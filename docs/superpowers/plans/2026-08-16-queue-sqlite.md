# Phase 2: SQLite-backed work queue — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the durable work queue defined in the spec (§3.1, §4, §6): a `queue_jobs` table managed by goose, a `contract.Queue` port, a SQLite-backed adapter with a claim-based lifecycle (`pending → claimed → done | pending (retry, backoff) | failed`), container wiring, and `docs/queue.md`. This unblocks the event handlers (`id-sweep`, `character-census`, `achievement-census`, `fc-census`) in the next phase.

**Architecture:** Hexagonal as per repo docs. `contract.Queue` port implemented by `infrastructure/queue` (new package) on top of the existing `contract.SQLiteDriver`. Job payloads are opaque JSON carried in `internal.QueueJob` (per `docs/data-contracts.md`: background-job transfer objects live in `port/dto/internal`). Dedup via `UNIQUE(type, payload_hash)` with the hash computed server-side (sha256 of payload). Claiming uses `BEGIN IMMEDIATE` + `UPDATE ... RETURNING` so multiple consumer pods are safe. Config: new `[queue]` section (claim batch size, max attempts, backoff base).

**Tech Stack:** Go 1.25, modernc.org/sqlite (bundles SQLite ≥ 3.35 — `RETURNING` supported), pressly/goose/v3, viper. Spec: `docs/superpowers/specs/2026-08-16-lodestone-census-design.md`.

**Verification commands:** `go build ./...`, `go test ./... -race`, `make lint`. Run from repo root.

---

### Task 1: queue_jobs migration

**Files:**
- Create: `infrastructure/sqlite/migration/query/00002_create_queue_jobs.sql`

- [ ] **Step 1: Write the migration**

```sql
-- Queue jobs: durable async work with a claim-based lifecycle.
-- Status flow: pending -> claimed -> done
--                        \-> pending (retry, attempts++, run_at backoff)
--                        \-> failed (after max_attempts)
-- Duplicate (type, payload_hash) rows are ignored at insert time.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE queue_jobs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    type         TEXT    NOT NULL,
    payload      TEXT    NOT NULL DEFAULT '{}',
    payload_hash TEXT    NOT NULL,
    status       TEXT    NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'claimed', 'done', 'failed')),
    run_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    claimed_at   TEXT,
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (type, payload_hash)
);

CREATE INDEX idx_queue_jobs_claim ON queue_jobs (type, status, run_at);
CREATE INDEX idx_queue_jobs_status ON queue_jobs (status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS queue_jobs;
-- +goose StatementEnd
```

Note: timestamps are TEXT in UTC RFC3339 with milliseconds (`strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`) so `run_at <= ?` compares lexicographically. All Go code must write the same format.

- [ ] **Step 2: Verify the migration applies**

```bash
go test ./infrastructure/sqlite/ -run TestDriver_InitAppliesMigrations -v
```

Then a manual smoke check that the table exists (run from repo root; `data/` is gitignored):

```bash
make build && ./bin/ffxiv-census migrate --direction up && \
  sqlite3 data/ffxiv-census.db ".schema queue_jobs"
```

(If the `sqlite3` CLI is absent, skip the `.schema` probe — Task 4's tests assert the schema.)

- [ ] **Step 3: Commit**

```bash
git add infrastructure/sqlite/migration/query/ && git commit -m "feat(sqlite): queue_jobs migration"
```

---

### Task 2: Queue contract, job DTO, and mock

**Files:**
- Create: `port/dto/internal/queue.go`
- Create: `port/contract/queue.go`
- Create: `mock/queue/queue.go`

- [ ] **Step 1: Write the job DTO (`port/dto/internal/queue.go`)**

```go
package internal

import "time"

// QueueJobStatus is the lifecycle state of a queue job.
type QueueJobStatus string

const (
	QueueJobPending QueueJobStatus = "pending"
	QueueJobClaimed QueueJobStatus = "claimed"
	QueueJobDone    QueueJobStatus = "done"
	QueueJobFailed  QueueJobStatus = "failed"
)

// QueueJob is a unit of async work carried between publishers and consumers.
// Payload is opaque JSON; the queue derives PayloadHash from it (sha256) so
// callers never set it on Publish. RunAt is UTC RFC3339 with milliseconds.
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
```

- [ ] **Step 2: Write the contract (`port/contract/queue.go`)**

```go
package contract

import (
	"context"

	"github.com/mihaiflorentin88/ffxiv-census/port/dto/internal"
)

// Queue defines a durable work queue with a claim-based job lifecycle:
// pending -> claimed -> done, or back to pending with exponential backoff
// (retry), or failed once attempts exceed max_attempts.
type Queue interface {
	// Publish inserts jobs as pending. Rows whose (type, payload_hash)
	// already exist (any status) are a no-op.
	Publish(ctx context.Context, jobs ...internal.QueueJob) error
	// Claim atomically claims up to n pending jobs of the given type whose
	// run_at has passed: marks them claimed, increments attempts. Safe for
	// concurrent consumers.
	Claim(ctx context.Context, jobType string, n int) ([]internal.QueueJob, error)
	// Complete marks a claimed job done and publishes nextJobs in the same
	// transaction (downstream chaining is atomic).
	Complete(ctx context.Context, id int64, nextJobs ...internal.QueueJob) error
	// Retry returns a claimed job to pending with backoff (base * 2^(attempts-1)),
	// or marks it failed when attempts >= max_attempts.
	Retry(ctx context.Context, id int64) error
	// Fail marks a claimed job failed permanently.
	Fail(ctx context.Context, id int64) error
	// Depth returns the number of jobs per status.
	Depth(ctx context.Context) (map[internal.QueueJobStatus]int, error)
}
```

- [ ] **Step 3: Write the in-memory fake (`mock/queue/queue.go`)**

Two adapters per port rule (AGENTS.md). `mock/queue` is a mutex-guarded in-memory fake:

```go
package queue

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/mihaiflorentin88/ffxiv-census/port/dto/internal"
)

// Fake is an in-memory Queue for tests. Publish/Claim/Complete/Retry/Fail
// mirror the SQLite adapter's observable semantics.
type Fake struct {
	mu   sync.Mutex
	jobs map[int64]internal.QueueJob
	next int64
}

func NewFake() *Fake {
	return &Fake{jobs: make(map[int64]internal.QueueJob)}
}

func (f *Fake) Publish(ctx context.Context, jobs ...internal.QueueJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishLocked(jobs)
	return nil
}

// publishLocked inserts jobs, deduplicating on (type, payload_hash) to mirror
// the UNIQUE constraint. Caller must hold f.mu — keeps Complete's atomic
// chaining deadlock-free (no re-entrant lock).
func (f *Fake) publishLocked(jobs []internal.QueueJob) {
	for _, j := range jobs {
		dup := false
		for _, existing := range f.jobs {
			if existing.Type == j.Type && existing.PayloadHash == j.PayloadHash {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		f.next++
		j.ID = f.next
		f.jobs[j.ID] = j
	}
}

// Claim claims up to n pending jobs of jobType whose RunAt has passed.
func (f *Fake) Claim(ctx context.Context, jobType string, n int) ([]internal.QueueJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	var out []internal.QueueJob
	for _, j := range f.jobs {
		if j.Type == jobType && j.Status == internal.QueueJobPending && !j.RunAt.After(now) {
			j.Status = internal.QueueJobClaimed
			j.Attempts++
			f.jobs[j.ID] = j
			out = append(out, j)
			if len(out) == n {
				break
			}
		}
	}
	return out, nil
}

// Complete marks the job done and publishes nextJobs under one lock hold
// (mirrors the adapter's single-transaction chaining).
func (f *Fake) Complete(ctx context.Context, id int64, nextJobs ...internal.QueueJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	j.Status = internal.QueueJobDone
	f.jobs[id] = j
	f.publishLocked(nextJobs)
	return nil
}

// Retry re-queues a claimed job as pending (no backoff in the fake); fails it
// once attempts exceed max_attempts.
func (f *Fake) Retry(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	if j.Attempts >= j.MaxAttempts {
		j.Status = internal.QueueJobFailed
	} else {
		j.Status = internal.QueueJobPending
	}
	f.jobs[id] = j
	return nil
}

// Fail marks a job failed.
func (f *Fake) Fail(ctx context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return fmt.Errorf("job %d not found", id)
	}
	j.Status = internal.QueueJobFailed
	f.jobs[id] = j
	return nil
}

// Depth returns job counts per status.
func (f *Fake) Depth(ctx context.Context) (map[internal.QueueJobStatus]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[internal.QueueJobStatus]int{}
	for _, j := range f.jobs {
		out[j.Status]++
	}
	return out, nil
}
```

Adjust the fake as needed while keeping dedup + lifecycle semantics; it must stay small.

- [ ] **Step 4: Verify it compiles**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add port/dto/internal/queue.go port/contract/queue.go mock/queue/ && git commit -m "feat(contract): queue port with job DTO and mock"
```

---

### Task 3: Config — queue section

**Files:**
- Modify: `config/config.go`, `config/config.toml`
- Test: `config/queue_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package config

import "testing"

func TestNewConfig_QueueDefaults(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Queue == nil {
		t.Fatal("expected queue section to be present")
	}
	if cfg.Queue.ClaimBatchSize != 4 {
		t.Errorf("claim_batch_size = %d, want 4", cfg.Queue.ClaimBatchSize)
	}
	if cfg.Queue.MaxAttempts != 5 {
		t.Errorf("max_attempts = %d, want 5", cfg.Queue.MaxAttempts)
	}
	if cfg.Queue.BackoffBaseSeconds != 5 {
		t.Errorf("backoff_base_seconds = %d, want 5", cfg.Queue.BackoffBaseSeconds)
	}
}

func TestQueueConfig_EnvOverride(t *testing.T) {
	t.Setenv("QUEUE_MAX_ATTEMPTS", "9")
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Queue.MaxAttempts != 9 {
		t.Errorf("QUEUE_MAX_ATTEMPTS override: got %d, want 9", cfg.Queue.MaxAttempts)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./config/
```

Expected: FAIL — `cfg.Queue` undefined (compile error).

- [ ] **Step 3: Implement**

In `config/config.go`: add `Queue *QueueConfig \`mapstructure:"queue"\`` to `Config` and:

```go
type QueueConfig struct {
	ClaimBatchSize     int `mapstructure:"claim_batch_size"`
	MaxAttempts        int `mapstructure:"max_attempts"`
	BackoffBaseSeconds int `mapstructure:"backoff_base_seconds"`
}
```

In `config/config.toml`, add:

```toml
[queue]
claim_batch_size = 4
max_attempts = 5
backoff_base_seconds = 5
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./config/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add config/ && git commit -m "feat(config): queue section with claim/backoff defaults"
```

---

### Task 4: SQLite-backed queue adapter

**Files:**
- Create: `infrastructure/queue/queue.go`
- Test: `infrastructure/queue/queue_test.go`

- [ ] **Step 1: Write the failing tests**

Use the production migration FS (`sqlitemigration.FS()`) so the real `queue_jobs` table is created, plus the real driver (`sqlite.NewDriver`) on a temp-file DB — real SQL, no mocks (AGENTS.md).

```go
package queue

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite"
	sqlitemigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/migration"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/mihaiflorentin88/ffxiv-census/port/dto/internal"
)

func testQueue(t *testing.T) contract.Queue {
	t.Helper()
	driver, err := sqlite.NewDriver(&config.SQLiteConfig{
		Path:         filepath.Join(t.TempDir(), "queue.db"),
		MaxOpenConns: 2,
		MaxIdleConns: 2,
		BusyTimeout:  "2s",
		JournalMode:  "WAL",
	}, sqlitemigration.FS())
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	t.Cleanup(func() { driver.Close() })
	q, err := NewQueue(driver, &config.QueueConfig{
		ClaimBatchSize:     4,
		MaxAttempts:        2,
		BackoffBaseSeconds: 5,
	})
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	return q
}

func job(t string, payload string) internal.QueueJob {
	return internal.QueueJob{Type: t, Payload: []byte(payload), MaxAttempts: 2}
}

func TestPublishAndClaimRoundtrip(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	if err := q.Publish(ctx, job("character-census", `{"id":1}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	claimed, err := q.Claim(ctx, "character-census", 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}
	if claimed[0].Type != "character-census" {
		t.Errorf("type = %q", claimed[0].Type)
	}
	if string(claimed[0].Payload) != `{"id":1}` {
		t.Errorf("payload = %q", claimed[0].Payload)
	}
	if claimed[0].Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (incremented on claim)", claimed[0].Attempts)
	}
	if claimed[0].Status != internal.QueueJobClaimed {
		t.Errorf("status = %q, want claimed", claimed[0].Status)
	}
}

func TestPublishDeduplicatesByTypeAndHash(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	if err := q.Publish(ctx, job("id-sweep", `{"chunk":1}`), job("id-sweep", `{"chunk":1}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	depth, err := q.Depth(ctx)
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if depth[internal.QueueJobPending] != 1 {
		t.Errorf("pending = %d, want 1 (duplicate ignored)", depth[internal.QueueJobPending])
	}
	if err := q.Publish(ctx, job("id-sweep", `{"chunk":2}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	depth, _ = q.Depth(ctx)
	if depth[internal.QueueJobPending] != 2 {
		t.Errorf("pending = %d, want 2", depth[internal.QueueJobPending])
	}
}

func TestClaimSkipsFutureAndRespectsLimit(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	future := job("fc-census", `{"fc":"A"}`)
	future.RunAt = time.Now().Add(time.Hour)
	if err := q.Publish(ctx, future, job("fc-census", `{"fc":"B"}`), job("fc-census", `{"fc":"C"}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	claimed, err := q.Claim(ctx, "fc-census", 2)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed = %d, want 2 (limit, future job skipped)", len(claimed))
	}
	for _, c := range claimed {
		if string(c.Payload) == `{"fc":"A"}` {
			t.Error("future-dated job was claimed")
		}
	}
}

func TestCompleteMarksDoneAndPublishesNext(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	if err := q.Publish(ctx, job("character-census", `{"id":7}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	claimed, err := q.Claim(ctx, "character-census", 1)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	next := job("achievement-census", `{"id":7}`)
	if err := q.Complete(ctx, claimed[0].ID, next); err != nil {
		t.Fatalf("complete: %v", err)
	}
	depth, _ := q.Depth(ctx)
	if depth[internal.QueueJobDone] != 1 {
		t.Errorf("done = %d, want 1", depth[internal.QueueJobDone])
	}
	claimedNext, err := q.Claim(ctx, "achievement-census", 1)
	if err != nil {
		t.Fatalf("claim next: %v", err)
	}
	if len(claimedNext) != 1 {
		t.Errorf("downstream job not published by Complete")
	}
}

func TestRetryBackoffThenFail(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	if err := q.Publish(ctx, job("character-census", `{"id":3}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Attempt 1: claim, retry -> pending with backoff.
	c1, _ := q.Claim(ctx, "character-census", 1)
	if err := q.Retry(ctx, c1[0].ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	depth, _ := q.Depth(ctx)
	if depth[internal.QueueJobPending] != 1 {
		t.Fatalf("after retry: pending = %d, want 1", depth[internal.QueueJobPending])
	}
	// Backoff: run_at must be in the future (5s base * 2^0), so claim finds nothing now.
	if claimed, _ := q.Claim(ctx, "character-census", 1); len(claimed) != 0 {
		t.Fatal("job claimed before backoff elapsed")
	}
	// Fast-forward: swap the queue's clock (q.(*Queue).now) to now+backoff, claim again,
	// retry -> attempts(2) >= max(2) -> failed.
	backoff := time.Duration(5) * time.Second
	inner := q.(*Queue)
	inner.now = func() time.Time { return time.Now().Add(backoff) }
	c2, err := q.Claim(ctx, "character-census", 1)
	if err != nil {
		t.Fatalf("claim after backoff: %v", err)
	}
	if len(c2) != 1 {
		t.Fatalf("claimed after backoff = %d, want 1", len(c2))
	}
	if err := q.Retry(ctx, c2[0].ID); err != nil {
		t.Fatalf("retry 2: %v", err)
	}
	depth, _ = q.Depth(ctx)
	if depth[internal.QueueJobFailed] != 1 {
		t.Errorf("failed = %d, want 1 (attempts >= max_attempts)", depth[internal.QueueJobFailed])
	}
}

func TestConcurrentClaimNoDoubleDelivery(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		if err := q.Publish(ctx, job("id-sweep", fmt.Sprintf(`{"chunk":%d}`, i))); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := q.Claim(ctx, "id-sweep", 10)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			mu.Lock()
			total += len(claimed)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 40 {
		t.Errorf("claimed total = %d, want 40 (no double delivery)", total)
	}
}
```

Notes: the clock swap in `TestRetryBackoffThenFail` requires the adapter to expose `now func() time.Time` (injectable clock). `TestConcurrentClaimNoDoubleDelivery` MUST pass under `go test -race`. No sleeps anywhere.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./infrastructure/queue/
```

Expected: FAIL — package does not exist / `NewQueue` undefined.

- [ ] **Step 3: Implement `infrastructure/queue/queue.go`**

```go
package queue

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/mihaiflorentin88/ffxiv-census/port/dto/internal"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

// Queue is a SQLite-backed work queue implementing contract.Queue.
type Queue struct {
	driver contract.SQLiteDriver
	cfg    *config.QueueConfig
	now    func() time.Time // injectable clock for deterministic backoff tests
}

func NewQueue(driver contract.SQLiteDriver, cfg *config.QueueConfig) (contract.Queue, error) {
	if driver == nil {
		return nil, errors.New("queue driver is nil")
	}
	if cfg == nil {
		return nil, errors.New("queue config is nil")
	}
	return &Queue{driver: driver, cfg: cfg, now: time.Now}, nil
}

func (q *Queue) Publish(ctx context.Context, jobs ...internal.QueueJob) error {
	now := q.now().UTC().Format(timeLayout)
	for _, j := range jobs {
		if j.MaxAttempts <= 0 {
			j.MaxAttempts = q.cfg.MaxAttempts
		}
		runAt := now
		if !j.RunAt.IsZero() {
			runAt = j.RunAt.UTC().Format(timeLayout)
		}
		_, err := q.driver.Execute(ctx,
			`INSERT OR IGNORE INTO queue_jobs (type, payload, payload_hash, status, run_at, max_attempts, created_at)
			 VALUES (?, ?, ?, 'pending', ?, ?, ?)`,
			j.Type, string(j.Payload), payloadHash(j.Payload), runAt, j.MaxAttempts, now)
		if err != nil {
			return fmt.Errorf("publish %s: %w", j.Type, err)
		}
	}
	return nil
}

func (q *Queue) Claim(ctx context.Context, jobType string, n int) ([]internal.QueueJob, error) {
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
	defer tx.Rollback() // no-op after commit

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
	return jobs, nil
}

func (q *Queue) Complete(ctx context.Context, id int64, nextJobs ...internal.QueueJob) error {
	db, err := q.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("complete begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE queue_jobs SET status = 'done' WHERE id = ? AND status = 'claimed'`, id); err != nil {
		return fmt.Errorf("complete: %w", err)
	}
	if err := q.publishTx(ctx, tx, nextJobs...); err != nil {
		return err
	}
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
		_, err = db.ExecContext(ctx,
			`UPDATE queue_jobs SET status = 'failed' WHERE id = ? AND status = 'claimed'`, id)
	} else {
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
	return nil
}

func (q *Queue) Depth(ctx context.Context) (map[internal.QueueJobStatus]int, error) {
	rows, err := q.driver.FetchMany(ctx,
		`SELECT status, COUNT(*) FROM queue_jobs GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("depth: %w", err)
	}
	defer rows.Close()
	out := map[internal.QueueJobStatus]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[internal.QueueJobStatus(status)] = n
	}
	return out, rows.Err()
}

// publishTx inserts jobs inside the caller's transaction (atomic chaining).
func (q *Queue) publishTx(ctx context.Context, tx *sql.Tx, jobs ...internal.QueueJob) error {
	now := q.now().UTC().Format(timeLayout)
	for _, j := range jobs {
		if j.MaxAttempts <= 0 {
			j.MaxAttempts = q.cfg.MaxAttempts
		}
		runAt := now
		if !j.RunAt.IsZero() {
			runAt = j.RunAt.UTC().Format(timeLayout)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO queue_jobs (type, payload, payload_hash, status, run_at, max_attempts, created_at)
			 VALUES (?, ?, ?, 'pending', ?, ?, ?)`,
			j.Type, string(j.Payload), payloadHash(j.Payload), runAt, j.MaxAttempts, now); err != nil {
			return fmt.Errorf("publish next: %w", err)
		}
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
func scanJobs(rows *sql.Rows) ([]internal.QueueJob, error) {
	var jobs []internal.QueueJob
	for rows.Next() {
		var j internal.QueueJob
		var payload, payloadHash, status, runAt, createdAt string
		var claimedAt sql.NullString
		if err := rows.Scan(&j.ID, &j.Type, &payload, &payloadHash, &status, &runAt,
			&j.Attempts, &j.MaxAttempts, &claimedAt, &createdAt); err != nil {
			return nil, err
		}
		j.Payload = []byte(payload)
		j.PayloadHash = payloadHash
		j.Status = internal.QueueJobStatus(status)
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
```

Notes for the implementer:

- `RETURNING` requires SQLite ≥ 3.35; modernc.org/sqlite bundles a recent version (v1.56.0 per go.mod). Verify during implementation with `go test ./infrastructure/queue/`. If the installed modernc version lacks `RETURNING`, fall back to a two-step claim (SELECT ids → UPDATE … WHERE id IN (…)) inside the same `BeginTx` — still multi-pod safe.
- `driver.Acquire` returns the shared pool `*sql.DB`; begin transactions on that. Do not start a transaction inside the driver wrapper.
- The injectable `now` clock makes backoff tests deterministic (no sleeps).
- Every job write must use `timeLayout` (UTC, ms precision) so `run_at <= ?` compares correctly.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./infrastructure/queue/ -v
go test ./infrastructure/queue/ -race
```

Expected: PASS (including the concurrent claim test under `-race`).

- [ ] **Step 5: Commit**

```bash
git add infrastructure/queue/ && git commit -m "feat(queue): sqlite-backed claim-based queue"
```

---

### Task 5: Container wiring

**Files:**
- Modify: `container/infrastructure.go`
- Test: `container/queue_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package container

import (
	"path/filepath"
	"testing"
)

func TestServiceContainer_Queue(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "queue.db"))
	Load = NewServiceContainer()

	q := Load.Queue()
	if q == nil {
		t.Fatal("expected non-nil queue")
	}
}

func TestServiceContainer_QueueCached(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "queue.db"))
	Load = NewServiceContainer()

	first := Load.Queue()
	second := Load.Queue()
	if first != second {
		t.Fatal("expected cached queue instance")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./container/
```

Expected: FAIL — `Load.Queue` undefined.

- [ ] **Step 3: Implement**

In `container/infrastructure.go`: add import `"github.com/mihaiflorentin88/ffxiv-census/infrastructure/queue"`, field `queue contract.Queue`, and accessor:

```go
func (s *ServiceContainer) Queue() contract.Queue {
	if s.infrastructure.queue != nil {
		return s.infrastructure.queue
	}
	driver := s.SQLite()
	if driver == nil {
		logging.Warn("container.queue", "sqlite driver unavailable, queue disabled")
		return nil
	}
	cfg := s.Config().Queue
	if cfg == nil {
		logging.Warn("container.queue", "queue config missing")
		return nil
	}
	q, err := queue.NewQueue(driver, cfg)
	if err != nil {
		logging.Error("container.queue", fmt.Sprintf("failed to create queue: %v", err))
		return nil
	}
	s.infrastructure.queue = q
	return q
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./container/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add container/ && git commit -m "feat(container): queue accessor"
```

---

### Task 6: Documentation

**Files:**
- Create: `docs/queue.md`
- Modify: `docs/architecture.md`, `docs/container.md`

- [ ] **Step 1: Write `docs/queue.md`**

Content must explain (spec §11): SQLite queue design — same datastore, `queue_jobs` table; job lifecycle `pending → claimed → done | pending (retry) | failed`; claim semantics (atomic `UPDATE ... RETURNING`, multi-pod safe); `UNIQUE(type, payload_hash)` dedup (sha256 of payload, computed server-side); retry backoff `base * 2^(attempts-1)`; `max_attempts` (config, default 5); `Depth` per status; atomic `Complete` with downstream publishes; config `[queue]` (claim_batch_size, max_attempts, backoff_base_seconds) + env overrides (`QUEUE_*`); usage pattern for consumers (claim → handle → complete/retry/fail).

- [ ] **Step 2: Update existing docs**

- `docs/architecture.md`: replace the "Future Hooks" line "Add a queue backed by SQLite … in later phases" with a "Queue" note pointing at `docs/queue.md`; mention the `Queue()` accessor in the container layer description.
- `docs/container.md`: add `Queue()` to the accessor list (Usage Tips + Structure section).

- [ ] **Step 3: Commit**

```bash
git add docs/ && git commit -m "docs: sqlite queue design and container wiring"
```

---

### Task 7: Final verification

- [ ] **Step 1: Full test suite with race detector**

```bash
go test ./... -race
```

Expected: all PASS.

- [ ] **Step 2: Lint**

```bash
make lint
```

Expected: no issues (fix any reported inline).

- [ ] **Step 3: Build + smoke test**

```bash
make build && ./bin/ffxiv-census migrate --direction up
```

Expected: exits 0; `data/ffxiv-census.db` contains `queue_jobs` (verified in Task 1).

- [ ] **Step 4: Commit any fixes**

```bash
git add -A && git commit -m "chore: queue phase verification"
```
