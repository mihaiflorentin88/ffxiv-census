package queue

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Queue is a PostgreSQL-backed work queue implementing contract.Queue.
type Queue struct {
	driver contract.DatabaseDriver
	cfg    *config.QueueConfig
	logger contract.Logger
	now    func() time.Time
}

func NewQueue(driver contract.DatabaseDriver, cfg *config.QueueConfig, logger contract.Logger) (contract.Queue, error) {
	if driver == nil {
		return nil, fmt.Errorf("queue: database driver is required")
	}
	if cfg == nil {
		cfg = &config.QueueConfig{
			ClaimBatchSize:     4,
			MaxAttempts:        5,
			BackoffBaseSeconds: 5,
		}
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Queue{
		driver: driver,
		cfg:    cfg,
		logger: logger,
		now:    time.Now,
	}, nil
}

// SetNowFunc overrides the queue's internal clock (useful in tests).
func (q *Queue) SetNowFunc(f func() time.Time) {
	q.now = f
}

func (q *Queue) Publish(ctx context.Context, jobs ...contract.QueueJob) (int, error) {
	if len(jobs) > 0 {
		q.logger.DebugContext(ctx, "queue.publish", slog.Int("jobs", len(jobs)))
	}
	now := q.now().UTC()
	var totalInserted int
	for _, j := range jobs {
		if j.MaxAttempts == 0 {
			j.MaxAttempts = q.cfg.MaxAttempts
		} else if j.MaxAttempts < 0 {
			j.MaxAttempts = 0
		}
		runAt := now
		if !j.RunAt.IsZero() {
			runAt = j.RunAt.UTC()
		}
		h := payloadHash(j.Payload)
		res, err := q.driver.Execute(ctx,
			`INSERT INTO queue_jobs (type, payload, payload_hash, status, run_at, max_attempts, created_at)
			 VALUES ($1, $2, $3, 'pending', $4, $5, $6)
			 ON CONFLICT (type, payload_hash) DO NOTHING`,
			j.Type, string(j.Payload), h, runAt, j.MaxAttempts, now)
		if err != nil {
			return totalInserted, fmt.Errorf("publish %s: %w", j.Type, err)
		}
		inserted, _ := res.RowsAffected()
		if inserted > 0 {
			totalInserted += int(inserted)
		}
		q.logger.DebugContext(ctx, "queue.publish_job", slog.String("event_type", j.Type), slog.String("payload_hash", h), slog.Bool("inserted", inserted > 0))
	}
	return totalInserted, nil
}

func (q *Queue) Claim(ctx context.Context, jobType string, n int, mode contract.ClaimMode) ([]contract.QueueJob, error) {
	return q.ClaimMultiple(ctx, []string{jobType}, n, mode)
}

func (q *Queue) ClaimMultiple(ctx context.Context, jobTypes []string, n int, mode contract.ClaimMode) ([]contract.QueueJob, error) {
	if len(jobTypes) == 0 {
		return nil, nil
	}
	if n <= 0 {
		n = q.cfg.ClaimBatchSize
	}
	now := q.now().UTC()
	db, err := q.driver.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("claim begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	placeholders := make([]string, len(jobTypes))
	args := make([]any, 0, len(jobTypes)+3)
	args = append(args, now) // $1 = claimed_at
	for i, jt := range jobTypes {
		args = append(args, jt)
		placeholders[i] = fmt.Sprintf("$%d", len(args))
	}
	args = append(args, now) // run_at <= $N
	runAtPos := len(args)
	args = append(args, n) // LIMIT $M
	limitPos := len(args)

	var modeFilter string
	switch mode {
	case contract.ClaimModeNewOnly:
		modeFilter = " AND attempts = 0"
	case contract.ClaimModeRetriesOnly:
		modeFilter = " AND attempts > 0"
	}

	query := fmt.Sprintf(`UPDATE queue_jobs
		 SET status = 'claimed', claimed_at = $1, attempts = attempts + 1
		 WHERE id IN (
		     SELECT id FROM queue_jobs
		     WHERE type IN (%s) AND status = 'pending' AND run_at <= $%d %s
		     ORDER BY run_at, id
		     LIMIT $%d
		 )
		 RETURNING id, type, payload, payload_hash, status, run_at, attempts, max_attempts, last_error, claimed_at, created_at, failed_at, completed_at`,
		strings.Join(placeholders, ", "), runAtPos, modeFilter, limitPos)

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("claim: %w", err)
	}
	jobs, err := scanJobs(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("claim commit: %w", err)
	}
	q.logger.DebugContext(ctx, "queue.claim", slog.Any("event_types", jobTypes), slog.Int("requested", n), slog.Int("claimed", len(jobs)))
	for _, j := range jobs {
		q.logger.DebugContext(ctx, "queue.claimed", slog.String("event_type", j.Type), slog.Int64("job_id", j.ID), slog.Int("attempts", j.Attempts))
	}
	return jobs, nil
}

func (q *Queue) Complete(ctx context.Context, id int64, nextJobs ...contract.QueueJob) error {
	db, err := q.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("complete begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := q.now().UTC()
	if _, err := tx.ExecContext(ctx,
		`UPDATE queue_jobs SET status = 'done', completed_at = $1 WHERE id = $2 AND status = 'claimed'`, now, id); err != nil {
		return fmt.Errorf("complete: %w", err)
	}
	if err := q.publishTx(ctx, tx, nextJobs...); err != nil {
		return err
	}
	q.logger.InfoContext(ctx, "queue.complete", slog.Int64("job_id", id), slog.Int("chained", len(nextJobs)))
	return tx.Commit()
}

func (q *Queue) Retry(ctx context.Context, id int64, lastErr string) error {
	db, err := q.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	now := q.now().UTC()
	var attempts, maxAttempts int
	if err := db.QueryRowContext(ctx,
		`SELECT attempts, max_attempts FROM queue_jobs WHERE id = $1`, id).Scan(&attempts, &maxAttempts); err != nil {
		return fmt.Errorf("retry read: %w", err)
	}

	baseSeconds := q.cfg.BackoffBaseSeconds
	if baseSeconds <= 0 {
		baseSeconds = 5
	}
	backoff := time.Duration(baseSeconds) * time.Second
	if attempts >= 2 {
		shift := uint(attempts - 1)
		if shift > 10 {
			shift = 10
		}
		backoff *= time.Duration(1 << shift)
	}
	jitterFactor := 0.9 + 0.3*rand.Float64()
	backoff = time.Duration(float64(backoff) * jitterFactor)
	runAt := now.Add(backoff).UTC()

	if maxAttempts > 0 && attempts >= maxAttempts {
		q.logger.ErrorContext(ctx, "queue.failed", slog.Int64("job_id", id), slog.Int("attempts", attempts), slog.Int("max_attempts", maxAttempts), slog.String("last_error", lastErr))
		_, err = db.ExecContext(ctx,
			`UPDATE queue_jobs SET status = 'failed', last_error = $1, failed_at = $2 WHERE id = $3 AND status = 'claimed'`, lastErr, now, id)
	} else {
		q.logger.WarnContext(ctx, "queue.retry", slog.Int64("job_id", id), slog.Int("attempts", attempts), slog.Int("max_attempts", maxAttempts), slog.Duration("backoff", backoff), slog.String("last_error", lastErr))
		_, err = db.ExecContext(ctx,
			`UPDATE queue_jobs SET status = 'pending', run_at = $1, claimed_at = NULL, last_error = $2
			 WHERE id = $3 AND status = 'claimed'`, runAt, lastErr, id)
	}
	if err != nil {
		return fmt.Errorf("retry: %w", err)
	}
	return nil
}

func (q *Queue) Fail(ctx context.Context, id int64, lastErr string) error {
	now := q.now().UTC()
	_, err := q.driver.Execute(ctx,
		`UPDATE queue_jobs SET status = 'failed', last_error = $1, failed_at = $2 WHERE id = $3 AND status = 'claimed'`, lastErr, now, id)
	if err != nil {
		return fmt.Errorf("fail: %w", err)
	}
	q.logger.ErrorContext(ctx, "queue.fail", slog.Int64("job_id", id), slog.String("last_error", lastErr))
	return nil
}

func (q *Queue) RetryFailed(ctx context.Context, jobType string, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	now := q.now().UTC()
	var query string
	var args []any
	if jobType != "" {
		query = `UPDATE queue_jobs
		         SET status = 'pending', run_at = $1, failed_at = NULL, attempts = 0
		         WHERE id IN (
		             SELECT id FROM queue_jobs
		             WHERE status = 'failed' AND type = $2
		             ORDER BY id ASC
		             LIMIT $3
		         )`
		args = []any{now, jobType, limit}
	} else {
		query = `UPDATE queue_jobs
		         SET status = 'pending', run_at = $1, failed_at = NULL, attempts = 0
		         WHERE id IN (
		             SELECT id FROM queue_jobs
		             WHERE status = 'failed'
		             ORDER BY id ASC
		             LIMIT $2
		         )`
		args = []any{now, limit}
	}
	res, err := q.driver.Execute(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("retry failed jobs: %w", err)
	}
	n, _ := res.RowsAffected()
	q.logger.InfoContext(ctx, "queue.retry_failed", slog.String("event_type", jobType), slog.Int("retried", int(n)))
	return int(n), nil
}

func (q *Queue) PurgeJobs(ctx context.Context, eventType string, status contract.QueueJobStatus, olderThan time.Duration) (int64, error) {
	cutoff := q.now().UTC().Add(-olderThan)
	var where []string
	var args []any

	addParam := func(clauseTpl string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clauseTpl, len(args)))
	}

	if eventType != "" && eventType != "all" {
		addParam("type = $%d", eventType)
	}
	if status != "" && status != "all" {
		addParam("status = $%d", string(status))
	}

	cutoffParam := len(args) + 1
	args = append(args, cutoff)

	where = append(where, fmt.Sprintf(`(
		(status = 'done' AND COALESCE(completed_at, created_at) <= $%d)
		OR (status = 'failed' AND COALESCE(failed_at, created_at) <= $%d)
		OR (status NOT IN ('done', 'failed') AND created_at <= $%d)
	)`, cutoffParam, cutoffParam, cutoffParam))

	query := "DELETE FROM queue_jobs WHERE " + strings.Join(where, " AND ")
	res, err := q.driver.Execute(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("purge jobs: %w", err)
	}
	n, _ := res.RowsAffected()
	q.logger.InfoContext(ctx, "queue.purge", slog.String("event_type", eventType), slog.String("status", string(status)), slog.Duration("older_than", olderThan), slog.Int64("purged", n))
	return n, nil
}

func (q *Queue) GetEventDetails(ctx context.Context, sampleLimit int) ([]contract.QueueEventDetail, error) {
	if sampleLimit <= 0 {
		sampleLimit = 5
	}
	stats, err := q.StatsByType(ctx)
	if err != nil {
		return nil, err
	}
	details := make([]contract.QueueEventDetail, 0, len(stats))
	for _, s := range stats {
		d := contract.QueueEventDetail{
			Type:       s.Type,
			Pending:    s.Pending,
			Claimed:    s.Claimed,
			Done:       s.Done,
			Failed:     s.Failed,
			Total:      s.Total,
			ActiveJobs: []contract.QueueJob{},
			NextJobs:   []contract.QueueJob{},
			FailedJobs: []contract.QueueJob{},
		}
		if s.Claimed > 0 {
			rows, err := q.driver.FetchMany(ctx,
				`SELECT id, type, payload, payload_hash, status, run_at, attempts, max_attempts, last_error, claimed_at, created_at, failed_at, completed_at
				 FROM queue_jobs
				 WHERE type = $1 AND status = 'claimed'
				 ORDER BY claimed_at DESC, id ASC
				 LIMIT $2`, s.Type, sampleLimit)
			if err == nil {
				jobs, scanErr := scanJobs(rows)
				rows.Close()
				if scanErr == nil && len(jobs) > 0 {
					d.ActiveJobs = jobs
				}
			}
		}
		if s.Pending > 0 {
			rows, err := q.driver.FetchMany(ctx,
				`SELECT id, type, payload, payload_hash, status, run_at, attempts, max_attempts, last_error, claimed_at, created_at, failed_at, completed_at
				 FROM queue_jobs
				 WHERE type = $1 AND status = 'pending'
				 ORDER BY run_at ASC, id ASC
				 LIMIT $2`, s.Type, sampleLimit)
			if err == nil {
				jobs, scanErr := scanJobs(rows)
				rows.Close()
				if scanErr == nil && len(jobs) > 0 {
					d.NextJobs = jobs
				}
			}
		}
		if s.Failed > 0 {
			rows, err := q.driver.FetchMany(ctx,
				`SELECT id, type, payload, payload_hash, status, run_at, attempts, max_attempts, last_error, claimed_at, created_at, failed_at, completed_at
				 FROM queue_jobs
				 WHERE type = $1 AND status = 'failed'
				 ORDER BY COALESCE(failed_at, created_at) DESC, id DESC
				 LIMIT $2`, s.Type, sampleLimit)
			if err == nil {
				jobs, scanErr := scanJobs(rows)
				rows.Close()
				if scanErr == nil && len(jobs) > 0 {
					d.FailedJobs = jobs
				}
			}
		}
		details = append(details, d)
	}
	return details, nil
}

func (q *Queue) Depth(ctx context.Context) (map[contract.QueueJobStatus]int, error) {
	rows, err := q.driver.FetchMany(ctx,
		`SELECT status, COUNT(*) FROM queue_jobs GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("depth: %w", err)
	}
	defer rows.Close()
	out := map[contract.QueueJobStatus]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[contract.QueueJobStatus(status)] = n
	}
	return out, rows.Err()
}

func (q *Queue) ReclaimClaimed(ctx context.Context, jobType string) (int, error) {
	res, err := q.driver.Execute(ctx,
		`UPDATE queue_jobs SET status = 'pending', run_at = $1, claimed_at = NULL
		  WHERE type = $2 AND status = 'claimed'`,
		q.now().UTC(), jobType)
	if err != nil {
		return 0, fmt.Errorf("reclaim claimed: %w", err)
	}
	n, _ := res.RowsAffected()
	q.logger.InfoContext(ctx, "queue.reclaim", slog.String("event_type", jobType), slog.Int("reclaimed", int(n)))
	return int(n), nil
}

func (q *Queue) ListJobs(ctx context.Context, filter contract.QueueJobFilter, limit, offset int) ([]contract.QueueJob, error) {
	var where []string
	var args []any

	addParam := func(clauseTpl string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clauseTpl, len(args)))
	}

	if filter.Type != "" {
		addParam("type = $%d", filter.Type)
	}
	if filter.Status != "" {
		addParam("status = $%d", string(filter.Status))
	}
	query := "SELECT id, type, payload, payload_hash, status, run_at, attempts, max_attempts, last_error, claimed_at, created_at, failed_at, completed_at FROM queue_jobs"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id DESC"
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
		if offset > 0 {
			args = append(args, offset)
			query += fmt.Sprintf(" OFFSET $%d", len(args))
		}
	} else if offset > 0 {
		args = append(args, offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}

	rows, err := q.driver.FetchMany(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list queue jobs: %w", err)
	}
	defer rows.Close()

	jobs, err := scanJobs(rows)
	if err != nil {
		return nil, fmt.Errorf("list queue jobs: %w", err)
	}
	if jobs == nil {
		jobs = []contract.QueueJob{}
	}
	return jobs, nil
}

func (q *Queue) CountJobs(ctx context.Context, filter contract.QueueJobFilter) (int64, error) {
	var where []string
	var args []any

	addParam := func(clauseTpl string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clauseTpl, len(args)))
	}

	if filter.Type != "" {
		addParam("type = $%d", filter.Type)
	}
	if filter.Status != "" {
		addParam("status = $%d", string(filter.Status))
	}
	query := "SELECT COUNT(*) FROM queue_jobs"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	var count int64
	row, err := q.driver.FetchOne(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("count queue jobs: %w", err)
	}
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count queue jobs: %w", err)
	}
	return count, nil
}

func (q *Queue) GetJob(ctx context.Context, id int64) (*contract.QueueJob, error) {
	query := "SELECT id, type, payload, payload_hash, status, run_at, attempts, max_attempts, last_error, claimed_at, created_at, failed_at, completed_at FROM queue_jobs WHERE id = $1"
	rows, err := q.driver.FetchMany(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("get queue job %d: %w", id, err)
	}
	defer rows.Close()

	jobs, err := scanJobs(rows)
	if err != nil {
		return nil, fmt.Errorf("get queue job %d: %w", id, err)
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	return &jobs[0], nil
}

func (q *Queue) StatsByType(ctx context.Context) ([]contract.QueueTypeStats, error) {
	query := `
SELECT type,
       COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0) AS pending_count,
       COALESCE(SUM(CASE WHEN status = 'claimed' THEN 1 ELSE 0 END), 0) AS claimed_count,
       COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0) AS done_count,
       COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) AS failed_count,
       COUNT(*) AS total_count
FROM queue_jobs
GROUP BY type
ORDER BY type ASC
`
	rows, err := q.driver.FetchMany(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("queue stats by type: %w", err)
	}
	defer rows.Close()

	var stats []contract.QueueTypeStats
	for rows.Next() {
		var s contract.QueueTypeStats
		if err := rows.Scan(&s.Type, &s.Pending, &s.Claimed, &s.Done, &s.Failed, &s.Total); err != nil {
			return nil, fmt.Errorf("scan queue stats: %w", err)
		}
		stats = append(stats, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if stats == nil {
		stats = []contract.QueueTypeStats{}
	}
	return stats, nil
}

func (q *Queue) publishTx(ctx context.Context, tx *sql.Tx, jobs ...contract.QueueJob) error {
	now := q.now().UTC()
	for _, j := range jobs {
		if j.MaxAttempts == 0 {
			j.MaxAttempts = q.cfg.MaxAttempts
		} else if j.MaxAttempts < 0 {
			j.MaxAttempts = 0
		}
		runAt := now
		if !j.RunAt.IsZero() {
			runAt = j.RunAt.UTC()
		}
		h := payloadHash(j.Payload)
		res, err := tx.ExecContext(ctx,
			`INSERT INTO queue_jobs (type, payload, payload_hash, status, run_at, max_attempts, created_at)
			 VALUES ($1, $2, $3, 'pending', $4, $5, $6)
			 ON CONFLICT (type, payload_hash) DO NOTHING`,
			j.Type, string(j.Payload), h, runAt, j.MaxAttempts, now)
		if err != nil {
			return fmt.Errorf("publish next: %w", err)
		}
		inserted, _ := res.RowsAffected()
		q.logger.DebugContext(ctx, "queue.publish_job", slog.String("event_type", j.Type), slog.String("payload_hash", h), slog.Bool("inserted", inserted > 0))
	}
	return nil
}

func payloadHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func scanJobs(rows *sql.Rows) ([]contract.QueueJob, error) {
	var jobs []contract.QueueJob
	for rows.Next() {
		var j contract.QueueJob
		var payload, payloadHash, status string
		var runAt, createdAt time.Time
		var attempts, maxAttempts int
		var lastError sql.NullString
		var claimedAt, failedAt, completedAt sql.NullTime
		if err := rows.Scan(&j.ID, &j.Type, &payload, &payloadHash, &status, &runAt,
			&attempts, &maxAttempts, &lastError, &claimedAt, &createdAt, &failedAt, &completedAt); err != nil {
			return nil, err
		}
		j.Payload = []byte(payload)
		j.PayloadHash = payloadHash
		j.Status = contract.QueueJobStatus(status)
		j.RunAt = runAt
		j.Attempts = attempts
		j.MaxAttempts = maxAttempts
		if lastError.Valid && lastError.String != "" {
			val := lastError.String
			j.LastError = &val
		}
		if claimedAt.Valid {
			t := claimedAt.Time
			j.ClaimedAt = &t
		}
		j.CreatedAt = createdAt
		if failedAt.Valid {
			t := failedAt.Time
			j.FailedAt = &t
		}
		if completedAt.Valid {
			t := completedAt.Time
			j.CompletedAt = &t
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
