# Implementation Plan: Multi-Queue Consumer, Provider Rate-Limit Pausing, Infinite Retries, Google Drive Backup, and Complete Documentation

- Design Spec: `docs/superpowers/specs/2026-08-18-multi-queue-rate-limit-and-backup.md`

## Context
1. **Multi-Queue Consumer**: The `consume` CLI command must consume from all registered event queues by default (`id-sweep`, `character-census`, `achievement-census`, `fc-census`) with dedicated queues per event type, and support configurable event filtering via flags.
2. **Provider Rate-Limit Pausing & Context Control**: Implement rate-limit tracking for `lodestone` and `tomestone`. When encountering HTTP 429, pause consumption of events tied to that provider for a cooldown period without blocking the other provider.
3. **Resilient Retry Engine for ID-Sweep & Queue**: Ensure failed events enter the queue slower on failure with exponential backoff and jitter, and persist incremented retry counts with infinite retry support (`max_attempts = 0`).
4. **Queue & Migration Architecture Review**: Review the pure-Go SQLite queue implementation and Goose migration setup.
5. **Google Drive Database Backup**: Implement a `backup` CLI command that performs a consistent SQLite `VACUUM INTO` snapshot and uploads the backup archive to Google Drive via service account credentials.
6. **Full Documentation Update**: Update `README.md` and `docs/` to document every CLI command, flag, configuration option, and operational workflow.

---

## Tasks & Phases

### Phase 1: Provider Rate-Limiter Interface & Implementation
- [x] Create `port/contract/provider.go` with `ProviderRateLimiter` interface.
- [x] Implement thread-safe `infrastructure/provider/limiter.go` and `mock/provider.go`.
- [x] Integrate into `container/infrastructure.go` as `container.Load.ProviderRateLimiter()`.
- [x] Wire into `infrastructure/lodestone/lodestone.go` and `infrastructure/tomestone/client.go` to pause on 429s.

### Phase 2: Queue Enhancements & Infinite Retry Mechanics
- [x] Add `ClaimMultiple(ctx, jobTypes, n)` to `contract.Queue` and `infrastructure/queue/queue.go`.
- [x] Add jittered exponential backoff and infinite retry support (`max_attempts = 0`) to `infrastructure/queue/queue.go` and `mock/queue/queue.go`.
- [x] Add unit tests in `infrastructure/queue/resilience_test.go`.

### Phase 3: Worker Multi-Queue & Provider-Aware Consumer
- [x] Update `domain/census/worker/worker.go` to support `RunEvents(ctx, eventTypes, concurrency)`.
- [x] Implement provider rate-limit checking before claiming jobs.
- [x] Add unit tests in `domain/census/worker/rate_limiting_test.go`.

### Phase 4: CLI Updates (`cmd/cli/consume.go`)
- [x] Make `<event>` argument optional in `cmd/cli/consume.go`.
- [x] Add `--events`, `--concurrency`, and `--poll-interval` flags.
- [x] Add CLI tests in `cmd/cli/consume_test.go`.

### Phase 5: Google Drive & Local Backup Command (`cmd/cli/backup.go`)
- [x] Implement `infrastructure/backup/gdrive.go` supporting `VACUUM INTO` and Google Drive API v3.
- [x] Implement `cmd/cli/backup.go` with `--target`, `--output`, `--gdrive-folder-id`, `--service-account-file`, `--service-account-b64`, and `--retention-days`.
- [x] Add tests in `infrastructure/backup/backup_test.go` and `cmd/cli/backup_test.go`.

### Phase 6: Complete Documentation & Verification
- [x] Overhaul `README.md` with complete CLI reference and configuration options.
- [x] Create `docs/backup.md` and update `docs/queue.md`.
- [x] Update `AGENTS.md` and verify all tests pass (`make fmt && make test && make build`).
