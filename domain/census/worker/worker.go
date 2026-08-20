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

// Worker claims jobs from configured event types and dispatches them to registered
// handlers using a dynamic Dispatcher and Worker Pool pattern.
type Worker struct {
	queue        contract.Queue
	handlers     *handler.Registry
	logger       contract.Logger
	rateLimiter  contract.ProviderRateLimiter
	pollInterval time.Duration
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
		queue:        q,
		handlers:     h,
		logger:       logger,
		rateLimiter:  rl,
		pollInterval: 500 * time.Millisecond,
	}
}

// SetPollInterval configures the worker's polling interval.
func (w *Worker) SetPollInterval(d time.Duration) {
	if d > 0 {
		w.pollInterval = d
	}
}

func (w *Worker) Run(ctx context.Context, eventType string, concurrency int) error {
	return w.RunEvents(ctx, []string{eventType}, concurrency)
}

// RunEvents runs dedicated concurrent worker pools for all configured event types simultaneously.
// It reserves 1 goroutine (workerID 0) as a dedicated retry worker that preferentially
// claims retry jobs (attempts > 1). All other goroutines preferentially claim new jobs.
// Concurrency is clamped to len(eventTypes)+1 minimum to guarantee at least 1 goroutine
// per event type plus 1 retry goroutine.
func (w *Worker) RunEvents(ctx context.Context, eventTypes []string, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 4
	}
	if len(eventTypes) == 0 {
		eventTypes = []string{
			handler.EventIDSweep,
			handler.EventCharacterCensus,
			handler.EventAchievementCensus,
			handler.EventFreeCompanyCensus,
		}
	}

	for _, eventType := range eventTypes {
		if _, ok := w.handlers.Get(eventType); !ok {
			return fmt.Errorf("no handler registered for event %q", eventType)
		}
	}

	// Clamp concurrency to guarantee at least 1 goroutine per event type + 1 retry goroutine.
	minConcurrency := len(eventTypes) + 1
	if concurrency < minConcurrency {
		w.logger.InfoContext(ctx, "worker.concurrency_clamped", slog.Int("requested", concurrency), slog.Int("minimum", minConcurrency))
		concurrency = minConcurrency
	}

	w.logger.InfoContext(ctx, "worker.start", slog.Any("event_types", eventTypes), slog.Int("concurrency", concurrency))
	for _, eventType := range eventTypes {
		if n, err := w.queue.CountJobs(ctx, contract.QueueJobFilter{Type: eventType, Status: contract.QueueJobPending}); err == nil && n == 0 {
			w.logger.InfoContext(ctx, "worker.queue_status", slog.String("event_type", eventType), slog.Int64("pending_jobs", 0), slog.String("notice", "no pending jobs in queue, waiting for new publications..."))
		}
		if n, err := w.queue.ReclaimClaimed(ctx, eventType); err != nil {
			return fmt.Errorf("reclaim claimed jobs for %s: %w", eventType, err)
		} else if n > 0 {
			w.logger.InfoContext(ctx, "worker.reclaimed", slog.String("event_type", eventType), slog.Int("reclaimed", n))
		}
	}

	childCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// stopClaiming is cancelled when the signal context fires, causing worker loops
	// to stop claiming new jobs. childCtx stays alive so in-flight processJob calls
	// can finish their Handle/Complete/Retry using a live context.
	stopClaiming, stopClaimingCancel := context.WithCancel(context.Background())
	defer stopClaimingCancel()

	go func() {
		<-ctx.Done()
		w.logger.InfoContext(ctx, "worker.draining", slog.String("signal", "stop claiming new jobs, waiting for in-flight jobs to complete"))
		stopClaimingCancel()
	}()

	// Reserve 1 goroutine for retries, distribute the rest evenly across event types.
	retryWorkers := 1
	newWorkers := concurrency - retryWorkers
	workersPerEvent := newWorkers / len(eventTypes) // always >= 1 due to clamping
	extraWorkers := newWorkers - (workersPerEvent * len(eventTypes))

	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	workerIDCounter := 0

	// Spawn the retry goroutine first (workerID 0).
	w.logger.InfoContext(ctx, "worker.pool_allocated", slog.String("event_type", "retry"), slog.Int("workers", retryWorkers), slog.String("mode", "retries_only"))
	for range retryWorkers {
		workerID := workerIDCounter
		workerIDCounter++
		wg.Add(1)
		go func(allTypes []string, wid int) {
			defer wg.Done()
			if err := w.eventWorkerLoop(stopClaiming, childCtx, allTypes[0], allTypes, wid, true); err != nil && !errors.Is(err, context.Canceled) {
				w.logger.ErrorContext(childCtx, "worker.loop_error", slog.String("event_type", "retry"), slog.Int("worker_id", wid), slog.Any("error", err))
				errCh <- err
			}
		}(eventTypes, workerID)
	}

	// Spawn new-job goroutines for each event type.
	for i, eventType := range eventTypes {
		numWorkers := workersPerEvent
		if i < extraWorkers {
			numWorkers++
		}
		w.logger.InfoContext(ctx, "worker.pool_allocated", slog.String("event_type", eventType), slog.Int("workers", numWorkers), slog.String("mode", "new_only"))

		for range numWorkers {
			workerID := workerIDCounter
			workerIDCounter++
			wg.Add(1)
			go func(primaryType string, allTypes []string, wid int) {
				defer wg.Done()
				if err := w.eventWorkerLoop(stopClaiming, childCtx, primaryType, allTypes, wid, false); err != nil && !errors.Is(err, context.Canceled) {
					w.logger.ErrorContext(childCtx, "worker.loop_error", slog.String("event_type", primaryType), slog.Int("worker_id", wid), slog.Any("error", err))
					errCh <- err
				}
			}(eventType, eventTypes, workerID)
		}
	}
	wg.Wait()
	close(errCh)
	w.logger.InfoContext(ctx, "worker.stop", slog.Any("event_types", eventTypes))

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

func (w *Worker) isEventTypeAvailable(eventType string) bool {
	if w.rateLimiter == nil {
		return true
	}
	switch eventType {
	case handler.EventAchievementCensus, handler.EventFreeCompanyCensus:
		return w.rateLimiter.IsAvailable(contract.ProviderLodestone)
	case handler.EventCharacterCensus, handler.EventIDSweep:
		return w.rateLimiter.IsAvailable(contract.ProviderLodestone) || w.rateLimiter.IsAvailable(contract.ProviderTomestone)
	default:
		return true
	}
}

func (w *Worker) eventWorkerLoop(claimCtx context.Context, processCtx context.Context, primaryType string, allTypes []string, workerID int, isRetryWorker bool) error {
	// Determine the preferred claim mode based on worker role.
	// Retry worker prefers retries; new workers prefer new jobs.
	// Both fall back to ClaimModeAny when idle to prevent starvation.
	preferredMode := contract.ClaimModeNewOnly
	if isRetryWorker {
		preferredMode = contract.ClaimModeRetriesOnly
	}

	for {
		if claimCtx.Err() != nil {
			return nil
		}

		// 1. Check provider availability for primary dedicated event type
		primaryAvail := w.isEventTypeAvailable(primaryType)
		if primaryAvail {
			// Try to claim from primary dedicated queue using preferred mode
			jobs, err := w.queue.Claim(claimCtx, primaryType, 1, preferredMode)
			if err != nil && claimCtx.Err() == nil {
				w.logger.ErrorContext(claimCtx, "worker.claim_error", slog.String("event_type", primaryType), slog.Int("worker_id", workerID), slog.Any("error", err))
			}
			if len(jobs) > 0 {
				for _, job := range jobs {
					w.processJob(processCtx, job, workerID)
				}
				continue
			}
		}

		// 2. If primary queue is empty (or paused), dynamically borrow work from other available queues
		var otherAvailable []string
		for _, et := range allTypes {
			if et != primaryType && w.isEventTypeAvailable(et) {
				otherAvailable = append(otherAvailable, et)
			}
		}

		if len(otherAvailable) > 0 {
			jobs, err := w.queue.ClaimMultiple(claimCtx, otherAvailable, 1, preferredMode)
			if err != nil && claimCtx.Err() == nil {
				w.logger.ErrorContext(claimCtx, "worker.claim_multiple_error", slog.Any("event_types", otherAvailable), slog.Int("worker_id", workerID), slog.Any("error", err))
			}
			if len(jobs) > 0 {
				for _, job := range jobs {
					w.processJob(processCtx, job, workerID)
				}
				continue
			}
		}

		// 3. Fallback: try claiming with ClaimModeAny to prevent permanent starvation.
		// This means the retry goroutine can help with new jobs when no retries exist,
		// and new-job goroutines can help with retries when no new jobs exist.
		if primaryAvail {
			jobs, err := w.queue.Claim(claimCtx, primaryType, 1, contract.ClaimModeAny)
			if err != nil && claimCtx.Err() == nil {
				w.logger.ErrorContext(claimCtx, "worker.claim_error", slog.String("event_type", primaryType), slog.Int("worker_id", workerID), slog.Any("error", err))
			}
			if len(jobs) > 0 {
				for _, job := range jobs {
					w.processJob(processCtx, job, workerID)
				}
				continue
			}
		}

		if len(otherAvailable) > 0 {
			jobs, err := w.queue.ClaimMultiple(claimCtx, otherAvailable, 1, contract.ClaimModeAny)
			if err != nil && claimCtx.Err() == nil {
				w.logger.ErrorContext(claimCtx, "worker.claim_multiple_error", slog.Any("event_types", otherAvailable), slog.Int("worker_id", workerID), slog.Any("error", err))
			}
			if len(jobs) > 0 {
				for _, job := range jobs {
					w.processJob(processCtx, job, workerID)
				}
				continue
			}
		}

		// 4. If all queues are empty or paused, wait for poll interval or earliest rate limit cooldown
		if !primaryAvail && len(otherAvailable) == 0 && w.rateLimiter != nil {
			earliest := w.rateLimiter.EarliestAvailable()
			waitDuration := time.Until(earliest)
			if waitDuration <= 0 {
				waitDuration = w.pollInterval
			}
			select {
			case <-claimCtx.Done():
				return nil
			case <-time.After(waitDuration):
				continue
			}
		}

		select {
		case <-claimCtx.Done():
			return nil
		case <-time.After(w.pollInterval):
			continue
		}
	}
}

func (w *Worker) processJob(ctx context.Context, job contract.QueueJob, workerID int) {
	h, ok := w.handlers.Get(job.Type)
	if !ok {
		w.logger.ErrorContext(ctx, "worker.missing_handler", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID))
		_ = w.queue.Fail(ctx, job.ID, fmt.Sprintf("no handler registered for event %s", job.Type))
		return
	}

	start := time.Now()
	w.logger.InfoContext(
		ctx, "worker.job_start",
		slog.String("event_type", job.Type),
		slog.Int64("job_id", job.ID),
		slog.Int("attempts", job.Attempts),
		slog.Int("worker_id", workerID),
		slog.Int("goroutine_id", goroutineID()),
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
		next, err = h.Handle(ctx, job.Payload)
	}()

	if err != nil {
		w.logger.WarnContext(ctx, "worker.job_retry", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts), slog.Duration("duration", time.Since(start)), slog.Any("error", err))
		// Use background context — job must return to queue even during shutdown.
		if rerr := w.queue.Retry(context.Background(), job.ID, err.Error()); rerr != nil {
			w.logger.ErrorContext(ctx, "worker.retry_error", slog.Int64("job_id", job.ID), slog.Any("error", rerr))
		}
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		return
	}

	w.logger.InfoContext(ctx, "worker.job_done", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts), slog.Duration("duration", time.Since(start)), slog.Int("chained", len(next)))
	if err := w.queue.Complete(ctx, job.ID, next...); err != nil {
		w.logger.ErrorContext(ctx, "worker.complete_error", slog.Int64("job_id", job.ID), slog.Any("error", err))
	}
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

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

func filterMatching(slice []string, targets []string) []string {
	var out []string
	for _, s := range slice {
		if contains(targets, s) {
			out = append(out, s)
		}
	}
	return out
}

func filterNotMatching(slice []string, excluded []string) []string {
	var out []string
	for _, s := range slice {
		if !contains(excluded, s) {
			out = append(out, s)
		}
	}
	return out
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
			handler.EventFreeCompanyCensus,
		}
	}

	w.logger.InfoContext(ctx, "worker.proxy_start", slog.Any("event_types", eventTypes), slog.Int("concurrency", concurrency))

	// Reclaim claimed jobs from previous crashed/restarted pods.
	for _, eventType := range eventTypes {
		if n, err := w.queue.ReclaimClaimed(ctx, eventType); err != nil {
			return fmt.Errorf("reclaim claimed jobs for %s: %w", eventType, err)
		} else if n > 0 {
			w.logger.InfoContext(ctx, "worker.reclaimed", slog.String("event_type", eventType), slog.Int("reclaimed", n))
		}
	}

	childCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopClaiming, stopClaimingCancel := context.WithCancel(context.Background())
	defer stopClaimingCancel()

	go func() {
		<-ctx.Done()
		w.logger.InfoContext(ctx, "worker.proxy_draining", slog.String("signal", "stop claiming new jobs, waiting for in-flight jobs to complete"))
		stopClaimingCancel()
	}()

	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	for i := range concurrency {
		workerID := i
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			if err := w.proxyWorkerLoop(stopClaiming, childCtx, eventTypes, wid, proxyHub, newHandlers, newLodestoneClient, newTomestoneClient, newRateLimiter); err != nil && !errors.Is(err, context.Canceled) {
				w.logger.ErrorContext(childCtx, "worker.proxy_loop_error", slog.Int("worker_id", wid), slog.Any("error", err))
				errCh <- err
				// Don't cancel childCtx — let other workers finish their in-flight jobs gracefully.
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

	for {
		if claimCtx.Err() != nil {
			// Release proxy on shutdown.
			_ = proxy.Release(context.Background(), owner)
			return nil
		}

		// Check proxy ownership and extend the lock in one atomic call.
		lockTTL := proxyHub.LockTTL()
		canUse, canErr := proxy.CanUse(claimCtx, owner, lockTTL)
		if canErr != nil {
			w.logger.WarnContext(claimCtx, "worker.proxy_canuse_error", slog.Int("worker_id", workerID), slog.Any("error", canErr))
		}
		if !canUse {
			w.logger.InfoContext(claimCtx, "worker.proxy_lost", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()), slog.String("owner", owner))
			// Acquire new proxy.
			newProxy, perr := proxyHub.NewProxy(claimCtx, owner)
			if perr != nil {
				return fmt.Errorf("proxy re-acquire: %w", perr)
			}
			if newProxy == nil {
				// No proxy available, wait and retry.
				select {
				case <-claimCtx.Done():
					return nil
				case <-time.After(w.pollInterval):
					continue
				}
			}
			// Release old proxy (best effort).
			_ = proxy.Release(context.Background(), owner)
			proxy = newProxy
			w.logger.InfoContext(claimCtx, "worker.proxy_reacquired", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()), slog.String("owner", owner))

			// Recreate clients with new proxy.
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

		// Try to claim a job from any available event type.
		jobs, err := w.queue.ClaimMultiple(claimCtx, eventTypes, 1, contract.ClaimModeAny)
		if err != nil && claimCtx.Err() == nil {
			w.logger.ErrorContext(claimCtx, "worker.proxy_claim_error", slog.Int("worker_id", workerID), slog.Any("error", err))
		}

		if len(jobs) > 0 {
			for _, job := range jobs {
				// Process with proxy-aware handlers.
				badProxy := w.processJobWithHandlers(processCtx, job, workerID, handlers, proxy, owner, proxyHub)
				if badProxy {
					w.logger.InfoContext(claimCtx, "worker.proxy_bad", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()))
					// Mark proxy as failed (increments fail count, sets inactive) and acquire a new one.
					_ = proxy.MarkFailed(context.Background(), owner)
					newProxy, perr := proxyHub.NewProxy(claimCtx, owner)
					if perr != nil {
						return fmt.Errorf("proxy re-acquire after failure: %w", perr)
					}
					if newProxy == nil {
						select {
						case <-claimCtx.Done():
							return nil
						case <-time.After(w.pollInterval):
							continue
						}
					}
					proxy = newProxy
					w.logger.InfoContext(claimCtx, "worker.proxy_reacquired", slog.Int("worker_id", workerID), slog.String("proxy", proxy.Address()), slog.String("owner", owner))
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
			}
			continue
		}

		// No jobs available, wait.
		select {
		case <-claimCtx.Done():
			_ = proxy.Release(context.Background(), owner)
			return nil
		case <-time.After(w.pollInterval):
			continue
		}
	}
}

// processJobWithHandlers processes a job using the provided handlers, with proxy re-acquisition on ownership change.
// Returns true if the job failed due to a proxy-related error (connection refused, timeout, host unreachable).
func (w *Worker) processJobWithHandlers(ctx context.Context, job contract.QueueJob, workerID int, handlers *handler.Registry, proxy *proxydomain.Proxy, owner string, proxyHub *proxydomain.ProxyHub) bool {
	h, ok := handlers.Get(job.Type)
	if !ok {
		w.logger.ErrorContext(ctx, "worker.missing_handler", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID))
		_ = w.queue.Fail(ctx, job.ID, fmt.Sprintf("no handler registered for event %s", job.Type))
		return false
	}

	start := time.Now()
	w.logger.InfoContext(
		ctx, "worker.job_start",
		slog.String("event_type", job.Type),
		slog.Int64("job_id", job.ID),
		slog.Int("attempts", job.Attempts),
		slog.Int("worker_id", workerID),
		slog.Int("goroutine_id", goroutineID()),
		slog.String("handler", fmt.Sprintf("%p", h)),
		slog.String("proxy", proxy.Address()),
		slog.String("owner", owner),
	)
	var next []contract.QueueJob
	var err error

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("worker panic: %v\nstack: %s", r, debug.Stack())
			}
		}()
		next, err = h.Handle(ctx, job.Payload)
	}()

	if err != nil {
		w.logger.WarnContext(ctx, "worker.job_retry", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts), slog.Duration("duration", time.Since(start)), slog.Any("error", err))
		// Use background context — job must return to queue even during shutdown.
		if rerr := w.queue.Retry(context.Background(), job.ID, err.Error()); rerr != nil {
			w.logger.ErrorContext(ctx, "worker.retry_error", slog.Int64("job_id", job.ID), slog.Any("error", rerr))
		}
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(200 * time.Millisecond):
			}
		}
		return isProxyError(err)
	}

	w.logger.InfoContext(ctx, "worker.job_done", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts), slog.Duration("duration", time.Since(start)), slog.Int("chained", len(next)))
	if err := w.queue.Complete(ctx, job.ID, next...); err != nil {
		w.logger.ErrorContext(ctx, "worker.complete_error", slog.Int64("job_id", job.ID), slog.Any("error", err))
	}
	return false
}
