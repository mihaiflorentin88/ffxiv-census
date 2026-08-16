package contract

import (
	"context"
	"time"
)

// QueueJobStatus is the lifecycle state of a queue job.
type QueueJobStatus string

const (
	QueueJobPending QueueJobStatus = "pending"
	QueueJobClaimed QueueJobStatus = "claimed"
	QueueJobDone    QueueJobStatus = "done"
	QueueJobFailed  QueueJobStatus = "failed"
)

// QueueJob is a unit of async work carried between publishers and consumers.
// Payload is opaque JSON; the adapter derives PayloadHash from it (sha256), so
// callers never set it on Publish. RunAt is UTC with millisecond precision.
type QueueJob struct {
	ID          int64
	Type        string
	Payload     []byte
	PayloadHash string
	Status      QueueJobStatus
	RunAt       time.Time
	Attempts    int
	MaxAttempts int
	ClaimedAt   *time.Time
	CreatedAt   time.Time
}

// Queue defines a durable work queue with a claim-based job lifecycle:
// pending -> claimed -> done, or back to pending with exponential backoff
// (retry), or failed once attempts exceed max_attempts.
type Queue interface {
	// Publish inserts jobs as pending. Rows whose (type, payload_hash)
	// already exist (any status) are a no-op.
	Publish(ctx context.Context, jobs ...QueueJob) error
	// Claim atomically claims up to n pending jobs of the given type whose
	// run_at has passed: marks them claimed, increments attempts. Safe for
	// concurrent consumers.
	Claim(ctx context.Context, jobType string, n int) ([]QueueJob, error)
	// Complete marks a claimed job done and publishes nextJobs in the same
	// transaction (downstream chaining is atomic).
	Complete(ctx context.Context, id int64, nextJobs ...QueueJob) error
	// Retry returns a claimed job to pending with backoff (base * 2^(attempts-1)),
	// or marks it failed when attempts >= max_attempts.
	Retry(ctx context.Context, id int64) error
	// Fail marks a claimed job failed permanently.
	Fail(ctx context.Context, id int64) error
	// Depth returns the number of jobs per status.
	Depth(ctx context.Context) (map[QueueJobStatus]int, error)
}
