# Dynamic Queue Dispatcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current competing-consumer queue worker model with a dynamic Dispatcher and Worker Pool pattern to improve resource utilization and enforce job category concurrency ceilings.

**Architecture:** A single Dispatcher goroutine evaluates worker pool capacity and claims jobs from SQLite based on category ceilings (100% Primary, 25% Retries/Updates/Secondary). Workers consume jobs from a centralized FIFO channel.

**Tech Stack:** Go, SQLite, Kubernetes (Helm), GitHub Actions for testing.

**Spec:** `docs/superpowers/specs/2026-08-18-dynamic-queue-dispatcher.md`

## Global Constraints

- Max Retries (`QUEUE_MAX_ATTEMPTS`): "50"
- Kubernetes: Apply Helm chart changes as per spec section 4.
- Concurrency: Update worker instances command to explicitly pass `-c 20`.

---
### Task 1: Update Queue Contract (`port/contract/queue.go`)

**Files:**
- Modify: `port/contract/queue.go`

**Interfaces:**
- Produces: `ClaimMode` enum, `Claim(ctx context.Context, eventType string, batchSize int, mode ClaimMode) ([]QueueJob, error)`

- [x] **Step 1: Define `ClaimMode` enum (`ClaimModeAny`, `ClaimModeNewOnly`, `ClaimModeRetriesOnly`)**
- [x] **Step 2: Update `Queue` interface `Claim` and `ClaimMultiple` method signatures**
### Task 2: Implement SQLite Adapter Support (`infrastructure/queue/sqlite.go`)

**Files:**
- Modify: `infrastructure/queue/sqlite.go` (and related test files to fix compilation)

**Interfaces:**
- Consumes: `ClaimMode` from `port/contract/queue.go`

- [x] **Step 1: Update `Claim` and `ClaimMultiple` methods in SQLite implementation to handle `ClaimMode` parameter**
- [x] **Step 2: Append `AND attempts = 0` for `ClaimModeNewOnly` and `AND attempts > 0` for `ClaimModeRetriesOnly` in the `UPDATE ... RETURNING` query.**
### Task 3: Update Mock Queue (`mock/queue/fake.go`)

**Files:**
- Modify: `mock/queue/fake.go` (and `mock/queue/queue_test.go` if necessary)

- [x] **Step 1: Update Fake Queue to implement the new `Claim` and `ClaimMultiple` signatures**
### Task 4: Implement Dispatcher and Worker Pool (`domain/census/worker/worker.go`)

**Files:**
- Modify: `domain/census/worker/worker.go`

- [x] **Step 1: Define the Dispatcher state**:
  - `jobChan := make(chan contract.QueueJob, concurrency)`
  - In-flight counters: `primaryInFlight`, `updatesInFlight`, `retriesInFlight`, `secondaryInFlight`.
  - Ceilings: `maxPrimary = concurrency`, `maxUpdates = max(1, floor(concurrency * 0.25))`, `maxRetries = max(1, floor(concurrency * 0.25))`, `maxSecondary = max(1, floor(concurrency * 0.25))`.
- [x] **Step 2: Implement Rate Limiting Support**: Ensure the dispatcher skips claiming for event types if `w.isEventTypeAvailable(et)` returns false (respecting `ProviderRateLimiter`).
- [x] **Step 3: Implement the Dispatch loop** (Runs every `w.pollInterval`): 
  - Calculate free capacity: `C - totalInFlight`. If `0`, continue.
  - **Retries**: Claim up to `min(capacity, maxRetries - retriesInFlight)` across all available event types using `ClaimMultiple(..., ClaimModeRetriesOnly)`.
  - **Updates**: Claim up to `min(capacity, maxUpdates - updatesInFlight)` for `"character-census"` using `Claim(..., ClaimModeNewOnly)`.
  - **Secondary**: Claim up to `min(capacity, maxSecondary - secondaryInFlight)` for `["fc-census", "achievement-census"]` using `ClaimMultiple(..., ClaimModeNewOnly)`.
  - **Primary**: Claim up to `min(capacity, maxPrimary - primaryInFlight)` for `"id-sweep"` using `Claim(..., ClaimModeNewOnly)`.
  - Push claimed jobs to `jobChan` and increment respective in-flight counters.
- [x] **Step 4: Implement the Worker Pool**: `C` goroutines read from `jobChan`, call `h.Handle`, update the DB via `Complete/Retry/Fail`, and decrement the correct in-flight counter when finished (using a `doneChan` or atomic counters).
- [x] **Step 5: Replace `multiLoop` with the Dispatcher + Worker Pool in `RunEvents` command**.

- [x] **Step 1: Set `QUEUE_MAX_ATTEMPTS: "50"`** in `env` block for webserver, workers, and cronjobs.
- [x] **Step 2: Update `backup` cronjob schedule** to `"0 1 * * *"`.
- [x] **Step 3: Update `publish-character` cronjob schedule** to `"30 */3 * * *"` and its command args to `publish character-census --limit 1000`.
- [x] **Step 4: Update `publish-id-sweep` cronjob schedule** to `"0 * * * *"` and its command args to `publish id-sweep --auto --batch-size 3000 --chunk-size 100`.
- [x] **Step 5: Update consumer worker concurrency instance count** to add `"-c"` and `"20"` to the command args.
### Task 6: Verification
- [x] **Step 1: Run `go test -v ./domain/census/worker/... ./infrastructure/queue/...`**
- [x] **Step 2: Run all tests: `make test`**
- [x] **Step 3: Run `make lint`**
- [x] **Step 4: Verify worker runs via manual CLI `consume` command test**
