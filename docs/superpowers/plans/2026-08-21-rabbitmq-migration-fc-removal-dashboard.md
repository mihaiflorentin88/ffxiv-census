# 2026-08-21 — RabbitMQ Queue Migration, FC Removal, Dashboard Upgrade

## Goal

Replace the PostgreSQL-backed poll-based queue with RabbitMQ push-based consumption, remove the dead Free Company collection feature, test proxies before handout, and upgrade the dashboard UI.

## What Was Implemented

### 1. RabbitMQ Helm Chart (`/home/mihai/Workspace/rabbitmq-chart`)

Created a standalone Helm chart following pgres-chart patterns:

- `Chart.yaml` — rabbitmq, appVersion 4.3.5
- `values.yaml` — NodePort (AMQP 30672, Management 31672), Vault ESO integration
- `templates/` — _helpers.tpl, vault.yaml (SecretStore + ExternalSecret for RABBITMQ_USER/RABBITMQ_PASSWORD), serviceaccount.yaml, statefulset.yaml (rabbitmq:4.3.-management), service.yaml (NodePort), NOTES.txt
- Deployed to k8s, vhost `ffxiv-census` created via `RABBITMQ_DEFAULT_VHOST`
- Vault policy `rabbitmq` created and added to `default-namespace` k8s auth role

### 2. RabbitMQ Go Adapter (`infrastructure/rabbitmq/queue.go`)

New package implementing simplified `contract.Queue`:

**Topology (per event type):**
- Exchange `census` (direct) → routes to `census.<type>` queues
- `census.<type>.failed` queue — dead-letters back to main exchange for retry
- Messages with TTL auto-retry, messages without TTL stay permanently

**Retry mechanism:**
- attempts < 5: publish to `census.<type>.failed` with TTL (5s, 10s, 20s, 40s, 80s)
- attempts >= 5: publish to `census.<type>.failed` without TTL (permanent)
- Attempt count stored in `x-attempts` header

**Connection resilience:** reconnect on closed connection with fresh channel.

### 3. Simplified Queue Contract (`port/contract/queue.go`)

Replaced 15-method interface with 3 methods:
```go
type Queue interface {
    Publish(ctx context.Context, job QueueJob) error
    Consume(ctx context.Context, eventTypes []string, concurrency int, handler func(ctx context.Context, job QueueJob) error) error
    Close() error
}
```

`QueueJob` simplified to `{Type string, Payload []byte}` — no ID, Status, Attempts, MaxAttempts, PayloadHash, etc.

### 4. Push-Based Workers

**Census worker** (`domain/census/worker/worker.go`):
- `RunEvents` calls `queue.Consume(ctx, eventTypes, concurrency, processJob)`
- `processJob` dispatches to handler, publishes downstream jobs individually via `queue.Publish`
- Returns error for retry (queue handles DLX internally)
- Suppresses `context.Canceled` on shutdown

**Proxy worker** (`domain/proxy/worker/worker.go`):
- Same push-based pattern
- `RunEventsWithProxy` with per-goroutine proxy lifecycle

### 5. ProxyHub Test-Before-Handout (`domain/proxy/hub.go`)

`NewProxyHub(repo, lockTTL, checker)` — added `*proxyinfra.Checker` parameter.

`NewProxy` now tests proxies before handing out:
- Claims proxy from DB
- Tests via `checker.Check(ctx, protocol, ip, port)`
- If check fails: `MarkFailed`, try next (up to 3 attempts)
- If all fail: return nil

### 6. Container Wiring (`container/infrastructure.go`)

- `Queue()` — creates `rabbitmq.New(cfg.GetURL(), logger)` instead of PG queue
- `ProxyHub()` — passes `s.ProxyChecker()` to `NewProxyHub`
- Removed `FreeCompanyRepository()` accessor
- Removed `infrastructure/queue` import, added `infrastructure/rabbitmq`

### 7. Config Changes (`config/config.go`, `config/config.toml`)

- Added `RabbitMQConfig` struct (URL, Host, Port, User, Password, Vhost) with `GetURL()` builder
- Added `[rabbitmq]` section to config.toml
- Removed `QueueConfig` struct and `[queue]` section
- Env overrides: `RABBITMQ_URL`, `RABBITMQ_HOST`, `RABBITMQ_PORT`, `RABBITMQ_USER`, `RABBITMQ_PASSWORD`, `RABBITMQ_VHOST`

### 8. CLI Updates

**`cmd/cli/publish.go`:**
- Single `Publish(ctx, job)` calls instead of variadic
- Removed FC census command
- Removed `--min-pending-jobs` flag from id-sweep daemon
- Removed dedup logging (no dedup in RabbitMQ)

**`cmd/cli/consume.go`:**
- Removed `--poll-interval` flag
- Removed `SetPollInterval` calls
- Removed rate limiter from `worker.New()`

**`cmd/cli/proxy.go`:**
- `proxy consume` now accepts optional positional arg for event type
- `proxy consume scan-proxy -c 5` works

**`cmd/cli/migrate.go`:**
- New `migrate queue` command — reads PG queue_jobs, publishes to RabbitMQ

### 9. Free Company Removal

**Deleted files (11):**
- `domain/census/handler/fc.go`, `fc_test.go`
- `infrastructure/postgres/repository/free_company.go`, `free_company_test.go`
- `mock/repository/free_company.go`
- `cmd/http/app/census/handler/free_company.go`, `free_company_test.go`
- `cmd/http/ui/free_company.go`, `free_company_test.go`, `templates/free_company_detail.html`
- `port/contract/free_company_repository.go`

**Modified files:**
- `domain/census/handler/event.go` — removed `EventFreeCompanyCensus`, `FreeCompanyCensusPayload`, `FreeCompanyCensusJob`
- `domain/census/service.go` — removed FreeCompanyRepository dependency, FC methods
- `container/domain.go` — removed FC handler registration
- `cmd/cli/consume.go` — removed fc-census from defaults
- `cmd/http/app/census/routes.go` — removed FC API routes
- `cmd/http/ui/routes.go` — removed FC UI routes
- `cmd/http/ui/character.go` — removed FreeCompany from CharacterProfileViewData
- `cmd/http/ui/templates/character.html` — removed FC detail section
- `port/contract/census.go` — removed FreeCompanyRecord
- `port/dto/response/census.go` — removed FC DTOs

### 10. Queue Dashboard & API Removal

**Deleted files:**
- `cmd/http/app/census/handler/queue.go`, `queue_test.go`
- `port/dto/response/queue.go`
- `cmd/cli/queue.go`, `queue_test.go`
- `infrastructure/queue/queue.go`, `queue_test.go`, `resilience_test.go`
- `mock/queue/queue.go`, `queue_test.go`
- `infrastructure/postgres/migration/query/00002_create_queue_jobs.sql`
- `infrastructure/postgres/migration/query/00006_queue_reliability_and_errors.sql`
- `config/queue_test.go`

**Modified files:**
- `cmd/http/app/census/routes.go` — removed `/api/v1/queue/*` routes
- `cmd/cli/root.go` — removed `queueCmd` registration
- `cmd/http/ui/dashboard.go` — removed queue depth section

### 11. Database Migrations

- `00010_drop_queue_jobs.sql` — `DROP TABLE IF EXISTS queue_jobs`
- `00011_drop_free_companies.sql` — `DROP TABLE IF EXISTS free_companies CASCADE`

### 12. K8s Helm Chart Updates (`k8s/`)

**`values.yaml`:**
- Added `RABBITMQ_URL` env var to all workers and cronjobs
- Split workers into per-event-type deployments:
  - `id-sweep`, `character-census`, `achievement-census` (direct consumers)
  - `proxy-id-sweep`, `proxy-character-census`, `proxy-achievement-census` (proxy consumers)
  - `proxy-new`, `proxy-scan` (proxy discovery/scanning)
- Each with configurable concurrency via `-c` flag

**`templates/vault.yaml`:**
- Added SecretStore for `rabbitmq/prod`
- Added `RABBITMQ_USER`/`RABBITMQ_PASSWORD` to ExternalSecret

### 13. Dashboard UI Upgrade (`cmd/http/ui/dashboard.go`, `templates/dashboard.html`)

- Removed queue-related info
- Added **Race Distribution** doughnut chart (Chart.js)
- Added **MSQ Expansion Completion** cards with progress bars
- All 6 data fetches run in parallel via `sync.WaitGroup` + `sync.Mutex`
- Data: summary, time series, region breakdown, race distribution, expansion completions

### 14. Documentation Updates

**Rewritten:**
- `docs/queue.md` — complete rewrite for RabbitMQ architecture

**Updated (removed FC, old queue patterns, added RabbitMQ):**
- `docs/events.md`
- `docs/architecture.md`
- `docs/census.md`
- `docs/proxy.md`
- `docs/http-api.md`
- `docs/lodestone.md`
- `docs/logging-and-middleware.md`
- `README.md`

**Swagger cleanup:**
- Removed queue endpoints (`/api/v1/queue/*`)
- Removed FC endpoints (`/api/v1/census/free-companies/*`)
- Removed old QueueJob schema, FC schemas
- Updated `swagger.yaml`, `swagger.json`, `docs.go`

### 15. Credential Cleanup

- Removed real RabbitMQ password from `config/config.toml`
- Replaced with `guest:guest` placeholders
- Real credentials in Vault at `rabbitmq/prod`

## Verification

- `go build ./...` — passes
- `go vet ./...` — passes
- `go test ./...` — all 26 packages pass
- RabbitMQ deployed and running on k8s (NodePort 30672/31672)
- Vault secret `rabbitmq/prod` created with RABBITMQ_USER/RABBITMQ_PASSWORD
- ESO ExternalSecret synced

## Files Modified/Created/Deleted Summary

**Created:** 8 files (rabbitmq adapter, migrations, helm chart)
**Modified:** ~30 files (workers, CLI, container, config, templates, docs, swagger)
**Deleted:** ~25 files (PG queue, FC collection, queue API/dashboard, old tests)
