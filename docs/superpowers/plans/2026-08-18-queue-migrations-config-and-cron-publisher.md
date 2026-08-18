# Implementation Plan: Pure-Go SQLite Work Queue & Runtime Goose Migrations, Go-Gitmirror Config & Env Alignment, and Kubernetes CronJob Publisher Execution

## 1. Plan Overview

- **Phase 1: Config Package Alignment with `go-gitmirror`**
  - Updated `config/config.go` with `strings.NewReplacer("-", "_", ".", "_")`, `AutomaticEnv()`, and `embed.FS`.
  - Added unit test suite in `config/config_test.go`.
- **Phase 2: Queue Auto-Retry & Migration Review**
  - Verified `infrastructure/queue/queue.go` atomic claims, jittered exponential backoff, and infinite retries (`max_attempts = 0`).
  - Verified `infrastructure/sqlite/driver.go` embedded Goose migrations with pure-Go `modernc.org/sqlite`.
- **Phase 3: Single-Shot & Auto-Forward Publisher for Kubernetes CronJobs**
  - Added `--auto` and `--batch-size` to `cmd/cli/publish.go`.
  - Configured `buildIDSweepJobs` to use `MaxAttempts: -1` for infinite retries on rate limits.
  - Added comprehensive unit tests in `cmd/cli/publish_test.go`.
- **Phase 4: Documentation & Superpowers Updates**
  - Updated `README.md`, `docs/queue.md`, and `AGENTS.md`.
  - Added spec and plan artifacts in `docs/superpowers/`.
- **Phase 5: Verification & Delivery**
  - Full test suite verification (`make test`).
  - Code formatting and linting (`make fmt && make lint`).
  - Binary compilation (`make build`).
