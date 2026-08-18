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
// handlers. It checks provider availability before claiming jobs to pause only affected
// provider queues during rate limiting (e.g. 429s).
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
		pollInterval: time.Second,
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

// RunEvents runs the worker consuming jobs from multiple event types concurrently.
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

	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			if err := w.multiLoop(childCtx, eventTypes, workerID); err != nil && !errors.Is(err, context.Canceled) {
				w.logger.ErrorContext(childCtx, "worker.loop_error", slog.Int("worker_id", workerID), slog.Any("error", err))
				errCh <- err
				cancel() // tear down sibling goroutines
			}
		}(i)
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
	case handler.EventCharacterCensus, handler.EventAchievementCensus, handler.EventFreeCompanyCensus:
		return w.rateLimiter.IsAvailable(contract.ProviderLodestone)
	case handler.EventIDSweep:
		// ID sweep can use either lodestone or tomestone; claim if at least one is available
		return w.rateLimiter.IsAvailable(contract.ProviderLodestone) || w.rateLimiter.IsAvailable(contract.ProviderTomestone)
	default:
		return true
	}
}
func (w *Worker) multiLoop(ctx context.Context, eventTypes []string, workerID int) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		// Filter available event types based on provider rate limit status
		var availableTypes []string
		for _, et := range eventTypes {
			if w.isEventTypeAvailable(et) {
				availableTypes = append(availableTypes, et)
			}
		}

		// If all configured event queues are currently paused, wait until the earliest provider becomes available
		if len(availableTypes) == 0 {
			if w.rateLimiter != nil {
				earliest := w.rateLimiter.EarliestAvailable()
				waitDuration := time.Until(earliest)
				if waitDuration <= 0 {
					waitDuration = w.pollInterval
				}
				w.logger.WarnContext(ctx, "worker.all_providers_paused",
					slog.Int("worker_id", workerID),
					slog.Duration("wait_duration", waitDuration),
					slog.Time("earliest_available", earliest),
				)
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

		jobs, err := w.queue.ClaimMultiple(ctx, availableTypes, 1)
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			w.logger.ErrorContext(ctx, "worker.claim_error", slog.Any("event_types", availableTypes), slog.Any("error", err))
			return fmt.Errorf("claim multiple %v: %w", availableTypes, err)
		}
		if len(jobs) == 0 {
			w.logger.DebugContext(ctx, "worker.idle", slog.Any("event_types", availableTypes))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(w.pollInterval):
				continue
			}
		}

		for _, job := range jobs {
			h, ok := w.handlers.Get(job.Type)
			if !ok {
				w.logger.ErrorContext(ctx, "worker.missing_handler", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID))
				_ = w.queue.Fail(ctx, job.ID, fmt.Sprintf("no handler registered for event %s", job.Type))
				continue
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
					if ctx.Err() != nil {
						return nil // clean shutdown
					}
					return fmt.Errorf("retry job %d: %w", job.ID, rerr)
				}
				if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(200 * time.Millisecond):
					}
				}
				continue
			}
			w.logger.InfoContext(ctx, "worker.job_done", slog.String("event_type", job.Type), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts), slog.Duration("duration", time.Since(start)), slog.Int("chained", len(next)))
			if err := w.queue.Complete(ctx, job.ID, next...); err != nil {
				if ctx.Err() != nil {
					return nil // clean shutdown
				}
				return fmt.Errorf("complete job %d: %w", job.ID, err)
			}
		}
	}
}
