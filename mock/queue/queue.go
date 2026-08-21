package mockqueue

import (
	"context"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Fake is an in-memory queue implementing contract.Queue for tests.
// Published jobs are buffered and delivered to Consume handlers synchronously.
type Fake struct {
	mu   sync.Mutex
	jobs []contract.QueueJob
}

// NewFake creates a new Fake queue.
func NewFake() *Fake {
	return &Fake{}
}

// Publish appends a job to the in-memory buffer.
func (f *Fake) Publish(_ context.Context, job contract.QueueJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs = append(f.jobs, job)
	return nil
}

// Consume drains buffered jobs and calls handler for each one. It blocks
// until ctx is cancelled. Jobs published after Consume starts are also
// delivered.
func (f *Fake) Consume(ctx context.Context, _ []string, _ int, handler func(ctx context.Context, job contract.QueueJob) error) error {
	for {
		f.mu.Lock()
		if len(f.jobs) > 0 {
			job := f.jobs[0]
			f.jobs = f.jobs[1:]
			f.mu.Unlock()
			_ = handler(ctx, job)
			continue
		}
		f.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

// Close is a no-op.
func (f *Fake) Close() error { return nil }

// ConsumeFailed is a no-op for the mock queue.
func (f *Fake) ConsumeFailed(_ context.Context, _ []string, _ int) error { return nil }
