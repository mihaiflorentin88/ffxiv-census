# Dynamic Queue Dispatcher — Design Spec

## Objective

Dedicate exactly 1 goroutine to retry jobs across all event types (capped at 1 when new jobs exist), remove dead code (`classifyJob`, `jobCategory` constants), fix the double-publish bug in handlers, and document the dispatcher algorithm.

## Architecture

### Goroutine Allocation Formula

Concurrency is clamped to a minimum of `len(eventTypes) + 1` (1 retry goroutine + 1 goroutine per event type).

```
minConcurrency = len(eventTypes) + 1
retryWorkers   = 1
newWorkers     = concurrency - retryWorkers
workersPerEvent = newWorkers / len(eventTypes)
extraWorkers    = newWorkers - (workersPerEvent * len(eventTypes))
```

Total goroutines always equals `concurrency`. Extras are distributed to the first event types.

### 3-Step Claim Loop

Each goroutine runs a 3-step claim loop:

1. **Primary queue (preferred mode):** Claim from the goroutine's dedicated event type.
2. **Borrow from other queues (preferred mode):** If primary is empty or paused, claim from other available types via `ClaimMultiple`.
3. **Fallback to `ClaimModeAny`:** If no jobs found in preferred mode, retry with `ClaimModeAny` to prevent starvation.

### Retry Goroutine

WorkerID 0 is the dedicated retry goroutine. It uses `ClaimModeRetriesOnly` when claiming from its primary queue and when borrowing from other queues. Falls back to `ClaimModeAny` when idle.

## Job Categories

| Category | Attempts | Claimed By |
|---|---|---|
| Retries | `attempts > 1` | WorkerID 0 (retry goroutine) preferentially |
| New | `attempts == 0` | All other goroutines preferentially |

Both categories can be claimed by any goroutine via `ClaimModeAny` fallback.

## Chaining

Handlers return downstream jobs in the `next` slice. The worker's `Complete(id, next...)` publishes them atomically in the same SQLite transaction. No eager `Publish` calls are made from handlers — this prevents double-enqueue and ensures exactly-once downstream delivery.

## ClaimMode Contract

| Mode | Behavior | Used By |
|---|---|---|
| `ClaimModeAny` | Claims any pending job regardless of attempts | Fallback for all goroutines |
| `ClaimModeNewOnly` | Claims only jobs with `attempts == 0` | New-job goroutines (workerID > 0) |
| `ClaimModeRetriesOnly` | Claims only jobs with `attempts > 0` | Retry goroutine (workerID 0) |

All three modes are now actively used by the worker.

## Rate-Limit Integration

`isEventTypeAvailable` gates claiming per provider availability:
- **Dual-source queues** (`id-sweep`, `character-census`): fall back to Tomestone when Lodestone is paused.
- **Lodestone-exclusive queues** (`achievement-census`, `fc-census`): pause consumption until cooldown.
- **Earliest cooldown sleep**: when all providers are paused, the worker sleeps until the earliest cooldown expires.

## Tomestone 404 Trust

When Lodestone has a transient error and Tomestone returns 404, handlers treat it as "unknown, retry" rather than "confirmed missing":
- `Lodestone 404 + Tomestone 404` → confirmed missing, skip/mark deleted
- `Lodestone transient error + Tomestone 404` → return error, trigger queue retry

## Lodestone Retry Preference

id-sweep retries always try Lodestone first (`source=auto` re-enters handler, Lodestone checked first via `isEventTypeAvailable(ProviderLodestone)`). If Lodestone is still paused, falls back to Tomestone.

## Verification

- Unit tests: `go test -race ./domain/census/worker/... ./domain/census/handler/...`
- Full suite: `make test`
- Lint: `make lint`
- New tests: `TestWorker_RetryGoroutine_ProcessesAllJobs`, `TestWorker_ConcurrencyClampedToMinimum`, `TestWorker_RetryGoroutine_LogsRetriesOnlyMode`
- Existing tests: `TestWorker_PublishesChainedJobs`, `TestIDSweep_LodestoneError_Tomestone404_ReturnsErrorForLodestoneRetry`, `TestCharacterCensus_LodestoneError_Tomestone404_ReturnsErrorForLodestoneRetry`
