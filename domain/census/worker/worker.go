package worker

import (
	"context"
	"fmt"
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
	pollInterval time.Duration
}

func New(q contract.Queue, h *handler.Registry) *Worker {
	return &Worker{queue: q, handlers: h, pollInterval: time.Second}
}

func (w *Worker) Run(ctx context.Context, eventType string, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 4
	}
	h, ok := w.handlers.Get(eventType)
	if !ok {
		return fmt.Errorf("no handler registered for event %q", eventType)
	}
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
	return nil
}

func (w *Worker) loop(ctx context.Context, eventType string, h handler.Handler) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		jobs, err := w.queue.Claim(ctx, eventType, 1)
		if err != nil {
			return fmt.Errorf("claim %s: %w", eventType, err)
		}
		if len(jobs) == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(w.pollInterval):
				continue
			}
		}
		for _, job := range jobs {
			next, err := h.Handle(ctx, job.Payload)
			if err != nil {
				if rerr := w.queue.Retry(ctx, job.ID); rerr != nil {
					return fmt.Errorf("retry job %d: %w", job.ID, rerr)
				}
				continue
			}
			if err := w.queue.Complete(ctx, job.ID, next...); err != nil {
				return fmt.Errorf("complete job %d: %w", job.ID, err)
			}
		}
	}
}
