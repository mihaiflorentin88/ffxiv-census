package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
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

	w.logger.InfoContext(ctx, "worker.start", slog.Any("event_types", eventTypes), slog.Int("concurrency", concurrency))

	// Capture the outer context (signal-aware) for shutdown checks.
	// The inner ctx from Consume is processCtx (not cancelled on shutdown).
	shutdownCtx := ctx

	processJob := func(processCtx context.Context, job contract.QueueJob) error {
		h, ok := w.handlers.Get(job.Type)
		if !ok {
			w.logger.ErrorContext(processCtx, "worker.missing_handler", slog.String("event_type", job.Type))
			return fmt.Errorf("no handler registered for event %s", job.Type)
		}

		// Wait for required providers to become available before processing.
		// Use shutdownCtx so we can exit during graceful shutdown.
		if w.rateLimiter != nil {
			if err := w.waitForProviders(shutdownCtx, job.Type); err != nil {
				return err
			}
		}

		start := time.Now()
		w.logger.InfoContext(
			processCtx, "worker.job_start",
			slog.String("event_type", job.Type),
			slog.String("handler", fmt.Sprintf("%p", h)),
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
				processCtx, "worker.job_retry",
				slog.String("event_type", job.Type),
				slog.Duration("duration", time.Since(start)),
				slog.Any("error", err),
			)
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
				select {
				case <-shutdownCtx.Done():
					return err
				case <-time.After(200 * time.Millisecond):
				}
			}
			return err
		}

		w.logger.InfoContext(
			processCtx, "worker.job_done",
			slog.String("event_type", job.Type),
			slog.Duration("duration", time.Since(start)),
			slog.Int("chained", len(next)),
		)

		// Publish downstream jobs individually.
		for _, nextJob := range next {
			if pubErr := w.queue.Publish(processCtx, nextJob); pubErr != nil {
				w.logger.ErrorContext(
					processCtx, "worker.publish_error",
					slog.String("event_type", nextJob.Type),
					slog.Any("error", pubErr),
				)
				return pubErr
			}
		}

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
			w.logger.InfoContext(ctx, "worker.waiting_for_provider", slog.String("event_type", eventType), slog.String("provider", "lodestone"))
			return w.rateLimiter.WaitUntilAvailable(ctx, contract.ProviderLodestone)
		}
	case handler.EventIDSweep, handler.EventCharacterCensus:
		// Dual-source: if both are unavailable, wait for the earliest one.
		lodestoneAvail := w.rateLimiter.IsAvailable(contract.ProviderLodestone)
		tomestoneAvail := w.rateLimiter.IsAvailable(contract.ProviderTomestone)
		if !lodestoneAvail && !tomestoneAvail {
			w.logger.InfoContext(ctx, "worker.waiting_for_provider", slog.String("event_type", eventType), slog.String("provider", "any"))
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

// goroutineID returns the current goroutine's ID for diagnostic logging.
func goroutineID() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	id := 0
	for i := len("goroutine "); i < n; i++ {
		if buf[i] < '0' || buf[i] > '9' {
			break
		}
		id = id*10 + int(buf[i]-'0')
	}
	return id
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
	proxyHub *proxydomain.ProxyHub,
	newHandlers func(lodestone contract.LodestoneClient, tomestone contract.TomestoneClient, rateLimiter contract.ProviderRateLimiter) *handler.Registry,
	newLodestoneClient func(proxyURL string) (contract.LodestoneClient, error),
	newTomestoneClient func(proxyURL string) (contract.TomestoneClient, error),
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

	w.logger.InfoContext(ctx, "worker.proxy_start", slog.Any("event_types", eventTypes), slog.Int("concurrency", concurrency))

	childCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	for i := range concurrency {
		workerID := i
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			if err := w.proxyWorkerLoop(ctx, childCtx, eventTypes, wid, proxyHub, newHandlers, newLodestoneClient, newTomestoneClient, newRateLimiter); err != nil && !errors.Is(err, context.Canceled) {
				w.logger.ErrorContext(childCtx, "worker.proxy_loop_error", slog.Int("worker_id", wid), slog.Any("error", err))
				errCh <- err
			}
		}(workerID)
	}

	wg.Wait()
	close(errCh)
	w.logger.InfoContext(ctx, "worker.proxy_stop", slog.Any("event_types", eventTypes))

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
	proxyHub *proxydomain.ProxyHub,
	newHandlers func(lodestone contract.LodestoneClient, tomestone contract.TomestoneClient, rateLimiter contract.ProviderRateLimiter) *handler.Registry,
	newLodestoneClient func(proxyURL string) (contract.LodestoneClient, error),
	newTomestoneClient func(proxyURL string) (contract.TomestoneClient, error),
	newRateLimiter func() contract.ProviderRateLimiter,
) error {
	owner := fmt.Sprintf("census-consume-g%d-p%d", workerID, runtime.NumGoroutine())

	// Acquire initial proxy.
	proxy, err := proxyHub.NewProxy(claimCtx, owner)
	if err != nil {
		return fmt.Errorf("proxy acquire: %w", err)
	}
	if proxy == nil {
		return fmt.Errorf("no proxy available for worker %d", workerID)
	}
	w.logger.InfoContext(claimCtx, "worker.proxy_acquired", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()), slog.String("owner", owner))

	// Create proxy-aware clients and handlers.
	proxyLimiter := newRateLimiter()
	lodestoneClient, err := newLodestoneClient(proxy.Address())
	if err != nil {
		return fmt.Errorf("create lodestone client: %w", err)
	}
	tomestoneClient, err := newTomestoneClient(proxy.Address())
	if err != nil {
		return fmt.Errorf("create tomestone client: %w", err)
	}
	handlers := newHandlers(lodestoneClient, tomestoneClient, proxyLimiter)

	// Use queue.Consume with a handler that uses proxy-aware clients.
	// Release proxy on exit (graceful shutdown or error).
	defer func() {
		if proxy != nil {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer releaseCancel()
			if err := proxy.Release(releaseCtx, owner); err != nil {
				w.logger.WarnContext(releaseCtx, "worker.proxy_release_error", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()), slog.Any("error", err))
			} else {
				w.logger.InfoContext(releaseCtx, "worker.proxy_released", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()), slog.String("owner", owner))
			}
		}
	}()

	processJob := func(ctx context.Context, job contract.QueueJob) error {
		// Check proxy ownership and extend the lock.
		// Use a short-lived context so shutdown doesn't block lock extension.
		lockCtx, lockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		lockTTL := proxyHub.LockTTL()
		canUse, canErr := proxy.CanUse(lockCtx, owner, lockTTL)
		lockCancel()
		if canErr != nil {
			w.logger.WarnContext(ctx, "worker.proxy_canuse_error", slog.Int("worker_id", workerID), slog.Any("error", canErr))
		}
		if !canUse {
			w.logger.InfoContext(ctx, "worker.proxy_lost", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()), slog.String("owner", owner))
			acquireCtx, acquireCancel := context.WithTimeout(context.Background(), 10*time.Second)
			newProxy, perr := proxyHub.NewProxy(acquireCtx, owner)
			acquireCancel()
			if perr != nil {
				return fmt.Errorf("proxy re-acquire: %w", perr)
			}
			if newProxy == nil {
				return fmt.Errorf("no proxy available for worker %d", workerID)
			}
			// Release old proxy before switching.
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = proxy.Release(releaseCtx, owner)
			releaseCancel()
			proxy = newProxy
			w.logger.InfoContext(ctx, "worker.proxy_reacquired", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()), slog.String("owner", owner))

			proxyLimiter = newRateLimiter()
			lodestoneClient, err = newLodestoneClient(proxy.Address())
			if err != nil {
				return fmt.Errorf("create lodestone client: %w", err)
			}
			tomestoneClient, err = newTomestoneClient(proxy.Address())
			if err != nil {
				return fmt.Errorf("create tomestone client: %w", err)
			}
			handlers = newHandlers(lodestoneClient, tomestoneClient, proxyLimiter)
		}

		h, ok := handlers.Get(job.Type)
		if !ok {
			return fmt.Errorf("no handler registered for event %s", job.Type)
		}

		start := time.Now()
		w.logger.InfoContext(
			ctx, "worker.job_start",
			slog.String("event_type", job.Type),
			slog.Int("worker_id", workerID),
			slog.String("proxy", proxy.Address()),
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
				ctx, "worker.job_retry",
				slog.String("event_type", job.Type),
				slog.Duration("duration", time.Since(start)),
				slog.Any("error", jobErr),
			)
			if isProxyError(jobErr) {
				w.logger.InfoContext(ctx, "worker.proxy_bad", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()))
				_ = proxy.MarkFailed(context.Background(), owner)
				newProxy, perr := proxyHub.NewProxy(ctx, owner)
				if perr != nil {
					return fmt.Errorf("proxy re-acquire after failure: %w", perr)
				}
				if newProxy == nil {
					return fmt.Errorf("no proxy available for worker %d", workerID)
				}
				proxy = newProxy
				w.logger.InfoContext(ctx, "worker.proxy_reacquired", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()), slog.String("owner", owner))
				proxyLimiter = newRateLimiter()
				lodestoneClient, err = newLodestoneClient(proxy.Address())
				if err != nil {
					return fmt.Errorf("create lodestone client: %w", err)
				}
				tomestoneClient, err = newTomestoneClient(proxy.Address())
				if err != nil {
					return fmt.Errorf("create tomestone client: %w", err)
				}
				handlers = newHandlers(lodestoneClient, tomestoneClient, proxyLimiter)
			}
			return jobErr
		}

		w.logger.InfoContext(
			ctx, "worker.job_done",
			slog.String("event_type", job.Type),
			slog.Duration("duration", time.Since(start)),
			slog.Int("chained", len(next)),
		)

		// Publish downstream jobs individually.
		for _, nextJob := range next {
			if pubErr := w.queue.Publish(ctx, nextJob); pubErr != nil {
				w.logger.ErrorContext(
					ctx, "worker.publish_error",
					slog.String("event_type", nextJob.Type),
					slog.Any("error", pubErr),
				)
				return pubErr
			}
		}

		return nil
	}

	return w.queue.Consume(claimCtx, eventTypes, 1, processJob)
}
