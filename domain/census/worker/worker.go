package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	proxydomain "github.com/mihaiflorentin88/ffxiv-census/domain/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Worker consumes jobs from configured event types and dispatches them to registered
// handlers using push-based consumption from the queue.
type Worker struct {
	queue       contract.Queue
	handlers    *handler.Registry
	logger      contract.Logger
	rateLimiter contract.ProviderRateLimiter
}

func New(q contract.Queue, h *handler.Registry, logger contract.Logger, rateLimiter ...contract.ProviderRateLimiter) *Worker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	var rl contract.ProviderRateLimiter
	if len(rateLimiter) > 0 {
		rl = rateLimiter[0]
	}
	return &Worker{
		queue:       q,
		handlers:    h,
		logger:      logger,
		rateLimiter: rl,
	}
}

// idleConnectionCloser is an optional interface for HTTP clients that support
// closing idle connections. This is a private structural interface so port
// contracts and fake clients do not gain a lifecycle method.
type idleConnectionCloser interface {
	CloseIdleConnections()
}

// closeIdleConnections closes idle connections if the client supports it.
func closeIdleConnections(client any) {
	if closer, ok := client.(idleConnectionCloser); ok {
		closer.CloseIdleConnections()
	}
}

func (w *Worker) Run(ctx context.Context, eventType string, concurrency int) error {
	return w.RunEvents(ctx, []string{eventType}, concurrency)
}

// RunEvents starts push-based consumption for all configured event types.
// The queue delivers messages to the handler; retry and dead-letter logic
// is handled internally by the queue adapter.
func (w *Worker) RunEvents(ctx context.Context, eventTypes []string, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 4
	}
	if len(eventTypes) == 0 {
		eventTypes = []string{
			handler.EventIDSweep,
			handler.EventCharacterCensus,
			handler.EventAchievementCensus,
		}
	}

	for _, eventType := range eventTypes {
		// Skip validation for failed queue names — they don't have handlers,
		// but messages from failed queues have the original event type as routing key.
		if strings.HasSuffix(eventType, ".failed") {
			continue
		}
		if _, ok := w.handlers.Get(eventType); !ok {
			return fmt.Errorf("no handler registered for event %q", eventType)
		}
	}

	w.logger.InfoContext(ctx, "Worker started", slog.Any("event_types", eventTypes), slog.Int("concurrency", concurrency))

	processJob := func(processCtx context.Context, job contract.QueueJob) error {
		h, ok := w.handlers.Get(job.Type)
		if !ok {
			w.logger.ErrorContext(processCtx, "No handler registered for event type", slog.String("event_type", job.Type))
			return fmt.Errorf("no handler registered for event %s", job.Type)
		}

		// Wait for required providers to become available before processing.
		// Use processCtx so a claimed job is not aborted by SIGTERM during
		// a rate-limit wait; cancellation only stops new claims.
		if w.rateLimiter != nil {
			if err := w.waitForProviders(processCtx, job.Type); err != nil {
				return err
			}
		}

		start := time.Now()
		w.logger.DebugContext(
			processCtx, "Processing job",
			slog.String("event_type", job.Type),
		)

		var next []contract.QueueJob
		var err error

		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("worker panic: %v\nstack: %s", r, debug.Stack())
				}
			}()
			next, err = h.Handle(processCtx, job.Payload)
		}()

		if err != nil {
			w.logger.WarnContext(
				processCtx, "Job failed, retrying",
				slog.String("event_type", job.Type),
				slog.Duration("duration", time.Since(start)),
				slog.Any("error", err),
			)
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
				select {
				case <-processCtx.Done():
					return err
				case <-time.After(200 * time.Millisecond):
				}
			}
			return err
		}

		// Publish downstream jobs individually.
		for _, nextJob := range next {
			if pubErr := w.queue.Publish(processCtx, nextJob); pubErr != nil {
				w.logger.ErrorContext(
					processCtx, "Failed to publish follow-up job",
					slog.String("event_type", nextJob.Type),
					slog.Any("error", pubErr),
				)
				return pubErr
			}
		}

		w.logger.InfoContext(
			processCtx, "Job completed successfully",
			slog.String("event_type", job.Type),
			slog.Duration("duration", time.Since(start)),
			slog.Int("chained", len(next)),
		)

		return nil
	}

	err := w.queue.Consume(ctx, eventTypes, concurrency, processJob)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// waitForProviders blocks until the providers required by the event type are available.
// - id-sweep: dual-source (Lodestone or Tomestone)
// - character-census: dual-source (Lodestone or Tomestone)
// - achievement-census: Lodestone-only
func (w *Worker) waitForProviders(ctx context.Context, eventType string) error {
	// Failed queue retries don't need provider checks — the original handler
	// will check providers when it processes the message.
	if strings.HasSuffix(eventType, ".failed") {
		return nil
	}
	switch eventType {
	case handler.EventAchievementCensus:
		// Lodestone-only: wait for Lodestone to become available.
		if !w.rateLimiter.IsAvailable(contract.ProviderLodestone) {
			w.logger.InfoContext(ctx, "Waiting for provider to become available", slog.String("provider", "lodestone"))
			return w.rateLimiter.WaitUntilAvailable(ctx, contract.ProviderLodestone)
		}
	case handler.EventIDSweep, handler.EventCharacterCensus:
		// Dual-source: if both are unavailable, wait for the earliest one.
		lodestoneAvail := w.rateLimiter.IsAvailable(contract.ProviderLodestone)
		tomestoneAvail := w.rateLimiter.IsAvailable(contract.ProviderTomestone)
		if !lodestoneAvail && !tomestoneAvail {
			w.logger.InfoContext(ctx, "Waiting for provider to become available", slog.String("provider", "any"))
			earliest := w.rateLimiter.EarliestAvailable()
			if !earliest.IsZero() {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Until(earliest)):
				}
			}
		}
	}
	return nil
}

// isProxyError returns true if the error indicates a proxy connection failure.
func isProxyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "host unreachable") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "Client.Timeout") ||
		strings.Contains(msg, "Service Unavailable") ||
		strings.Contains(msg, "proxyconnect")
}

// isRateLimitError returns true if the error indicates a 429 or rate-limit response.
// Rate-limit errors are provider cooldowns, not proxy failures — they must never
// trigger a proxy swap.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "429") || strings.Contains(msg, "rate limit")
}

// It retries on nil results and transient acquisition errors with exponential
// backoff (5s → 10s → 20s → 40s → 60s cap), logging only on the first attempt
// and when backoff reaches the cap. This prevents CPU and DB burn when fewer
// active proxies exist than configured goroutines.
func (w *Worker) waitForProxy(ctx context.Context, owner string, proxyHub *proxydomain.ProxyHub) (*proxydomain.Proxy, error) {
	backoff := 5 * time.Second
	const maxBackoff = 60 * time.Second
	firstAttempt := true
	capLogged := false

	for {
		p, err := proxyHub.NewProxy(ctx, owner)
		if err == nil && p != nil {
			return p, nil
		}

		if firstAttempt {
			if err != nil {
				w.logger.WarnContext(ctx, "No proxy available, waiting", slog.String("worker_id", owner), slog.Any("error", err))
			} else {
				w.logger.InfoContext(ctx, "No proxy available, waiting", slog.String("worker_id", owner))
			}
			firstAttempt = false
		} else if !capLogged && backoff >= maxBackoff {
			w.logger.InfoContext(ctx, "Proxy acquisition backing off", slog.String("worker_id", owner), slog.Duration("backoff", backoff))
			capLogged = true
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		case <-proxyHub.NotifyCh():
			timer.Stop()
			// Proxy became available — reset backoff and retry immediately.
			backoff = 5 * time.Second
			continue
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// replaceProxy acquires a replacement proxy, builds new clients/handlers, and
// only then releases or marks-failed the previous proxy. This ordering prevents
// a window where the worker holds no proxy at all.
//
// If markBad is true, the previous proxy is marked failed (inactive + incremented
// fail count). Otherwise it is released normally. MarkFailed and release errors
// are logged but do not prevent the replacement from being used.
func (w *Worker) replaceProxy(
	ctx context.Context,
	previous *proxydomain.Proxy,
	owner string,
	markBad bool,
	proxyHub *proxydomain.ProxyHub,
	newLodestoneClient func(string, contract.ProviderRateLimiter) (contract.LodestoneClient, error),
	newTomestoneClient func(string, contract.ProviderRateLimiter) (contract.TomestoneClient, error),
	newRateLimiter func() contract.ProviderRateLimiter,
	newHandlers func(contract.LodestoneClient, contract.TomestoneClient, contract.ProviderRateLimiter) *handler.Registry,
) (*proxydomain.Proxy, contract.LodestoneClient, contract.TomestoneClient, contract.ProviderRateLimiter, *handler.Registry, error) {
	// Mark/release the previous proxy BEFORE acquiring replacement.
	// This frees the slot so a pool with no spare capacity can rotate.
	if previous != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if markBad {
			if markErr := previous.MarkFailed(cleanupCtx, owner); markErr != nil {
				w.logger.WarnContext(cleanupCtx, "Failed to mark proxy as failed",
					slog.String("proxy_address", previous.Address()), slog.Any("error", markErr))
			}
		} else {
			if relErr := previous.Release(cleanupCtx, owner); relErr != nil {
				w.logger.WarnContext(cleanupCtx, "Failed to release proxy",
					slog.String("proxy_address", previous.Address()), slog.Any("error", relErr))
			}
		}
		cancel()
		// Wake one waiting worker now that a proxy slot is free.
		proxyHub.NotifyAvailable()
	}

	// Acquire replacement — now the previous slot is free.
	replacement, err := w.waitForProxy(ctx, owner, proxyHub)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("no checked proxy available: %w", err)
	}

	// Build new clients/handlers for the replacement.
	proxyLimiter := newRateLimiter()
	lodestoneClient, err := newLodestoneClient(replacement.Address(), proxyLimiter)
	if err != nil {
		if relErr := replacement.Release(context.Background(), owner); relErr != nil {
			w.logger.WarnContext(context.Background(), "Failed to release proxy during cleanup",
				slog.String("proxy_address", replacement.Address()), slog.Any("error", relErr))
		}
		return nil, nil, nil, nil, nil, fmt.Errorf("create lodestone client: %w", err)
	}
	tomestoneClient, err := newTomestoneClient(replacement.Address(), proxyLimiter)
	if err != nil {
		if relErr := replacement.Release(context.Background(), owner); relErr != nil {
			w.logger.WarnContext(context.Background(), "Failed to release proxy during cleanup",
				slog.String("proxy_address", replacement.Address()), slog.Any("error", relErr))
		}
		return nil, nil, nil, nil, nil, fmt.Errorf("create tomestone client: %w", err)
	}
	handlers := newHandlers(lodestoneClient, tomestoneClient, proxyLimiter)

	return replacement, lodestoneClient, tomestoneClient, proxyLimiter, handlers, nil
}

// RunEventsWithProxy runs the worker pool with per-goroutine proxy lifecycle.
// Each goroutine acquires its own proxy from the ProxyHub, creates proxy-aware
// clients, and uses them for all requests. If a proxy's ownership changes
// (CanUse returns false), the goroutine acquires a new proxy and retries the
// job in-place.
func (w *Worker) RunEventsWithProxy(
	ctx context.Context,
	eventTypes []string,
	concurrency int,
	ownerPrefix string,
	proxyHub *proxydomain.ProxyHub,
	newHandlers func(lodestone contract.LodestoneClient, tomestone contract.TomestoneClient, rateLimiter contract.ProviderRateLimiter) *handler.Registry,
	newLodestoneClient func(proxyURL string, limiter contract.ProviderRateLimiter) (contract.LodestoneClient, error),
	newTomestoneClient func(proxyURL string, limiter contract.ProviderRateLimiter) (contract.TomestoneClient, error),
	newRateLimiter func() contract.ProviderRateLimiter,
) error {
	if concurrency <= 0 {
		concurrency = 4
	}
	if len(eventTypes) == 0 {
		eventTypes = []string{
			handler.EventIDSweep,
			handler.EventCharacterCensus,
			handler.EventAchievementCensus,
		}
	}

	w.logger.InfoContext(ctx, "Proxy worker started", slog.Any("event_types", eventTypes), slog.Int("concurrency", concurrency))

	childCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	for i := range concurrency {
		workerID := i
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			if err := w.proxyWorkerLoop(ctx, childCtx, eventTypes, wid, ownerPrefix, proxyHub, newHandlers, newLodestoneClient, newTomestoneClient, newRateLimiter); err != nil && !errors.Is(err, context.Canceled) {
				w.logger.ErrorContext(childCtx, "Proxy worker loop error", slog.Int("worker_id", wid), slog.Any("error", err))
				errCh <- err
			}
		}(workerID)
	}

	wg.Wait()
	close(errCh)
	w.logger.InfoContext(ctx, "Proxy worker stopped", slog.String("owner", ownerPrefix))

	var errs []error
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// proxyWorkerLoop is the main loop for a single proxy-mode worker goroutine.
func (w *Worker) proxyWorkerLoop(
	claimCtx context.Context,
	processCtx context.Context,
	eventTypes []string,
	workerID int,
	ownerPrefix string,
	proxyHub *proxydomain.ProxyHub,
	newHandlers func(lodestone contract.LodestoneClient, tomestone contract.TomestoneClient, rateLimiter contract.ProviderRateLimiter) *handler.Registry,
	newLodestoneClient func(proxyURL string, limiter contract.ProviderRateLimiter) (contract.LodestoneClient, error),
	newTomestoneClient func(proxyURL string, limiter contract.ProviderRateLimiter) (contract.TomestoneClient, error),
	newRateLimiter func() contract.ProviderRateLimiter,
) error {
	owner := fmt.Sprintf("%s-w%d", ownerPrefix, workerID)

	// Acquire initial proxy — wait if none available.
	proxy, err := w.waitForProxy(claimCtx, owner, proxyHub)
	if err != nil {
		return fmt.Errorf("proxy acquire: %w", err)
	}
	w.logger.InfoContext(claimCtx, "Acquired proxy", slog.String("proxy_address", proxy.Address()))

	// Create proxy-aware clients and handlers.
	proxyLimiter := newRateLimiter()
	lodestoneClient, err := newLodestoneClient(proxy.Address(), proxyLimiter)
	if err != nil {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer releaseCancel()
		if relErr := proxy.Release(releaseCtx, owner); relErr != nil {
			w.logger.WarnContext(releaseCtx, "Failed to release proxy during cleanup", slog.String("proxy_address", proxy.Address()), slog.Any("error", relErr))
			return errors.Join(fmt.Errorf("create lodestone client: %w", err), fmt.Errorf("release proxy: %w", relErr))
		}
		return fmt.Errorf("create lodestone client: %w", err)
	}
	tomestoneClient, err := newTomestoneClient(proxy.Address(), proxyLimiter)
	if err != nil {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer releaseCancel()
		if relErr := proxy.Release(releaseCtx, owner); relErr != nil {
			w.logger.WarnContext(releaseCtx, "Failed to release proxy during cleanup", slog.String("proxy_address", proxy.Address()), slog.Any("error", relErr))
			return errors.Join(fmt.Errorf("create tomestone client: %w", err), fmt.Errorf("release proxy: %w", relErr))
		}
		return fmt.Errorf("create tomestone client: %w", err)
	}
	handlers := newHandlers(lodestoneClient, tomestoneClient, proxyLimiter)

	defer func() {
		closeIdleConnections(tomestoneClient)
		if proxy != nil {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer releaseCancel()
			if err := proxy.Release(releaseCtx, owner); err != nil {
				w.logger.WarnContext(releaseCtx, "Failed to release proxy", slog.String("proxy_address", proxy.Address()), slog.Any("error", err))
			} else {
				w.logger.InfoContext(releaseCtx, "Proxy worker stopped", slog.String("owner", owner))
				proxyHub.NotifyAvailable()
			}
		}
	}()

	// proxyWaitForProviders applies the same event-aware provider wait used by
	// waitForProviders, but using the proxy's own rate limiter.
	proxyWaitForProviders := func(ctx context.Context, eventType string) error {
		if proxyLimiter == nil {
			return nil
		}
		if strings.HasSuffix(eventType, ".failed") {
			return nil
		}
		switch eventType {
		case handler.EventAchievementCensus:
			if !proxyLimiter.IsAvailable(contract.ProviderLodestone) {
				w.logger.InfoContext(ctx, "Waiting for provider to become available", slog.String("provider", "lodestone"))
				return proxyLimiter.WaitUntilAvailable(ctx, contract.ProviderLodestone)
			}
		case handler.EventIDSweep, handler.EventCharacterCensus:
			lodestoneAvail := proxyLimiter.IsAvailable(contract.ProviderLodestone)
			tomestoneAvail := proxyLimiter.IsAvailable(contract.ProviderTomestone)
			if !lodestoneAvail && !tomestoneAvail {
				w.logger.InfoContext(ctx, "Waiting for provider to become available", slog.String("provider", "any"))
				earliest := proxyLimiter.EarliestAvailable()
				if !earliest.IsZero() {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(time.Until(earliest)):
					}
				}
			}
		}
		return nil
	}

	processJob := func(ctx context.Context, job contract.QueueJob) error {
		// Check proxy ownership and extend the lock.
		// Use a short-lived context so shutdown doesn't block lock extension.
		lockCtx, lockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		lockTTL := proxyHub.LockTTL()
		canUse, canErr := proxy.CanUse(lockCtx, owner, lockTTL)
		lockCancel()
		if canErr != nil {
			// Database error — return for queue retry, retain current proxy.
			w.logger.WarnContext(ctx, "Proxy worker loop error", slog.Int("worker_id", workerID), slog.Any("error", canErr))
			return fmt.Errorf("proxy canuse check: %w", canErr)
		}
		if !canUse {
			w.logger.InfoContext(ctx, "Proxy ownership lost, acquiring replacement", slog.Int("worker_id", workerID), slog.String("proxy_address", proxy.Address()))
			newProxy, newLodestone, newTomestone, newLimiter, newReg, rerr := w.replaceProxy(
				ctx, proxy, owner, false, proxyHub,
				newLodestoneClient, newTomestoneClient, newRateLimiter, newHandlers,
			)
			if rerr != nil {
				return fmt.Errorf("proxy re-acquire: %w", rerr)
			}
			proxy = newProxy
			lodestoneClient = newLodestone
			closeIdleConnections(tomestoneClient)
			tomestoneClient = newTomestone
			proxyLimiter = newLimiter
			handlers = newReg
			w.logger.InfoContext(ctx, "Acquired proxy", slog.String("proxy_address", proxy.Address()))
		}

		// Event-aware provider wait before handler call.
		if err := proxyWaitForProviders(ctx, job.Type); err != nil {
			return err
		}

		h, ok := handlers.Get(job.Type)
		if !ok {
			return fmt.Errorf("no handler registered for event %s", job.Type)
		}

		start := time.Now()
		w.logger.DebugContext(
			ctx, "Processing job",
			slog.String("event_type", job.Type),
			slog.Int("worker_id", workerID),
			slog.String("proxy_address", proxy.Address()),
			slog.String("owner", owner),
		)

		var next []contract.QueueJob
		var jobErr error

		func() {
			defer func() {
				if r := recover(); r != nil {
					jobErr = fmt.Errorf("worker panic: %v\nstack: %s", r, debug.Stack())
				}
			}()
			next, jobErr = h.Handle(ctx, job.Payload)
		}()

		if jobErr != nil {
			w.logger.WarnContext(
				ctx, "Job failed, retrying",
				slog.String("event_type", job.Type),
				slog.Duration("duration", time.Since(start)),
				slog.Any("error", jobErr),
			)

			// 429/rate-limit is a provider cooldown, not a dead proxy.
			// The client that received the 429 has already applied its own
			// provider cooldown via the rate limiter. Return the error for
			// normal queue retry — do NOT mark the proxy failed or acquire
			// a replacement, and do NOT infer the provider from event type.
			if isRateLimitError(jobErr) {
				return jobErr
			}

			// Proxy transport error: replace the proxy and retry once.
			if isProxyError(jobErr) {
				w.logger.InfoContext(ctx, "Proxy connection failed, replacing", slog.Int("worker_id", workerID), slog.String("proxy_address", proxy.Address()))
				newProxy, newLodestone, newTomestone, newLimiter, newReg, rerr := w.replaceProxy(
					ctx, proxy, owner, true, proxyHub,
					newLodestoneClient, newTomestoneClient, newRateLimiter, newHandlers,
				)
				if rerr != nil {
					return fmt.Errorf("proxy re-acquire after failure: %w", rerr)
				}
				proxy = newProxy
				lodestoneClient = newLodestone
				closeIdleConnections(tomestoneClient)
				tomestoneClient = newTomestone
				proxyLimiter = newLimiter
				handlers = newReg
				w.logger.InfoContext(ctx, "Acquired proxy", slog.String("proxy_address", proxy.Address()))

				// Retry the same delivery once through the replacement proxy.
				// Wait for providers before retrying.
				if err := proxyWaitForProviders(ctx, job.Type); err != nil {
					return err
				}
				retryH, retryOk := handlers.Get(job.Type)
				if !retryOk {
					return fmt.Errorf("no handler registered for event %s", job.Type)
				}

				retryStart := time.Now()
				w.logger.InfoContext(
					ctx, "Processing job",
					slog.String("event_type", job.Type),
					slog.Int("worker_id", workerID),
					slog.String("proxy_address", proxy.Address()),
					slog.String("owner", owner),
				)

				var retryNext []contract.QueueJob
				var retryErr error
				func() {
					defer func() {
						if r := recover(); r != nil {
							retryErr = fmt.Errorf("worker panic: %v\nstack: %s", r, debug.Stack())
						}
					}()
					retryNext, retryErr = retryH.Handle(ctx, job.Payload)
				}()

				if retryErr != nil {
					// If the retry also failed with a proxy error, mark the
					// replacement failed before returning to RabbitMQ.
					if isProxyError(retryErr) {
						w.logger.InfoContext(ctx, "Replacement proxy also failed", slog.Int("worker_id", workerID), slog.String("proxy_address", proxy.Address()))
						markCtx, markCancel := context.WithTimeout(context.Background(), 10*time.Second)
						if markErr := proxy.MarkFailed(markCtx, owner); markErr != nil {
							w.logger.WarnContext(markCtx, "Failed to mark proxy as failed",
								slog.String("proxy_address", proxy.Address()), slog.Any("error", markErr))
						}
						markCancel()
					}
					w.logger.WarnContext(
						ctx, "Job failed, retrying",
						slog.String("event_type", job.Type),
						slog.Duration("duration", time.Since(retryStart)),
						slog.Any("error", retryErr),
					)
					return retryErr
				}

				for _, nextJob := range retryNext {
					if pubErr := w.queue.Publish(ctx, nextJob); pubErr != nil {
						w.logger.ErrorContext(
							ctx, "Failed to publish follow-up job",
							slog.String("event_type", nextJob.Type),
							slog.Any("error", pubErr),
						)
						return pubErr
					}
				}
				w.logger.InfoContext(
					ctx, "Job completed successfully",
					slog.String("event_type", job.Type),
					slog.Duration("duration", time.Since(retryStart)),
					slog.Int("chained", len(retryNext)),
				)
				return nil
			}

			return jobErr
		}

		// Publish downstream jobs individually.
		for _, nextJob := range next {
			if pubErr := w.queue.Publish(ctx, nextJob); pubErr != nil {
				w.logger.ErrorContext(
					ctx, "Failed to publish follow-up job",
					slog.String("event_type", nextJob.Type),
					slog.Any("error", pubErr),
				)
				return pubErr
			}
		}

		w.logger.InfoContext(
			ctx, "Job completed successfully",
			slog.String("event_type", job.Type),
			slog.Duration("duration", time.Since(start)),
			slog.Int("chained", len(next)),
		)

		return nil
	}

	return w.queue.Consume(claimCtx, eventTypes, 1, processJob)
}
