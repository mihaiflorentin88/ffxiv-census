# SQLite-backed Work Queue

ffxiv-census runs its durable async work queue in the **same SQLite datastore** as application data. There is no separate queue process: the `queue_jobs` table (applied by goose migration `00002`) holds jobs, and consumers claim them with a single atomic `UPDATE`.

## Lifecycle

```
pending -> claimed -> done (completed_at set)
              \-> pending (retry: attempts++, last_error captured, run_at pushed back with exponential backoff)
              \-> failed (permanently, when attempts >= max_attempts; failed_at and last_error captured)
              \-> pending (via RetryFailed / DLQ replay: attempts reset to 0, run_at set to now)
```

- **Publish** inserts jobs as `pending` and returns `(inserted int, err error)`. Rows whose `(type, payload_hash)` already exist — in *any* status — are ignored (`INSERT OR IGNORE` on the `UNIQUE (type, payload_hash)` constraint), so re-publishing is idempotent. The returned integer indicates how many jobs were newly enqueued vs deduplicated.
- **Claim** atomically marks up to `claim_batch_size` pending jobs of one type as `claimed` and increments `attempts`.
- **Complete** marks a claimed job `done`, records `completed_at`, and publishes downstream jobs in the **same transaction** (atomic chaining).
- **Retry** returns a claimed job to `pending` with `run_at` set to `now + backoff` and records `last_error`, or marks it `failed` once `attempts >= max_attempts` with `failed_at` timestamp.
- **Fail** marks a claimed job `failed` permanently with `last_error` and `failed_at` timestamps.
- **RetryFailed** transitions `failed` dead-letter jobs back to `pending` with `attempts = 0` and `run_at = now`.
- **PurgeJobs** deletes completed or failed jobs older than a specified duration.
## Atomic claim (multi-pod safe)

Claiming is a single `UPDATE ... WHERE id IN (SELECT ... WHERE status = 'pending') ... RETURNING`. SQLite serialises writers with a file-level lock, and the subquery re-evaluates `status = 'pending'` under that lock, so concurrent consumers (multiple processes, multiple goroutines) can never double-deliver the same job. The driver's `busy_timeout` pragma absorbs contention while waiting for the write lock. Claim order is `run_at, id`, so older due jobs win.

## Deduplication

`payload_hash` is a sha256 of the raw payload, computed server-side by the adapter — callers never set it. Combined with `type`, it makes duplicate jobs (e.g. a re-published census request) a no-op regardless of status.

## Backoff

`run_at` after a retry is `now + backoff_base * 2^(attempts-1)` seconds. With the default `backoff_base_seconds = 5`: attempt 1 → 5 s, attempt 2 → 10 s, attempt 3 → 20 s, etc.

## Configuration

```toml
[queue]
claim_batch_size = 4
max_attempts = 5
backoff_base_seconds = 5
```

| Field                | Purpose                                                        |
| -------------------- | -------------------------------------------------------------- |
| `claim_batch_size`   | Default batch for `Claim` when `n <= 0`.                       |
| `max_attempts`       | Default for jobs published without an explicit `MaxAttempts`.  |
| `backoff_base_seconds` | Base duration for the exponential retry backoff.             |

Environment overrides work like `[sqlite]`: dots become underscores and the section name is the prefix — e.g. `QUEUE_MAX_ATTEMPTS=3`, `QUEUE_CLAIM_BATCH_SIZE=10`, `QUEUE_BACKOFF_BASE_SECONDS=30`.

## Contract

`port/contract.Queue` (see `port/contract/queue.go`) is implemented by `infrastructure/queue` (SQLite) and `mock/queue` (in-memory fake for tests). The job type lives in `contract` because Go's internal-package rule would block `port/contract`, `infrastructure/queue`, and `mock/queue` from importing `port/dto/internal`.

## Consumer pattern

```go
jobs, err := queue.Claim(ctx, "character-census", 10) // batch of due jobs
for _, j := range jobs {
    if err := handle(j); err != nil {
        queue.Retry(ctx, j.ID, err.Error()) // or Fail(ctx, j.ID, err.Error()) for permanent errors
        continue
    }
    queue.Complete(ctx, j.ID, downstreamJob(j)) // atomic chaining
}
```

Worker loops are panic-isolated: unexpected panics in handlers are caught, formatted with stack traces, and forwarded to `queue.Retry(ctx, j.ID, panicTrace)` so the worker goroutine does not crash.

`Depth(ctx)` returns the job count per status — useful for dashboards and backpressure signals.

## Operational Inspection & REST APIs

In addition to consumer execution methods (`Claim`, `Complete`, `Retry`, `Fail`, `RetryFailed`, `PurgeJobs`), `contract.Queue` exposes query capabilities for administrative and monitoring interfaces:

- `Depth(ctx)`: aggregates active job counts by status (`pending`, `claimed`, `done`, `failed`).
- `StatsByType(ctx)`: aggregates job status breakdown grouped by event type.
- `GetEventDetails(ctx, sampleLimit)`: returns aggregated counts and sampled active, upcoming queued, and dead-letter failed jobs per event type.
- `ListJobs(ctx, filter, limit, offset)`: returns paginated jobs matching optional `Type` and `Status` filters, ordered newest first (`id DESC`).
- `CountJobs(ctx, filter)`: returns total count of jobs matching the filter.
- `GetJob(ctx, id)`: fetches a single job by its numeric ID with error trace and timestamps.

These capabilities are surfaced over HTTP in the REST API (see `docs/http-api.md` and `swagger.json`):
- `GET /api/v1/queue` — status depth overview.
- `GET /api/v1/queue/events?sample_limit=5` — event types summary, live stats, and active/next/failed job samples.
- `POST /api/v1/queue/retry-failed` — replay failed dead-letter jobs back to pending.
- `POST /api/v1/queue/purge` — purge old completed or failed jobs.
- `GET /api/v1/queue/jobs` — paginated job list with query filters.
- `GET /api/v1/queue/jobs/{id}` — single job detail with payload, error traces, and timestamps.

## CLI Management

CLI commands are available under `ffxiv-census queue`:
- `ffxiv-census queue stats [--event-type TYPE] [--sample-limit N]` — prints ASCII table of queue status and lists sampled active, upcoming, and failed jobs.
- `ffxiv-census queue retry-failed [--event-type TYPE] [--limit N]` — replays dead-letter failed jobs back to pending.
- `ffxiv-census queue purge [--status done|failed] [--older-than 24h]` — purges old done or failed jobs.
## Container wiring

`container.Load.Queue()` lazily builds the adapter on top of the SQLite driver (which self-migrates on first use) and caches it. Like `SQLite()`, it degrades to a logged `nil` if the driver or `[queue]` config is unavailable.
