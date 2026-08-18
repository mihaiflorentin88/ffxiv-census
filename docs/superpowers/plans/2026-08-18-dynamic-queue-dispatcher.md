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
- Produces: `ClaimMode` enum, `Claim(ctx context.Context, eventType string, batchSize int, mode ClaimMode) ([]Job, error)`

- [ ] **Step 1: Define `ClaimMode` enum**
- [ ] **Step 2: Update `Queue` interface `Claim` method signature**

### Task 2: Implement SQLite Adapter Support (`infrastructure/queue/sqlite.go`)

**Files:**
- Modify: `infrastructure/queue/sqlite.go`

**Interfaces:**
- Consumes: `ClaimMode` from `port/contract/queue.go`

- [ ] **Step 1: Update `Claim` method in SQLite implementation to apply `AND attempts = 0` or `AND attempts > 0` based on `ClaimMode`**

### Task 3: Update Mock Queue (`mock/queue/queue.go`)

**Files:**
- Modify: `mock/queue/queue.go`

- [ ] **Step 1: Update `MockQueue` to implement the new `Claim` signature**

### Task 4: Implement Dispatcher and Worker Pool (`domain/census/worker/worker.go`)

**Files:**
- Modify: `domain/census/worker/worker.go`

- [ ] **Step 1: Define the `Dispatcher` structure (state for in-flight counters, worker pool configuration)**
- [ ] **Step 2: Implement the Dispatch loop (calculate free capacity, round-robin claim logic based on spec categories)**
- [ ] **Step 3: Replace `multiLoop` with the Dispatcher + Worker Pool in `consume` command**

### Task 5: Kubernetes Configuration Updates (`k8s/values.yaml`)

**Files:**
- Modify: `k8s/values.yaml`

- [ ] **Step 1: Set `QUEUE_MAX_ATTEMPTS: "50"`**
- [ ] **Step 2: Update `backup` cronjob schedule**
- [ ] **Step 3: Update `publish-character` cronjob schedule and command**
- [ ] **Step 4: Update `publish-id-sweep` cronjob schedule and command**
- [ ] **Step 5: Update consumer worker concurrency instance count to `20`**

### Task 6: Verification

- [ ] **Step 1: Run `go test -v ./cmd/http/middleware` (Wait, this is queue, not middleware, check `tests`)**
- [ ] **Step 2: Run all tests: `make test`**
- [ ] **Step 3: Run `make lint`**
- [ ] **Step 4: Smoke test locally (Dev mode)**
