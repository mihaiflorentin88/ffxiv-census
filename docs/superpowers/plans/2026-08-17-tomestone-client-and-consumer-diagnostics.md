# Tomestone.gg API Client & Consumer Worker Diagnostics — Implementation Plan

**Goal:**
1. **Explain and fix consumer worker visibility**: Explain why `publish` followed by `consume` produced no job logs (the job range `36795950` to `36795999` was already marked `done` in `data/ffxiv-census.db`, so `INSERT OR IGNORE` was a no-op and the 20 workers had 0 pending jobs). Enhance `publish` to log actual inserted vs deduplicated job counts, and enhance `consume` to emit an informative `INFO` log when 0 pending jobs exist so users know workers are waiting for work.
2. **Implement Tomestone.gg API client**: Build a production-grade, tested `TomestoneClient` adapter implementing `port/contract.TomestoneClient` for `https://tomestone.gg` REST API (`GET /api/character/profile/{id}` and `GET /api/character/profile/{server}/{name}`), supporting Laravel Sanctum Bearer token authentication, rate limiting, timeouts, error mapping, and model conversions to domain character records.

---

## 1. Root Cause Analysis of Consumer Logs

When running:
```bash
./bin/ffxiv-census publish id-sweep --from 36795950 --to 36795999 --chunk-size 100
./bin/ffxiv-census consume id-sweep --concurrency 20
```
- The database `data/ffxiv-census.db` already contained job `ID=6` (`{"from":36795950,"to":36795999}`) with `status='done'`.
- `q.Publish` uses SQLite `INSERT OR IGNORE INTO queue_jobs ...` on the `UNIQUE(type, payload_hash)` constraint. Since job 6 already existed, `RowsAffected` was 0 and no new job was created.
- `publishCmd` calculated `len(jobs) == 1` in memory and logged `publish.id_sweep <- jobs: 1` without checking if rows were actually inserted.
- When `consume` launched 20 workers, all 20 queried for `status='pending' AND type='id-sweep'` and received 0 jobs.
- The worker loop logs `worker.idle` at `DEBUG` level (suppressed in standard stdout), so the terminal stayed quiet after `worker.start` and `queue.reclaim`, making it appear hung when it was simply waiting for new pending work.

---

## 2. Architecture & Design Decisions

### A. Publish & Consume Diagnostics
- **`infrastructure/queue/queue.go`**: Return `(inserted int, err error)` from `Publish` or record actual inserted count.
- **`cmd/cli/publish.go`**: Update log message to report `enqueued: X, deduplicated: Y` so users immediately see if their published range was ignored due to prior completion.
- **`domain/census/worker/worker.go`**: On startup and periodically when idle, if `w.queue.CountJobs(ctx, ...)` finds 0 pending jobs for the event type, emit `w.logger.InfoContext(ctx, "worker.queue_status", slog.String("event_type", eventType), slog.Int64("pending_jobs", 0), slog.String("notice", "no pending jobs in queue, waiting for new publications..."))`.

### B. Tomestone.gg Integration
- **API Specs & Security**: `https://tomestone.gg/api-docs?api-docs.json` (OpenAPI 3.0.0). Endpoints require Laravel Sanctum Bearer tokens (`Authorization: Bearer <token>`). Tokens are generated in user Account Settings on `https://tomestone.gg`.
- **Config**:
  ```toml
  [tomestone]
  api_token = ""
  base_url = "https://tomestone.gg"
  rate_limit = 10.0
  timeout = "10s"
  ```
  Environment variable override: `TOMESTONE_API_TOKEN`, `TOMESTONE_BASE_URL`, `TOMESTONE_RATE_LIMIT`, `TOMESTONE_TIMEOUT`.
- **Port Contract (`port/contract/tomestone.go`)**:
  - `TomestoneCharacter`: domain representation of Tomestone's profile JSON (ID, Name, Server, DC, Gender, Race, Tribe, GrandCompany, FreeCompany, Jobs, ActiveJob, Gear, UpdatedAt).
  - `TomestoneClient` interface:
    - `FetchCharacterProfile(ctx context.Context, id uint32, update bool) (*TomestoneCharacter, error)`
    - `FetchCharacterProfileByName(ctx context.Context, server, name string, update bool) (*TomestoneCharacter, error)`
    - `IsConfigured() bool`
- **Adapter (`infrastructure/tomestone/client.go`)**:
  - Net/HTTP client with timeout, Bearer auth header injection, token-bucket rate limiter (`golang.org/x/time/rate`), and structured logging (`contract.Logger`).
  - Error mapping: 401 Unauthenticated (`contract.ErrTomestoneUnauthenticated`), 404 Not Found (`contract.ErrCharacterNotFound`), 429 Rate Limited with warning logs, 5xx server errors.
- **Mock (`mock/tomestone/tomestone.go`)**: In-memory fake implementing `contract.TomestoneClient` for unit tests.
- **CLI (`cmd/cli/tomestone.go`)**:
  - `./bin/ffxiv-census tomestone character <id> | <server> <name> [--update] [--raw]` command to test and inspect live Tomestone API profile fetching directly.

---

## 3. Ordered Implementation Steps

### Task 1: Queue Publish Deduplication Visibility & Worker Idle Diagnostics

**Files:**
- Modify: `port/contract/queue.go`, `infrastructure/queue/queue.go`, `mock/queue/queue.go`, `cmd/cli/publish.go`, `domain/census/worker/worker.go`
- Test: `infrastructure/queue/queue_test.go`, `mock/queue/queue_test.go`, `domain/census/worker/worker_test.go`, `cmd/http/app/census/handler/census_test.go`

**Step 1.1: Update `contract.Queue` & `Publish` return values**
- Change `Publish(ctx context.Context, jobs ...QueueJob) error` to:
  `Publish(ctx context.Context, jobs ...QueueJob) (int, error)` (returns number of newly inserted jobs).
- In `infrastructure/queue/queue.go`: sum `res.RowsAffected()` across all inserted jobs in the batch and return total inserted count.
- In `mock/queue/queue.go`: count how many jobs were inserted vs deduplicated and return inserted count.
- Update all callsites across repository to handle the returned `(int, error)` or `(_, err)`.

**Step 1.2: Update `cmd/cli/publish.go`**
- In `publishIDSweepCmd` and `publishCharacterCensusCmd`:
  - Call `inserted, err := q.Publish(...)`.
  - Log `publish.id_sweep` with `slog.Int("requested", len(jobs))`, `slog.Int("enqueued", inserted)`, `slog.Int("deduplicated", len(jobs)-inserted)`.
  - If `inserted == 0`, log a visible `WARN` explaining: `all requested jobs already exist in queue (done/pending/failed); no new work enqueued`.

**Step 1.3: Update `domain/census/worker/worker.go`**
- In `worker.Run`:
  - Query `w.queue.CountJobs(ctx, contract.QueueJobFilter{Type: eventType, Status: contract.QueueJobPending})`.
  - If `pending_jobs == 0`:
    `w.logger.InfoContext(ctx, "worker.queue_status", slog.String("event_type", eventType), slog.Int64("pending_jobs", 0), slog.String("notice", "no pending jobs in queue, waiting for new publications..."))`.

---

## 4. Tomestone.gg Configuration & Port Contract

**Files:**
- Modify: `config/config.go`, `config/config.toml`
- Create: `port/contract/tomestone.go`, `config/tomestone_test.go`

**Step 2.1: Add `TomestoneConfig` to `config/config.go` and `config/config.toml`**
```go
type TomestoneConfig struct {
	APIToken  string  `mapstructure:"api_token"`
	BaseURL   string  `mapstructure:"base_url"`
	RateLimit float64 `mapstructure:"rate_limit"`
	Timeout   string  `mapstructure:"timeout"`
}
```
Defaults: `BaseURL = "https://tomestone.gg"`, `RateLimit = 10.0`, `Timeout = "10s"`.

**Step 2.2: Define `port/contract/tomestone.go`**
- Define `TomestoneCharacter`, `TomestoneClassJob`, `TomestoneGear`.
- Define sentinel errors: `ErrTomestoneUnauthenticated`, `ErrTomestoneDisabled`.
- Define `TomestoneClient` interface:
```go
type TomestoneClient interface {
	FetchCharacterProfile(ctx context.Context, id uint32, update bool) (*TomestoneCharacter, error)
	FetchCharacterProfileByName(ctx context.Context, server, name string, update bool) (*TomestoneCharacter, error)
	IsConfigured() bool
}
```

---

## 5. Tomestone.gg Infrastructure Client & Mock

**Files:**
- Create: `infrastructure/tomestone/client.go`, `infrastructure/tomestone/client_test.go`, `mock/tomestone/tomestone.go`, `mock/tomestone/tomestone_test.go`, `container/tomestone_test.go`
- Modify: `container/infrastructure.go`

**Step 3.1: Write tests in `infrastructure/tomestone/client_test.go`**
- Test `FetchCharacterProfile` with successful 200 JSON payload and verify field mapping.
- Test `FetchCharacterProfile` with 401 response -> returns `contract.ErrTomestoneUnauthenticated`.
- Test `FetchCharacterProfile` with 404 response -> returns `contract.ErrCharacterNotFound`.
- Test `FetchCharacterProfile` with 429 response -> logs warning and returns rate limit error.
- Test unconfigured client (`api_token == ""`) -> `IsConfigured() == false`.

**Step 3.2: Implement `infrastructure/tomestone/client.go`**
- Parse JSON response from `https://tomestone.gg/api/character/profile/{id}`.
- Map fields into `contract.TomestoneCharacter` and conversion helper `ConvertToCharacterRecord()`.
- Use `rate.Limiter` to enforce rate limit ceiling.

**Step 3.3: Implement `mock/tomestone/tomestone.go`**
- In-memory fake implementing `contract.TomestoneClient` for domain testing.

**Step 3.4: Wire into `container/infrastructure.go`**
- Add `TomestoneClient() contract.TomestoneClient` lazy accessor to service container.

---

## 6. CLI Commands & Documentation

**Files:**
- Create: `cmd/cli/tomestone.go`, `docs/tomestone.md`
- Modify: `docs/queue.md`

**Step 4.1: Add `tomestone character` CLI command in `cmd/cli/tomestone.go`**
- `./bin/ffxiv-census tomestone character <id> | <server> <name> [--update] [--raw]`
- Fetches character profile via Tomestone API and prints JSON summary, or reports if token is missing.

**Step 4.2: Update Documentation**
- Add `docs/tomestone.md` explaining how to configure Tomestone.gg API tokens, available endpoints, and how it supplements Lodestone.
- Update `docs/queue.md` with the new publish deduplication count behavior.

---

## 7. Verification & Testing

1. **Unit & Race Tests**:
   - `go test -race ./...`
2. **Linter & Code Formatting**:
   - `make fmt && PATH="$HOME/go/bin:$PATH" make lint`
3. **Live CLI Smoke Tests**:
   - Run `./bin/ffxiv-census publish id-sweep --from 36795950 --to 36795999 --chunk-size 100` and verify it reports `enqueued: 0, deduplicated: 1` with an informative warning.
   - Run `./bin/ffxiv-census consume id-sweep --concurrency 2` and verify it immediately logs `worker.queue_status: pending_jobs: 0 (no pending jobs in queue, waiting for new publications...)`.
   - Publish a new fresh range: `./bin/ffxiv-census publish id-sweep --from 36796000 --to 36796005 --chunk-size 10` -> verify `enqueued: 1`.
   - Verify consumer claims and processes the new range with real-time logs.
   - Test `./bin/ffxiv-census tomestone character 36795950` with/without token.
