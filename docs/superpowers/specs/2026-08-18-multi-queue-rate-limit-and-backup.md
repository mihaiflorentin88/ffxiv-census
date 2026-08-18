# Multi-Queue Consumer, Provider Rate-Limit Pausing, Resilient Retries & Backup Design Spec

## Problem Statement

1. **Consumer Limitations**: `ffxiv-census consume` historically required an explicit `<event>` argument, forcing operators to run 4 separate processes for `id-sweep`, `character-census`, `achievement-census`, and `fc-census`.
2. **Rate Limit Handling**: When hitting Lodestone or Tomestone HTTP 429s, worker routines lacked provider-level coordination, risking spamming rate-limited APIs while unnecessarily halting non-rate-limited queues.
3. **Queue Resilience**: Retried jobs needed jittered exponential backoff to avoid thundering herds, and critical ID sweep tasks required infinite retry persistence (`max_attempts = 0`).
4. **Database Backups**: As SQLite operates as a single-file datastore, operators needed an automated point-in-time snapshot mechanism (`VACUUM INTO`) supporting local rotation and Google Drive cloud uploads.

---

## Architecture & Interfaces

### 1. Provider Rate-Limiter Port & Adapter

- **Interface (`port/contract/provider.go`)**:
  - `IsAvailable(p Provider) bool`
  - `Pause(p Provider, d time.Duration, reason string)`
  - `PausedUntil(p Provider) (time.Time, bool)`
  - `WaitUntilAvailable(ctx context.Context, p Provider) error`
  - `EarliestAvailable() time.Time`
- **Adapter (`infrastructure/provider/limiter.go`)**: Thread-safe in-memory rate-limit state tracker with `sync.RWMutex`.
- **Mock (`mock/provider.go`)**: In-memory fake for deterministic unit tests.

### 2. Multi-Queue & Queue Enhancements

- **Multi-Type Claim**:
  - `contract.Queue.ClaimMultiple(ctx context.Context, jobTypes []string, n int) ([]contract.QueueJob, error)`
  - Atomic SQLite update query utilizing `WHERE type IN (...) AND status = 'pending' AND run_at <= ?`.
- **Jittered Backoff & Infinite Retry**:
  - `backoff = min(base * 2^(attempts-1), max_cap) * jitter` (jitter: `[0.9, 1.2]`).
  - When `max_attempts = 0`, jobs are never marked `failed` and continue retrying with exponential delay.

### 3. Multi-Queue Consumer Worker

- `domain/census/worker/worker.go`:
  - `RunEvents(ctx context.Context, eventTypes []string, concurrency int) error`
  - Dynamically filters available queues based on provider status.
  - If a provider is paused (e.g. Lodestone 429), Lodestone-dependent queues pause while Tomestone ID sweeps continue.
  - If all queues are paused, worker sleeps until the earliest provider cooldown expires.

### 4. Point-in-Time SQLite Backup Engine

- `infrastructure/backup/gdrive.go` & `cmd/cli/backup.go`:
  - SQLite `VACUUM INTO '<snapshot_path>'` for point-in-time consistency.
  - Local retention rotation (`--retention-days`).
  - Google Drive API v3 upload via Service Account key file, Base64 JSON string, or raw JSON string.

---

## Verification & Test Strategy

- `infrastructure/provider`: Unit tests for pause duration, availability checks, and earliest unpause computation.
- `infrastructure/queue`: Unit tests for `ClaimMultiple`, jittered backoff, and infinite retries.
- `domain/census/worker`: Unit tests for multi-queue default consumption and provider-isolated pauses.
- `infrastructure/backup` & `cmd/cli`: Unit tests for snapshot creation, data integrity, and CLI flags.
