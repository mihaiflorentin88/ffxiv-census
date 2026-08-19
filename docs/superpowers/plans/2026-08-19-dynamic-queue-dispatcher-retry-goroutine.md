# Dynamic Queue Dispatcher — Retry Goroutine, Cleanup & Chaining Fix

## Context

The queue consumer worker (`domain/census/worker/worker.go`) allocates goroutines evenly across event types but has no retry prioritization — retry jobs (attempts > 1) compete equally with new jobs. The `classifyJob` function and category constants are dead code from an abandoned dispatcher design. Additionally, `id-sweep` and `character-census` handlers double-publish downstream jobs (eagerly via `Publish` during processing AND atomically via `Complete` return). The `ClaimMode` enum exists in the contract but is unused by the worker.

**Goal:** Dedicate exactly 1 goroutine to retry jobs across all event types (capped at 1 when new jobs exist), remove dead code, fix the double-publish bug, and add documentation for the dispatcher algorithm.

## Approach

### Step 1: Fix double-publish bug in handlers

**Files:** `domain/census/handler/idsweep.go`, `domain/census/handler/character.go`

Both handlers call `h.queue.Publish(ctx, jobs...)` during processing AND return the same jobs in the `next` slice. The worker's `Complete(id, next...)` then publishes them again atomically. The dedup constraint prevents double-enqueue but wastes DB round-trips.

**Change:** Remove all `h.queue.Publish(ctx, jobs...)` calls from both handlers. Rely solely on atomic chaining via the returned `next` slice → `Complete(id, next...)`.

### Step 2: Remove dead `queue` field from handler structs

After Step 1 removes the eager Publish calls, the `queue` field and constructor parameter become unused. Remove from both `IDSweep` and `CharacterCensus` structs and constructors. Update all call sites.

### Step 3: Remove dead code from worker.go

Remove the unused `jobCategory` type, `catRetries`/`catUpdates`/`catSecondary`/`catPrimary` constants, and the `classifyJob` function.

### Step 4: Fix goroutine allocation formula and implement dedicated retry goroutine

- Clamp concurrency to `len(eventTypes) + 1` minimum
- Reserve 1 goroutine for retries (workerID 0) using `ClaimModeRetriesOnly`
- All other goroutines use `ClaimModeNewOnly`
- Fallback to `ClaimModeAny` when idle to prevent starvation

### Step 5: Update tests

- Update handler constructor calls to remove `queue` parameter
- Add `TestWorker_RetryGoroutine_ProcessesAllJobs` — 10 new + 5 retry jobs
- Add `TestWorker_ConcurrencyClampedToMinimum` — verify clamping warning
- Add `TestWorker_RetryGoroutine_LogsRetriesOnlyMode` — verify mode logging
- Verify existing `TestWorker_PublishesChainedJobs` still passes

### Step 6: Document the dispatcher algorithm

Add "Dispatcher Algorithm" section to `docs/queue.md` covering goroutine allocation, 3-step claim loop, ClaimMode contract, and rate-limit integration.

### Step 8: Verify Tomestone 404 trust and Lodestone retry preference

Both behaviors already correct. Verify via existing tests `TestIDSweep_LodestoneError_Tomestone404_ReturnsErrorForLodestoneRetry` and `TestCharacterCensus_LodestoneError_Tomestone404_ReturnsErrorForLodestoneRetry`.

### Step 9: Archive plan and spec to docs/superpowers/

Copy plan to `docs/superpowers/plans/` and write spec to `docs/superpowers/specs/`.

## Verification

- `make test` — all tests pass
- `make lint` — clean
- `go test -race ./domain/census/worker/... ./domain/census/handler/...` — race detector clean
