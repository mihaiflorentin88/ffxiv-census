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

## Jittered Exponential Backoff & Infinite Retries

- **Exponential Backoff with Jitter**: When a job is retried, its `run_at` delay is computed as `backoff = min(base * 2^(attempts-1), max_cap) * jitter`, where `jitter` is `[0.9, 1.2]`. This prevents thundering herds when external services recover.
- **Infinite Retry (`max_attempts = 0`)**: For critical tasks like `id-sweep`, setting `MaxAttempts = 0` configures infinite retries—jobs will back off slower on failure, record incrementing attempts and error messages in SQLite, and never transition to `failed`.

## Multi-Queue Consumption & Rate-Limit Pausing

Consumers (`ffxiv-census consume`) poll all registered event queues concurrently (`id-sweep`, `character-census`, `achievement-census`, `fc-census`). External rate limits are tracked per provider:
- **Dual-Source Queues (`id-sweep`, `character-census`)**: Use Lodestone as primary. When Lodestone is rate-limited (HTTP 429), workers automatically route requests through Tomestone.gg. If Tomestone is rate-limited, requests route through Lodestone.
- **Lodestone-Exclusive Queues (`achievement-census`, `fc-census`)**: When Lodestone is rate-limited, these queues pause consumption until the cooldown expires while dual-source queues continue processing.
- **Earliest Cooldown Sleep**: When all providers are paused, the worker sleeps until the earliest provider cooldown expires.

## Dispatcher Algorithm

The worker uses a **dynamic dispatcher with a dedicated retry goroutine** to prioritize retry jobs (attempts > 1) over new jobs.

### Goroutine Allocation

Concurrency is clamped to a minimum of `len(eventTypes) + 1` (1 retry goroutine + 1 goroutine per event type). The allocation formula:

```
minConcurrency = len(eventTypes) + 1
retryWorkers   = 1
newWorkers     = concurrency - retryWorkers
workersPerEvent = newWorkers / len(eventTypes)   // always >= 1 due to clamping
extraWorkers    = newWorkers - (workersPerEvent * len(eventTypes))  // distributed to first event types
```

**Example with concurrency=5 and 4 event types:**
- WorkerID 0: retry goroutine (claims retries from any event type)
- WorkerID 1: primary=id-sweep (new only)
- WorkerID 2: primary=character-census (new only)
- WorkerID 3: primary=achievement-census (new only)
- WorkerID 4: primary=fc-census (new only)

If `--concurrency 3` is passed, it is silently raised to 5. If `--concurrency 8`: 1 retry + 7 distributed (e.g. 2, 2, 2, 1).

### 3-Step Claim Loop

Each goroutine runs a 3-step claim loop on every poll cycle:

1. **Primary queue (preferred mode):** Claim from the goroutine's dedicated event type using `ClaimModeRetriesOnly` (retry goroutine) or `ClaimModeNewOnly` (new-job goroutines).
2. **Borrow from other queues (preferred mode):** If the primary queue is empty or paused, claim from other available event types using the same preferred mode via `ClaimMultiple`.
3. **Fallback to `ClaimModeAny`:** If no jobs were found in the preferred mode, retry both primary and borrowed queues with `ClaimModeAny` to prevent starvation. This means:
   - The retry goroutine can help with new jobs when no retries exist.
   - New-job goroutines can help with retries when no new jobs exist.

### ClaimMode Contract

| Mode | Behavior | Used By |
|---|---|---|
| `ClaimModeAny` | Claims any pending job regardless of attempts | Fallback for all goroutines |
| `ClaimModeNewOnly` | Claims only jobs with `attempts == 0` | New-job goroutines (workerID > 0) |
| `ClaimModeRetriesOnly` | Claims only jobs with `attempts > 0` | Retry goroutine (workerID 0) |

### Rate-Limit Integration

`isEventTypeAvailable` gates claiming per provider availability. Dual-source queues (`id-sweep`, `character-census`) fall back to Tomestone when Lodestone is paused. Lodestone-exclusive queues (`achievement-census`, `fc-census`) pause consumption until cooldown.

### Job Chaining

Handlers return downstream jobs in the `next` slice. The worker's `Complete(id, next...)` publishes them atomically in the same SQLite transaction. No eager `Publish` calls are made from handlers — this prevents double-enqueue and ensures exactly-once downstream delivery.

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
- `ffxiv-census queue purge [--status done|failed|pending|claimed|all] [--older-than 24h] [--all]` — purges old or all jobs immediately.
## Container wiring

`container.Load.Queue()` lazily builds the adapter on top of the SQLite driver (which self-migrates on first use) and caches it. Like `SQLite()`, it degrades to a logged `nil` if the driver or `[queue]` config is unavailable.
