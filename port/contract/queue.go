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
	LastError   *string
	ClaimedAt   *time.Time
	CreatedAt   time.Time
	FailedAt    *time.Time
	CompletedAt *time.Time
}

// QueueJobFilter defines optional criteria for querying queue jobs.
type QueueJobFilter struct {
	Type   string
	Status QueueJobStatus
}

// QueueTypeStats holds aggregated job counts by status for a specific event type.
type QueueTypeStats struct {
	Type    string
	Pending int
	Claimed int
	Done    int
	Failed  int
	Total   int
}

// QueueEventDetail holds aggregated job counts along with sampled active, next, and failed jobs.
type QueueEventDetail struct {
	Type       string
	Pending    int
	Claimed    int
	Done       int
	Failed     int
	Total      int
	ActiveJobs []QueueJob
	NextJobs   []QueueJob
	FailedJobs []QueueJob
}

// Queue defines a durable work queue with a claim-based job lifecycle:
// pending -> claimed -> done, or back to pending with exponential backoff
// (retry), or failed once attempts exceed max_attempts.
type Queue interface {
	// Publish inserts jobs as pending. Rows whose (type, payload_hash)
	// already exist (any status) are a no-op. Returns the count of newly inserted jobs.
	Publish(ctx context.Context, jobs ...QueueJob) (int, error)
	// Claim atomically claims up to n pending jobs of the given type whose
	// run_at has passed: marks them claimed, increments attempts. Safe for
	// concurrent consumers.
	Claim(ctx context.Context, jobType string, n int) ([]QueueJob, error)
	// Complete marks a claimed job done and publishes nextJobs in the same
	// transaction (downstream chaining is atomic).
	Complete(ctx context.Context, id int64, nextJobs ...QueueJob) error
	// Retry returns a claimed job to pending with backoff (base * 2^(attempts-1)),
	// or marks it failed when attempts >= max_attempts. Records lastErr.
	Retry(ctx context.Context, id int64, lastErr string) error
	// Fail marks a claimed job failed permanently. Records lastErr.
	Fail(ctx context.Context, id int64, lastErr string) error
	// RetryFailed transitions failed jobs back to pending with run_at = now.
	// If jobType is non-empty, only jobs of that type are retried. Returns number of jobs retried.
	RetryFailed(ctx context.Context, jobType string, limit int) (int, error)
	// PurgeJobs deletes jobs matching status that were completed/failed/created older than olderThan duration.
	PurgeJobs(ctx context.Context, status QueueJobStatus, olderThan time.Duration) (int64, error)
	// GetEventDetails returns aggregated queue status counts and sampled active, upcoming, and failed jobs.
	GetEventDetails(ctx context.Context, sampleLimit int) ([]QueueEventDetail, error)
	// Depth returns the number of jobs per status.
	Depth(ctx context.Context) (map[QueueJobStatus]int, error)
	// ReclaimClaimed returns to 'pending' every job of jobType stuck in
	// 'claimed' status (a previous consumer was killed mid-flight). It clears
	// claimed_at and resets run_at to now so the job is immediately claimable.
	// Returns the number of jobs reclaimed.
	ReclaimClaimed(ctx context.Context, jobType string) (int, error)
	// ListJobs returns non-deleted queue jobs matching filter, ordered by id desc (newest first).
	ListJobs(ctx context.Context, filter QueueJobFilter, limit, offset int) ([]QueueJob, error)
	// CountJobs returns the number of queue jobs matching filter.
	CountJobs(ctx context.Context, filter QueueJobFilter) (int64, error)
	// GetJob returns one queue job by id, or nil if not found.
	GetJob(ctx context.Context, id int64) (*QueueJob, error)
	// StatsByType returns aggregated job counts (pending, claimed, done, failed, total) grouped by event type.
	StatsByType(ctx context.Context) ([]QueueTypeStats, error)
}
