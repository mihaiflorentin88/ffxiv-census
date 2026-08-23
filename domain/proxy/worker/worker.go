package worker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/handler"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Worker consumes proxy-related jobs from the queue via push-based consumption
// and dispatches them to registered handlers.
type Worker struct {
	queue    contract.Queue
	handlers *handler.Registry
	logger   contract.Logger
}

func New(q contract.Queue, h *handler.Registry, logger contract.Logger) *Worker {
	return &Worker{
		queue:    q,
		handlers: h,
		logger:   loggerOrDiscard(logger),
	}
}

func loggerOrDiscard(l contract.Logger) contract.Logger {
	if l == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return l
}

// Run runs the worker for a single event type.
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
			handler.EventNewProxy,
		}
	}

	for _, eventType := range eventTypes {
		if _, ok := w.handlers.Get(eventType); !ok {
			return fmt.Errorf("no handler registered for event %q", eventType)
		}
	}

	w.logger.InfoContext(ctx, "worker.start", slog.Any("event_types", eventTypes), slog.Int("concurrency", concurrency))

	processJob := func(ctx context.Context, job contract.QueueJob) error {
		h, ok := w.handlers.Get(job.Type)
		if !ok {
			w.logger.ErrorContext(ctx, "worker.missing_handler", slog.String("event_type", job.Type))
			return fmt.Errorf("no handler registered for event %s", job.Type)
		}

		start := time.Now()
		w.logger.InfoContext(
			ctx, "worker.job_start",
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
			next, err = h.Handle(ctx, job.Payload)
		}()

		if err != nil {
			w.logger.WarnContext(
				ctx, "worker.job_retry",
				slog.String("event_type", job.Type),
				slog.Duration("duration", time.Since(start)),
				slog.Any("error", err),
			)
			return err
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

	return w.queue.Consume(ctx, eventTypes, concurrency, processJob)
}
