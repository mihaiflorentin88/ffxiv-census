package queue

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

// Queue is a SQLite-backed work queue implementing contract.Queue.
type Queue struct {
	driver contract.SQLiteDriver
	cfg    *config.QueueConfig
	logger contract.Logger
	now    func() time.Time // injectable clock for deterministic backoff tests
}

func NewQueue(driver contract.SQLiteDriver, cfg *config.QueueConfig, logger contract.Logger) (contract.Queue, error) {
	if driver == nil {
		return nil, errors.New("queue driver is nil")
	}
	if cfg == nil {
		return nil, errors.New("queue config is nil")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Queue{driver: driver, cfg: cfg, logger: logger, now: time.Now}, nil
}

func (q *Queue) Publish(ctx context.Context, jobs ...contract.QueueJob) error {
	if len(jobs) > 0 {
		q.logger.DebugContext(ctx, "queue.publish", slog.Int("jobs", len(jobs)))
	}
	now := q.now().UTC().Format(timeLayout)
	for _, j := range jobs {
		if j.MaxAttempts <= 0 {
			j.MaxAttempts = q.cfg.MaxAttempts
		}
		runAt := now
		if !j.RunAt.IsZero() {
			runAt = j.RunAt.UTC().Format(timeLayout)
		}
		h := payloadHash(j.Payload)
		res, err := q.driver.Execute(ctx,
			`INSERT OR IGNORE INTO queue_jobs (type, payload, payload_hash, status, run_at, max_attempts, created_at)
			 VALUES (?, ?, ?, 'pending', ?, ?, ?)`,
			j.Type, string(j.Payload), h, runAt, j.MaxAttempts, now)
		if err != nil {
			return fmt.Errorf("publish %s: %w", j.Type, err)
		}
		inserted, _ := res.RowsAffected()
		q.logger.DebugContext(ctx, "queue.publish_job", slog.String("event_type", j.Type), slog.String("payload_hash", h), slog.Bool("inserted", inserted > 0))
	}
	return nil
}

func (q *Queue) Claim(ctx context.Context, jobType string, n int) ([]contract.QueueJob, error) {
	if n <= 0 {
		n = q.cfg.ClaimBatchSize
	}
	now := q.now().UTC().Format(timeLayout)
	db, err := q.driver.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("claim begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after commit

	rows, err := tx.QueryContext(ctx,
		`UPDATE queue_jobs
		 SET status = 'claimed', claimed_at = ?, attempts = attempts + 1
		 WHERE id IN (
		     SELECT id FROM queue_jobs
		     WHERE type = ? AND status = 'pending' AND run_at <= ?
		     ORDER BY run_at, id
		     LIMIT ?
		 )
		 RETURNING id, type, payload, payload_hash, status, run_at, attempts, max_attempts, claimed_at, created_at`,
		now, jobType, now, n)
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
	q.logger.DebugContext(ctx, "queue.claim", slog.String("event_type", jobType), slog.Int("requested", n), slog.Int("claimed", len(jobs)))
	for _, j := range jobs {
		q.logger.DebugContext(ctx, "queue.claimed", slog.String("event_type", jobType), slog.Int64("job_id", j.ID), slog.Int("attempts", j.Attempts))
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

	if _, err := tx.ExecContext(ctx,
		`UPDATE queue_jobs SET status = 'done' WHERE id = ? AND status = 'claimed'`, id); err != nil {
		return fmt.Errorf("complete: %w", err)
	}
	if err := q.publishTx(ctx, tx, nextJobs...); err != nil {
		return err
	}
	q.logger.InfoContext(ctx, "queue.complete", slog.Int64("job_id", id), slog.Int("chained", len(nextJobs)))
	return tx.Commit()
}

func (q *Queue) Retry(ctx context.Context, id int64) error {
	db, err := q.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	now := q.now().UTC()
	var attempts, maxAttempts int
	if err := db.QueryRowContext(ctx,
		`SELECT attempts, max_attempts FROM queue_jobs WHERE id = ?`, id).Scan(&attempts, &maxAttempts); err != nil {
		return fmt.Errorf("retry read: %w", err)
	}
	backoff := time.Duration(q.cfg.BackoffBaseSeconds) * time.Second
	if attempts >= 2 {
		backoff *= time.Duration(1 << (attempts - 1)) // base * 2^(attempts-1)
	}
	runAt := now.Add(backoff).UTC().Format(timeLayout)
	if attempts >= maxAttempts {
		q.logger.ErrorContext(ctx, "queue.failed", slog.Int64("job_id", id), slog.Int("attempts", attempts), slog.Int("max_attempts", maxAttempts))
		_, err = db.ExecContext(ctx,
			`UPDATE queue_jobs SET status = 'failed' WHERE id = ? AND status = 'claimed'`, id)
	} else {
		q.logger.WarnContext(ctx, "queue.retry", slog.Int64("job_id", id), slog.Int("attempts", attempts), slog.Int("max_attempts", maxAttempts), slog.Duration("backoff", backoff))
		_, err = db.ExecContext(ctx,
			`UPDATE queue_jobs SET status = 'pending', run_at = ?, claimed_at = NULL
			 WHERE id = ? AND status = 'claimed'`, runAt, id)
	}
	if err != nil {
		return fmt.Errorf("retry: %w", err)
	}
	return nil
}

func (q *Queue) Fail(ctx context.Context, id int64) error {
	_, err := q.driver.Execute(ctx,
		`UPDATE queue_jobs SET status = 'failed' WHERE id = ? AND status = 'claimed'`, id)
	if err != nil {
		return fmt.Errorf("fail: %w", err)
	}
	q.logger.ErrorContext(ctx, "queue.fail", slog.Int64("job_id", id))
	return nil
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

// publishTx inserts jobs inside the caller's transaction (atomic chaining).
func (q *Queue) publishTx(ctx context.Context, tx *sql.Tx, jobs ...contract.QueueJob) error {
	now := q.now().UTC().Format(timeLayout)
	for _, j := range jobs {
		if j.MaxAttempts <= 0 {
			j.MaxAttempts = q.cfg.MaxAttempts
		}
		runAt := now
		if !j.RunAt.IsZero() {
			runAt = j.RunAt.UTC().Format(timeLayout)
		}
		h := payloadHash(j.Payload)
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO queue_jobs (type, payload, payload_hash, status, run_at, max_attempts, created_at)
			 VALUES (?, ?, ?, 'pending', ?, ?, ?)`,
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

// scanJobs scans RETURNING rows in column order:
// id, type, payload, payload_hash, status, run_at, attempts, max_attempts,
// claimed_at, created_at. run_at/claimed_at/created_at are TEXT in timeLayout;
// claimed_at may be NULL.
func scanJobs(rows *sql.Rows) ([]contract.QueueJob, error) {
	var jobs []contract.QueueJob
	for rows.Next() {
		var j contract.QueueJob
		var payload, payloadHash, status, runAt, createdAt string
		var claimedAt sql.NullString
		if err := rows.Scan(&j.ID, &j.Type, &payload, &payloadHash, &status, &runAt,
			&j.Attempts, &j.MaxAttempts, &claimedAt, &createdAt); err != nil {
			return nil, err
		}
		j.Payload = []byte(payload)
		j.PayloadHash = payloadHash
		j.Status = contract.QueueJobStatus(status)
		if t, err := time.Parse(timeLayout, runAt); err == nil {
			j.RunAt = t
		}
		if claimedAt.Valid {
			if t, err := time.Parse(timeLayout, claimedAt.String); err == nil {
				j.ClaimedAt = &t
			}
		}
		if t, err := time.Parse(timeLayout, createdAt); err == nil {
			j.CreatedAt = t
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
