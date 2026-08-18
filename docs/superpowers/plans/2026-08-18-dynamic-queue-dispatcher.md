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

- [ ] **Step 1: Define `ClaimMode` enum (`ClaimModeAny`, `ClaimModeNewOnly`, `ClaimModeRetriesOnly`)**
- [ ] **Step 2: Update `Queue` interface `Claim` and `ClaimMultiple` method signatures**

### Task 2: Implement SQLite Adapter Support (`infrastructure/queue/sqlite.go`)

**Files:**
- Modify: `infrastructure/queue/sqlite.go` (and related test files to fix compilation)

**Interfaces:**
- Consumes: `ClaimMode` from `port/contract/queue.go`

- [ ] **Step 1: Update `Claim` and `ClaimMultiple` methods in SQLite implementation to handle `ClaimMode` parameter**
- [ ] **Step 2: Append `AND attempts = 0` for `ClaimModeNewOnly` and `AND attempts > 0` for `ClaimModeRetriesOnly` in the `UPDATE ... RETURNING` query.**

### Task 3: Update Mock Queue (`mock/queue/fake.go`)

**Files:**
- Modify: `mock/queue/fake.go` (and `mock/queue/queue_test.go` if necessary)

- [ ] **Step 1: Update Fake Queue to implement the new `Claim` and `ClaimMultiple` signatures**

### Task 4: Implement Dispatcher and Worker Pool (`domain/census/worker/worker.go`)

**Files:**
- Modify: `domain/census/worker/worker.go`

- [ ] **Step 1: Define the Dispatcher state** (in-flight counters for Primary, Updates, Retries, Secondary; and limits based on `concurrency`).
- [ ] **Step 2: Implement Rate Limiting Support**: Ensure the dispatcher skips claiming for event types if `w.isEventTypeAvailable(et)` returns false (respecting `ProviderRateLimiter`).
- [ ] **Step 3: Implement the Dispatch loop**: 
  - Calculate free capacity: `C - totalInFlight`.
  - Round-robin through: Retries (all event types, mode=RetriesOnly), Updates (`character-census`, mode=NewOnly), Secondary (`fc-census`, `achievement-census`, mode=NewOnly), Primary (`id-sweep`, mode=NewOnly).
  - Claim up to `min(free, ceiling - inflight)` for each category and push to `jobChan`.
- [ ] **Step 4: Implement the Worker Pool**: `C` goroutines read from `jobChan`, call the registered handler, update the DB (Complete/Retry/Fail), and signal completion to the dispatcher to decrement `inFlight` counters.
- [ ] **Step 5: Replace `multiLoop` with the Dispatcher + Worker Pool in `RunEvents` command**.

### Task 5: Kubernetes Configuration Updates (`k8s/values.yaml`)

**Files:**
- Modify: `k8s/values.yaml`

- [ ] **Step 1: Set `QUEUE_MAX_ATTEMPTS: "50"`**
- [ ] **Step 2: Update `backup` cronjob schedule**
- [ ] **Step 3: Update `publish-character` cronjob schedule and command**
- [ ] **Step 4: Update `publish-id-sweep` cronjob schedule and command**
- [ ] **Step 5: Update consumer worker concurrency instance count to `20`**

### Task 6: Verification

- [ ] **Step 1: Run `go test -v ./domain/census/worker/... ./infrastructure/queue/...`**
- [ ] **Step 2: Run all tests: `make test`**
- [ ] **Step 3: Run `make lint`**
- [ ] **Step 4: Verify worker runs via manual CLI `consume` command test**
