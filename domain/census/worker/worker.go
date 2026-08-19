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

	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

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
			if err := w.eventWorkerLoop(childCtx, allTypes[0], allTypes, wid, true); err != nil && !errors.Is(err, context.Canceled) {
				w.logger.ErrorContext(childCtx, "worker.loop_error", slog.String("event_type", "retry"), slog.Int("worker_id", wid), slog.Any("error", err))
				errCh <- err
				cancel()
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
				if err := w.eventWorkerLoop(childCtx, primaryType, allTypes, wid, false); err != nil && !errors.Is(err, context.Canceled) {
					w.logger.ErrorContext(childCtx, "worker.loop_error", slog.String("event_type", primaryType), slog.Int("worker_id", wid), slog.Any("error", err))
					errCh <- err
					cancel()
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

func (w *Worker) eventWorkerLoop(ctx context.Context, primaryType string, allTypes []string, workerID int, isRetryWorker bool) error {
	// Determine the preferred claim mode based on worker role.
	// Retry worker prefers retries; new workers prefer new jobs.
	// Both fall back to ClaimModeAny when idle to prevent starvation.
	preferredMode := contract.ClaimModeNewOnly
	if isRetryWorker {
		preferredMode = contract.ClaimModeRetriesOnly
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		// 1. Check provider availability for primary dedicated event type
		primaryAvail := w.isEventTypeAvailable(primaryType)
		if primaryAvail {
			// Try to claim from primary dedicated queue using preferred mode
			jobs, err := w.queue.Claim(ctx, primaryType, 1, preferredMode)
			if err != nil && ctx.Err() == nil {
				w.logger.ErrorContext(ctx, "worker.claim_error", slog.String("event_type", primaryType), slog.Int("worker_id", workerID), slog.Any("error", err))
			}
			if len(jobs) > 0 {
				for _, job := range jobs {
					w.processJob(ctx, job, workerID)
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
			jobs, err := w.queue.ClaimMultiple(ctx, otherAvailable, 1, preferredMode)
			if err != nil && ctx.Err() == nil {
				w.logger.ErrorContext(ctx, "worker.claim_multiple_error", slog.Any("event_types", otherAvailable), slog.Int("worker_id", workerID), slog.Any("error", err))
			}
			if len(jobs) > 0 {
				for _, job := range jobs {
					w.processJob(ctx, job, workerID)
				}
				continue
			}
		}

		// 3. Fallback: try claiming with ClaimModeAny to prevent permanent starvation.
		// This means the retry goroutine can help with new jobs when no retries exist,
		// and new-job goroutines can help with retries when no new jobs exist.
		if primaryAvail {
			jobs, err := w.queue.Claim(ctx, primaryType, 1, contract.ClaimModeAny)
			if err != nil && ctx.Err() == nil {
				w.logger.ErrorContext(ctx, "worker.claim_error", slog.String("event_type", primaryType), slog.Int("worker_id", workerID), slog.Any("error", err))
			}
			if len(jobs) > 0 {
				for _, job := range jobs {
					w.processJob(ctx, job, workerID)
				}
				continue
			}
		}

		if len(otherAvailable) > 0 {
			jobs, err := w.queue.ClaimMultiple(ctx, otherAvailable, 1, contract.ClaimModeAny)
			if err != nil && ctx.Err() == nil {
				w.logger.ErrorContext(ctx, "worker.claim_multiple_error", slog.Any("event_types", otherAvailable), slog.Int("worker_id", workerID), slog.Any("error", err))
			}
			if len(jobs) > 0 {
				for _, job := range jobs {
					w.processJob(ctx, job, workerID)
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
			case <-ctx.Done():
				return nil
			case <-time.After(waitDuration):
				continue
			}
		}

		select {
		case <-ctx.Done():
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
	w.logger.InfoContext(ctx, "worker.job_start", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts))
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
		if rerr := w.queue.Retry(ctx, job.ID, err.Error()); rerr != nil {
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
