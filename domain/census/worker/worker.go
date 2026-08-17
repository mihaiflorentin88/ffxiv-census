package worker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Worker claims jobs of one event type and dispatches them to the registered
// handler. Handler errors are retried (backoff/max-attempts enforced by the
// queue); success publishes the handler's returned jobs atomically.
type Worker struct {
	queue        contract.Queue
	handlers     *handler.Registry
	logger       contract.Logger
	pollInterval time.Duration
}

func New(q contract.Queue, h *handler.Registry, logger contract.Logger) *Worker {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Worker{queue: q, handlers: h, logger: logger, pollInterval: time.Second}
}

func (w *Worker) Run(ctx context.Context, eventType string, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 4
	}
	h, ok := w.handlers.Get(eventType)
	if !ok {
		w.logger.ErrorContext(ctx, "worker.error", slog.String("event_type", eventType), slog.String("error", "no handler registered"))
		return fmt.Errorf("no handler registered for event %q", eventType)
	}
	w.logger.InfoContext(ctx, "worker.start", slog.String("event_type", eventType), slog.Int("concurrency", concurrency))
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.loop(ctx, eventType, h); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	w.logger.InfoContext(ctx, "worker.stop", slog.String("event_type", eventType))
	return nil
}

func (w *Worker) loop(ctx context.Context, eventType string, h handler.Handler) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		jobs, err := w.queue.Claim(ctx, eventType, 1)
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			w.logger.ErrorContext(ctx, "worker.claim_error", slog.String("event_type", eventType), slog.Any("error", err))
			return fmt.Errorf("claim %s: %w", eventType, err)
		}
		if len(jobs) == 0 {
			w.logger.DebugContext(ctx, "worker.idle", slog.String("event_type", eventType))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(w.pollInterval):
				continue
			}
		}
		for _, job := range jobs {
			start := time.Now()
			w.logger.InfoContext(ctx, "worker.job_start", slog.String("event_type", eventType), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts))
			next, err := h.Handle(ctx, job.Payload)
			if err != nil {
				w.logger.WarnContext(ctx, "worker.job_retry", slog.String("event_type", eventType), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts), slog.Duration("duration", time.Since(start)), slog.Any("error", err))
				if rerr := w.queue.Retry(ctx, job.ID); rerr != nil {
					if ctx.Err() != nil {
						return nil // clean shutdown
					}
					return fmt.Errorf("retry job %d: %w", job.ID, rerr)
				}
				continue
			}
			w.logger.InfoContext(ctx, "worker.job_done", slog.String("event_type", eventType), slog.Int64("job_id", job.ID), slog.Int("attempts", job.Attempts), slog.Duration("duration", time.Since(start)), slog.Int("chained", len(next)))
			if err := w.queue.Complete(ctx, job.ID, next...); err != nil {
				if ctx.Err() != nil {
					return nil // clean shutdown
				}
				return fmt.Errorf("complete job %d: %w", job.ID, err)
			}
		}
	}
}
