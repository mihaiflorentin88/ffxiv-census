package contract

import (
	"context"
)

// QueueJob is a unit of async work carried between publishers and consumers.
// Payload is opaque JSON. Type is the event type used for routing.
type QueueJob struct {
	Type    string
	Payload []byte
}

// Queue defines a durable work queue with push-based consumption.
// The adapter handles retry with exponential backoff and dead-letter routing
// internally — callers only see Publish and Consume.
type Queue interface {
	// Publish sends a single job to the queue. The job's Type is used as the
	// routing key. Returns error on failure.
	Publish(ctx context.Context, job QueueJob) error
	// Consume starts concurrency consumers for the given event types. Each
	// message is dispatched to handler. On handler return:
	//   - nil  → message is acked
	//   - error → message is retried with exponential backoff; after maxAttempts
	//             it is sent to the dead-letter queue
	// Consume blocks until ctx is cancelled.
	Consume(ctx context.Context, eventTypes []string, concurrency int, handler func(ctx context.Context, job QueueJob) error) error
	// ConsumeFailed consumes from per-event-type failed queues and
	// re-publishes messages back to the main queues. Messages that have
	// exceeded maxFailedAttempts are permanently discarded.
	// If eventTypes is empty, consumes from all failed queues.
	ConsumeFailed(ctx context.Context, eventTypes []string, concurrency int) error
	// Close closes the underlying connection.
	Close() error
}
