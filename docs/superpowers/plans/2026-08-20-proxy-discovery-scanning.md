# Proxy Discovery, Scanning & Consumer — Implementation Plan

## Context

The census pipeline is rate-limited to 1 req/s on Lodestone. Free proxy providers (ProxyScrape, Geonode) offer public APIs with no keys required. This plan adds a proxy pool: discover proxies from providers, scan them for viability against The Lodestone, and maintain a database of working proxies for future census worker consumption.

**Spec:** `docs/superpowers/specs/2026-08-20-proxy-discovery-scanning.md`

**Critical patterns to follow** (from census consumer fixes):
- `FOR UPDATE SKIP LOCKED` in claim subqueries (commit `8c4231b`)
- `ReclaimClaimed` on worker startup (commit `d192209`)
- Dedicated retry goroutine (workerID 0) + per-event-type goroutines (commit `68756f7`)
- `ClaimMode` system: `NewOnly`/`RetriesOnly` with `Any` fallback to prevent starvation
- Graceful shutdown: `stopClaiming` cancels on signal, `childCtx` stays alive for in-flight jobs
- Panic recovery in `processJob` with stack trace forwarded to `queue.Retry`
- Handler interface: `Handle(ctx, payload) ([]QueueJob, error)` with `Registry`
- No batch publishing — `discover` and `scan` publish individual events per proxy

## Step 0: Write this plan to disk

Write this file to `docs/superpowers/plans/2026-08-20-proxy-discovery-scanning.md`. Done.

## Step 1: Database Migration

**File:** `infrastructure/postgres/migration/query/00008_create_proxies.sql`

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE proxies (
    id              SERIAL PRIMARY KEY,
    protocol        TEXT NOT NULL,
    ip              TEXT NOT NULL,
    port            INTEGER NOT NULL,
    country         TEXT,
    anonymity       TEXT,
    latency_ms      INTEGER,
    uptime_percent  REAL,
    status          TEXT NOT NULL DEFAULT 'inactive',
    last_scanned_at TIMESTAMPTZ,
    last_alive_at   TIMESTAMPTZ,
    first_seen_at   TIMESTAMPTZ NOT NULL,
    source          TEXT NOT NULL,
    fail_count      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    UNIQUE(protocol, ip, port)
);
CREATE INDEX idx_proxies_scan_priority ON proxies(status, last_scanned_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS proxies;
-- +goose StatementEnd
```

## Step 2: Port Contracts

**File:** `port/contract/proxy.go`

```go
type ProxyRecord struct {
    ID             int64
    Protocol       string
    IP             string
    Port           int
    Country        *string
    Anonymity      *string
    LatencyMS      *int
    UptimePercent  *float64
    Status         string  // "active", "inactive", "dead"
    LastScannedAt  *time.Time
    LastAliveAt    *time.Time
    FirstSeenAt    time.Time
    Source         string
    FailCount      int
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

type ProxyProvider interface {
    Name() string
    FetchProxies(ctx context.Context) ([]ProxyRecord, error)
}

type ProxyRepository interface {
    Upsert(ctx context.Context, rec ProxyRecord) (exists bool, err error)
    Get(ctx context.Context, id int64) (*ProxyRecord, error)
    UpdateStatus(ctx context.Context, id int64, status string, latencyMS *int, failCount int, lastAliveAt *time.Time) error
    UpdateScanTime(ctx context.Context, id int64) error
    ListForScan(ctx context.Context, limit int) ([]ProxyRecord, error)
    ListActive(ctx context.Context, limit int) ([]ProxyRecord, error)
    Count(ctx context.Context) (int64, error)
    CountByStatus(ctx context.Context) (map[string]int64, error)
}
```

`Upsert` uses `INSERT ... ON CONFLICT(protocol, ip, port) DO UPDATE` — returns `exists=true` when the proxy was already in the DB (so discover can skip re-publishing).

`ListForScan` priority ordering (single query with `CASE WHEN`):
1. `status = 'inactive'` → priority 0, ordered by `last_scanned_at ASC NULLS FIRST`
2. `status = 'active' AND last_scanned_at < now - 10min` → priority 1
3. `status = 'dead' AND last_scanned_at < now - 3 days` → priority 2

## Step 3: Event Types, Payloads & Handler Interface

**File:** `domain/proxy/handler/event.go`

```go
const (
    EventNewProxy  = "new-proxy"
    EventScanProxy = "scan-proxy"
)

type NewProxyPayload struct {
    Protocol      string   `json:"protocol"`
    IP            string   `json:"ip"`
    Port          int      `json:"port"`
    Country       *string  `json:"country,omitempty"`
    Anonymity     *string  `json:"anonymity,omitempty"`
    Source        string   `json:"source"`
    UptimePercent *float64 `json:"uptime_percent,omitempty"`
}

type ScanProxyPayload struct {
    ProxyID int64 `json:"proxy_id"`
}
```

Plus `NewProxyJob(rec)` and `ScanProxyJob(proxyID)` builder functions (same pattern as `AchievementCensusJob`, `CharacterCensusJob`).

**File:** `domain/proxy/handler/handler.go`

Replicate the census handler interface and registry pattern exactly:

```go
type Handler interface {
    Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error)
}

type Registry struct {
    handlers map[string]Handler
}

func NewRegistry() *Registry
func (r *Registry) Register(eventType string, h Handler)
func (r *Registry) Get(eventType string) (Handler, bool)
```

## Step 4: Mock Implementations

**Files:**
- `mock/repository/proxy.go` — In-memory `ProxyRepository` fake
- `mock/proxy/provider.go` — In-memory `ProxyProvider` fake

## Step 5: Infrastructure — Proxy Checker

**File:** `infrastructure/proxy/checker.go`

```go
type Checker struct {
    testURL     string
    timeout     time.Duration
    logger      contract.Logger
    client      *http.Client
}

func NewChecker(testURL string, timeout time.Duration, logger contract.Logger) *Checker

// Check tests a proxy by making an HTTP GET through it to the test URL.
// Returns latency in ms or an error.
func (c *Checker) Check(ctx context.Context, protocol, ip string, port int) (latencyMS int, err error)
```

Uses `net/http` with `Transport{Proxy: http.ProxyURL(proxyURL)}`. Timeout from config. Measures round-trip time. The checker does NOT use the Lodestone client (godestone) — it's a simple HTTP GET to `https://na.finalfantasyxiv.com/lodestone/` to verify the proxy works for Lodestone traffic.

## Step 6: Infrastructure — ProxyScrape Client

**File:** `infrastructure/proxyscrape/client.go`

Implements `contract.ProxyProvider`. Calls ProxyScrape API v4 (`https://api.proxyscrape.com/v4/free-proxy-list/get`), maps JSON response to `[]contract.ProxyRecord`. Filter for alive proxies only. Uses the shared `contract.HTTPClient`.

## Step 7: Infrastructure — Geonode Client

**File:** `infrastructure/geonode/client.go`

Implements `contract.ProxyProvider`. Calls Geonode API with pagination (`https://proxylist.geonode.com/api/proxy-list`), maps response to `[]contract.ProxyRecord`. Uses the shared `contract.HTTPClient`.

## Step 8: Infrastructure — Proxy Repository

**File:** `infrastructure/postgres/repository/proxy.go`

Implements `contract.ProxyRepository`. SQL queries against the `proxies` table.

## Step 9: Domain Service

**File:** `domain/proxy/service.go`

```go
type Service struct {
    providers  []contract.ProxyProvider
    repo       contract.ProxyRepository
    checker    *proxy.Checker
    logger     contract.Logger
}

func NewService(providers []contract.ProxyProvider, repo contract.ProxyRepository, checker *proxy.Checker, logger contract.Logger) *Service

// ProcessNewProxy inserts a discovered proxy and tests it.
// Returns nil if proxy already exists (dedup).
func (s *Service) ProcessNewProxy(ctx context.Context, payload NewProxyPayload) error

// ProcessScanProxy tests an existing proxy and updates its status.
func (s *Service) ProcessScanProxy(ctx context.Context, proxyID int64) error
```

Shared `processProxyCheck` logic:
- Check passes → `status = "active"`, `latency_ms = measured`, `fail_count = 0`, `last_alive_at = now`
- Check fails → `fail_count++`, if `fail_count >= threshold` OR inactive > 2 days → `status = "dead"`, else `status = "inactive"`
- Always update `last_scanned_at = now`

## Step 10: Domain Worker

**File:** `domain/proxy/worker/worker.go`

Replicate `domain/census/worker/worker.go` pattern exactly, adapted for proxy events:

```go
type Worker struct {
    queue       contract.Queue
    handlers    *handler.Registry
    logger      contract.Logger
    pollInterval time.Duration
}

func New(q contract.Queue, h *handler.Registry, logger contract.Logger) *Worker
func (w *Worker) SetPollInterval(d time.Duration)
func (w *Worker) Run(ctx context.Context, eventType string, concurrency int) error
func (w *Worker) RunEvents(ctx context.Context, eventTypes []string, concurrency int) error
```

Key behaviors (matching census worker):
- **Default event types:** `[EventNewProxy, EventScanProxy]`
- **Concurrency clamp:** minimum `len(eventTypes) + 1` = 3 (1 retry + 1 per event type)
- **Startup:** `ReclaimClaimed` for each event type before spawning goroutines
- **WorkerID 0:** Dedicated retry goroutine using `ClaimModeRetriesOnly`
- **Other goroutines:** `ClaimModeNewOnly` per event type, `ClaimModeAny` fallback
- **Graceful shutdown:** `stopClaiming` context cancels on signal, `childCtx` for in-flight jobs
- **`processJob`:** Panic recovery → `queue.Retry`, success → `queue.Complete` with chained jobs
- **No rate-limit awareness needed:** `isEventTypeAvailable` always returns `true` (proxy checks don't share Lodestone rate limits — each check is an independent HTTP request through a different proxy)
- **Poll interval:** Default 500ms (configurable via `--poll-interval`)

**File:** `domain/proxy/worker/worker_test.go`

Tests:
- `TestWorker_ProcessesNewProxyJobs`
- `TestWorker_ProcessesScanProxyJobs`
- `TestWorker_PublishesChainedJobs`
- `TestWorker_RetryGoroutine_ProcessesAllJobs`
- `TestWorker_ConcurrencyClampedToMinimum`
- `TestWorker_ReclaimsClaimedOnStart`
- `TestWorker_GracefulShutdown`

## Step 11: Domain Handlers

**File:** `domain/proxy/handler/new_proxy.go`

```go
type NewProxy struct {
    service *proxy.Service
    logger  contract.Logger
}

func NewNewProxy(svc *proxy.Service, logger contract.Logger) *NewProxy
func (h *NewProxy) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error)
```

Unmarshal `NewProxyPayload`, call `service.ProcessNewProxy`. Return empty `next` (leaf event — no chaining).

**File:** `domain/proxy/handler/new_proxy_test.go`

Tests with mock repo/checker: successful new proxy, duplicate proxy (already exists), checker failure.

**File:** `domain/proxy/handler/scan_proxy.go`

```go
type ScanProxy struct {
    service *proxy.Service
    logger  contract.Logger
}

func NewScanProxy(svc *proxy.Service, logger contract.Logger) *ScanProxy
func (h *ScanProxy) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error)
```

Unmarshal `ScanProxyPayload`, call `service.ProcessScanProxy`. Return empty `next` (leaf event).

**File:** `domain/proxy/handler/scan_proxy_test.go`

Tests with mock repo/checker: proxy becomes active, proxy becomes inactive, proxy becomes dead.

## Step 12: Container Wiring

**File:** `container/infrastructure.go`

Add lazy singleton accessors:
- `ProxyRepository() contract.ProxyRepository` — builds from `Database()`
- `ProxyChecker() *proxy.Checker` — builds from `[proxy]` config
- `ProxyScrapeProvider() contract.ProxyProvider` — builds ProxyScrape client using `HTTPClient()`
- `GeonodeProvider() contract.ProxyProvider` — builds Geonode client using `HTTPClient()`

**File:** `container/domain.go`

Add:
- `ProxyService() *proxy.Service` — wires providers, repo, checker
- `ProxyHandlers() *proxyhandler.Registry` — registers `NewProxy` and `ScanProxy` handlers (same pattern as `Handlers()`)
- `ProxyWorker() *pworker.Worker` — builds worker from `Queue()`, `ProxyHandlers()`, `Logger()`

## Step 13: Config

**File:** `config/config.go`

Add structs:
```go
type ProxyConfig struct {
    TestURL              string             `mapstructure:"test_url"`
    TestTimeout          string             `mapstructure:"test_timeout"`
    ScanBatchSize        int                `mapstructure:"scan_batch_size"`
    DeadThresholdDays    int                `mapstructure:"dead_threshold_days"`
    DeadScanIntervalDays int                `mapstructure:"dead_scan_interval_days"`
    FailCountThreshold   int                `mapstructure:"fail_count_threshold"`
    Providers            ProxyProviderConfig `mapstructure:"providers"`
}

type ProxyProviderConfig struct {
    ProxyScrape bool `mapstructure:"proxyscrape"`
    Geonode     bool `mapstructure:"geonode"`
}
```

**File:** `config/config.toml`

```toml
[proxy]
test_url                = "https://na.finalfantasyxiv.com/lodestone/"
test_timeout            = "15s"
scan_batch_size         = 50
dead_threshold_days     = 2
dead_scan_interval_days = 3
fail_count_threshold    = 5

[proxy.providers]
proxyscrape = true
geonode     = true
```

## Step 14: CLI

**File:** `cmd/cli/proxy.go`

Three subcommands under `proxy`:

```bash
# Discover proxies from all configured providers, publish new-proxy events
./bin/ffxiv-census proxy discover

# Scan proxies from DB, publish scan-proxy events (priority-ordered)
./bin/ffxiv-census proxy scan [--limit 50]

# Consume new-proxy and scan-proxy events (long-running)
./bin/ffxiv-census proxy consume [--concurrency 4] [--poll-interval 500ms]
```

`discover`:
1. Resolve providers from container (based on config toggles)
2. For each provider, call `FetchProxies`
3. For each proxy, publish a `new-proxy` event via `queue.Publish` (individual events, not batch)
4. Log counts: discovered, published, skipped (already in queue via dedup)
5. Exit

`scan`:
1. Call `ProxyRepository.ListForScan(limit)` to get priority-ordered proxies
2. For each proxy, publish a `scan-proxy` event via `queue.Publish`
3. Log count of published scan events
4. Exit

`consume`:
1. Resolve `ProxyWorker` from container
2. Signal handling: `signal.NotifyContext` for SIGINT/SIGTERM
3. Call `worker.RunEvents(ctx, eventTypes, concurrency)`
4. Same pattern as `cmd/cli/consume.go`

**File:** `cmd/cli/proxy_test.go`

Tests for each subcommand.

## Step 15: Tests

| File | Tests |
|------|-------|
| `domain/proxy/service_test.go` | ProcessNewProxy (success, duplicate, checker fail), ProcessScanProxy (active, inactive, dead transitions, fail_count threshold) |
| `domain/proxy/handler/new_proxy_test.go` | Handler with mock service: success, bad payload |
| `domain/proxy/handler/scan_proxy_test.go` | Handler with mock service: success, bad payload, proxy not found |
| `domain/proxy/worker/worker_test.go` | Retry goroutine, concurrency clamping, ReclaimClaimed, graceful shutdown, panic recovery |
| `infrastructure/proxy/checker_test.go` | Success, timeout, connection refused (use httptest.Server) |
| `infrastructure/proxyscrape/client_test.go` | Parse JSON response, empty list, HTTP error (use httptest.Server) |
| `infrastructure/geonode/client_test.go` | Parse JSON response, pagination, empty list, HTTP error |
| `infrastructure/postgres/repository/proxy_test.go` | Upsert, Get, UpdateStatus, ListForScan priority ordering, CountByStatus |
| `mock/repository/proxy_test.go` | Conformance tests mirroring repository tests |

## Step 16: Documentation

**Files to create/update:**
- `docs/proxy.md` — New doc: architecture, CLI usage, configuration, status lifecycle
- `docs/architecture.md` — Add `domain/proxy/` to directory cheat sheet
- `docs/queue.md` — Add `new-proxy` and `scan-proxy` events
- `docs/events.md` — Add proxy events table with payloads

## Step 17: Verification

1. `make test` — all tests pass (existing + new)
2. `make lint` — clean
3. `go test -race ./domain/proxy/... ./infrastructure/proxy/... ./infrastructure/proxyscrape/... ./infrastructure/geonode/...` — race detector clean
4. Manual: `proxy discover` → events in queue → `proxy consume` → proxies with `active` status in DB
5. Verify scan prioritization: insert test proxies with various statuses, run `proxy scan`, verify event order
6. Verify worker: `proxy consume` processes both event types with dedicated goroutines, reclaims claimed on restart

## File Summary

### New files (22)
| File | Purpose |
|------|---------|
| `infrastructure/postgres/migration/query/00008_create_proxies.sql` | DB migration |
| `port/contract/proxy.go` | ProxyRecord, ProxyProvider, ProxyRepository |
| `domain/proxy/service.go` | ProxyService business logic |
| `domain/proxy/service_test.go` | Service tests |
| `domain/proxy/handler/event.go` | Event types, payloads, job builders |
| `domain/proxy/handler/handler.go` | Handler interface + Registry |
| `domain/proxy/handler/new_proxy.go` | New-proxy handler |
| `domain/proxy/handler/new_proxy_test.go` | Handler tests |
| `domain/proxy/handler/scan_proxy.go` | Scan-proxy handler |
| `domain/proxy/handler/scan_proxy_test.go` | Handler tests |
| `domain/proxy/worker/worker.go` | Proxy worker (same pattern as census worker) |
| `domain/proxy/worker/worker_test.go` | Worker tests |
| `infrastructure/proxy/checker.go` | Proxy checker (HTTP GET via proxy) |
| `infrastructure/proxy/checker_test.go` | Checker tests |
| `infrastructure/proxyscrape/client.go` | ProxyScrape provider client |
| `infrastructure/proxyscrape/client_test.go` | Provider tests |
| `infrastructure/geonode/client.go` | Geonode provider client |
| `infrastructure/geonode/client_test.go` | Provider tests |
| `infrastructure/postgres/repository/proxy.go` | Proxy repository |
| `infrastructure/postgres/repository/proxy_test.go` | Repository tests |
| `mock/repository/proxy.go` | In-memory ProxyRepository fake |
| `mock/proxy/provider.go` | In-memory ProxyProvider fake |
| `cmd/cli/proxy.go` | CLI: discover, scan, consume |
| `cmd/cli/proxy_test.go` | CLI tests |
| `docs/proxy.md` | Proxy feature documentation |

### Modified files (6)
| File | Change |
|------|--------|
| `config/config.go` | Add ProxyConfig, ProxyProviderConfig |
| `config/config.toml` | Add [proxy] section |
| `container/infrastructure.go` | Add ProxyRepository, ProxyChecker, provider accessors |
| `container/domain.go` | Add ProxyService, ProxyHandlers, ProxyWorker |
| `docs/architecture.md` | Add proxy to directory cheat sheet |
| `docs/events.md` | Add proxy events |
