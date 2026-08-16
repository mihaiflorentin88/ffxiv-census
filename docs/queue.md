# SQLite-backed Work Queue

ffxiv-census runs its durable async work queue in the **same SQLite datastore** as application data. There is no separate queue process: the `queue_jobs` table (applied by goose migration `00002`) holds jobs, and consumers claim them with a single atomic `UPDATE`.

## Lifecycle

```
pending -> claimed -> done
              \-> pending (retry: attempts++, run_at pushed back with exponential backoff)
              \-> failed (permanently, when attempts >= max_attempts)
```

- **Publish** inserts jobs as `pending`. Rows whose `(type, payload_hash)` already exist — in *any* status — are ignored (`INSERT OR IGNORE` on the `UNIQUE (type, payload_hash)` constraint), so re-publishing is idempotent.
- **Claim** atomically marks up to `claim_batch_size` pending jobs of one type as `claimed` and increments `attempts`.
- **Complete** marks a claimed job `done` and publishes downstream jobs in the **same transaction** (atomic chaining).
- **Retry** returns a claimed job to `pending` with `run_at` set to `now + backoff`, or marks it `failed` once `attempts >= max_attempts`.
- **Fail** marks a claimed job `failed` permanently.

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
        queue.Retry(ctx, j.ID) // or Fail(ctx, j.ID) for permanent errors
        continue
    }
    queue.Complete(ctx, j.ID, downstreamJob(j)) // atomic chaining
}
```

`Depth(ctx)` returns the job count per status — useful for dashboards and backpressure signals.

## Container wiring

`container.Load.Queue()` lazily builds the adapter on top of the SQLite driver (which self-migrates on first use) and caches it. Like `SQLite()`, it degrades to a logged `nil` if the driver or `[queue]` config is unavailable.
