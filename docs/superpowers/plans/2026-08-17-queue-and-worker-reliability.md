# Queue & Worker Reliability Engine with Rich State Introspection — Implementation Plan

**Goal:** Build a robust Queue & Worker Reliability engine featuring error message capture on retries/failures, Dead-Letter Queue (DLQ) replay, worker panic isolation, stale job pruning, and rich `/api/v1/queue/events` inspection exposing active processing jobs, upcoming queued messages, and failed job error traces.

**Architecture:** Hexagonal architecture using SQLite transactional queue persistence. Queue jobs are enriched with `last_error`, `failed_at`, and `completed_at` timestamps. The worker loop isolates panics and propagates handler error strings to the database. The HTTP queue handler exposes aggregated state breakdowns along with message previews (active, pending, failed) for full operational observability.

**Tech Stack:** Go 1.22+, SQLite (modernc.org/sqlite), Goose migrations, net/http REST API, Cobra CLI, table-driven tests with `-race` detection.

## Global Constraints

- Pure Go SQLite driver (`CGO_ENABLED=0` compatible, no CGO).
- Strict TDD (failing test first, minimal implementation, pass, refactor).
- All queue mutations and state transitions must be atomic and transactional.
- Preserve zero-allocation string/hash matching and sha256 payload deduplication.
- Always delegate read-only codebase exploration to tiny LM Studio models via `task`.

## Components & Changes

1. **Database Migration (`00006_queue_reliability_and_errors.sql`):**
   - Adds `last_error`, `failed_at`, and `completed_at` to `queue_jobs`.
   - Adds composite indexes for fast queue scanning and pruning.

2. **Queue Port & Adapters (`port/contract/queue.go`, `infrastructure/queue/queue.go`, `mock/queue/queue.go`):**
   - `Retry(ctx, id, lastErr)` & `Fail(ctx, id, lastErr)` error capture.
   - `RetryFailed(ctx, jobType, limit)` for DLQ replay.
   - `PurgeJobs(ctx, status, olderThan)` for job record pruning.
   - `GetEventDetails(ctx, sampleLimit)` for live introspection.

3. **Worker Panic Recovery (`domain/census/worker/worker.go`):**
   - Wraps handler execution in panic isolation defer/recover.
   - Forwards panic stack traces and handler error messages directly to `queue.Retry`.

4. **REST API & Swagger (`cmd/http/app/census/handler/queue.go`, `routes.go`, `swagger.json`):**
   - `GET /api/v1/queue/events?sample_limit=N` with active, next, and failed jobs.
   - `POST /api/v1/queue/retry-failed` to replay dead-letter jobs.
   - `POST /api/v1/queue/purge` to purge old done/failed records.

5. **CLI Operations (`cmd/cli/queue.go`, `cmd/cli/root.go`):**
   - `ffxiv-census queue stats`
   - `ffxiv-census queue retry-failed`
   - `ffxiv-census queue purge`
