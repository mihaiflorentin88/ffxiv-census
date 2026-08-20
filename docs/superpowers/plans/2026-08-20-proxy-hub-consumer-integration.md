# Proxy Hub & Consumer Integration — Implementation Plan

## Context

The census pipeline hits Lodestone at 1 req/s (Cloudflare IP bans). The proxy pool (already implemented: discover, scan, consume) maintains a database of working proxies. This plan integrates those proxies into the census consumer so workers can rotate outbound IPs and bypass Lodestone rate limiting.

**Key design decisions:**
- **Lodestone is the primary target in proxy mode** — proxies let us call Lodestone through different IPs, bypassing its per-IP rate limit. Tomestone is the fallback only when Lodestone is unavailable.
- **Non-proxy mode is 100% unchanged** — the `--proxy` flag is optional. Without it, behavior is identical to current implementation. All existing handler logic, rate limiting, provider fallback order, and worker wiring remain exactly as they are today.
- **ALL requests in proxy mode MUST go through the proxy** — if proxy-mode consumers make any request without a proxy, they share the non-proxy consumer's IP and break its rate limits. This requires a godestone fork.
- **SOCKS support** — add `golang.org/x/net/proxy` for socks4/socks5 dialers alongside native HTTP/HTTPS proxy support.
- **Per-goroutine proxy isolation** — each worker goroutine gets its own proxy via `ProxyHub.NewProxy()`. The proxy is locked to that goroutine's process name + goroutine ID.
- **Locking uses `FOR UPDATE SKIP LOCKED`** — same atomic pattern as the queue claim mechanism (commit `8c4231b`).
- **Proxy re-acquisition on ownership change** — when `CanUse()` returns false, the goroutine acquires a new proxy and retries the job in-place (no queue round-trip).

**Spec:** `docs/superpowers/specs/2026-08-20-proxy-hub-consumer-integration.md`

## Step 0: Write this plan to disk

Write this file to `docs/superpowers/plans/2026-08-20-proxy-hub-consumer-integration.md`. Done.

## Step 1: Fork Godestone — Proxy Support

**Repository:** `github.com/mihaiflorentin88/godestone/v2` (fork of `github.com/xivapi/godestone/v2`)

The fork adds proxy support to the `Scraper` struct. The change is ~10 lines total:

**File:** `scraper.go` — add field and option:
```go
type Scraper struct {
    // ... existing fields ...
    proxyURL string  // NEW: optional proxy URL for all collectors
}

// WithProxy sets a proxy URL for all HTTP requests made by the scraper.
func WithProxy(proxyURL string) func(*Scraper) {
    return func(s *Scraper) { s.proxyURL = proxyURL }
}

// Update NewScraper to accept optional functional options:
func NewScraper(dataProvider provider.DataProvider, lang SiteLang, opts ...func(*Scraper)) *Scraper {
    // ... existing init ...
    s := &Scraper{ /* ... */ }
    for _, opt := range opts {
        opt(s)
    }
    return s
}
```

**Files:** `character.go`, `achievement.go`, `freecompany.go`, `classjob.go`, `linkshell.go`, `pvpteam.go`, `search.go`, `mimo.go` — in each `build*Collector` method, after `c := colly.NewCollector(...)`, add protocol-aware proxy injection:

```go
if s.proxyURL != "" {
    if err := setCollectorProxy(c, s.proxyURL); err != nil {
        // log error, collector proceeds without proxy
    }
}
```

Add helper in `scraper.go`:
```go
func setCollectorProxy(c *colly.Collector, proxyURL string) error {
    u, err := url.Parse(proxyURL)
    if err != nil {
        return err
    }
    switch u.Scheme {
    case "http", "https":
        c.SetProxy(proxyURL)
    case "socks4", "socks5":
        dialer, err := proxy.FromURL(u, proxy.Direct)
        if err != nil {
            return err
        }
        c.WithTransport(&http.Transport{DialContext: dialer.(proxy.ContextDialer).DialContext})
    default:
        return fmt.Errorf("unsupported proxy protocol: %s", u.Scheme)
    }
    return nil
}
```

Import `golang.org/x/net/proxy` in the fork. colly's `SetProxy` only supports HTTP/HTTPS; SOCKS requires `WithTransport` with a custom dialer.

**File:** `go.mod` in ffxiv-census — replace:
```
replace github.com/xivapi/godestone/v2 => github.com/mihaiflorentin88/godestone/v2 v2.10.0-proxy
```

## Step 2: Database Migration — Locking Columns

**File:** `infrastructure/postgres/migration/query/00009_proxy_locking.sql`

```sql
-- +goose Up
ALTER TABLE proxies ADD COLUMN locked_by TEXT;
ALTER TABLE proxies ADD COLUMN locked_at TIMESTAMPTZ;
CREATE INDEX idx_proxies_available ON proxies(status, locked_at, latency_ms)
    WHERE status = 'active';

-- +goose Down
DROP INDEX IF EXISTS idx_proxies_available;
ALTER TABLE proxies DROP COLUMN IF EXISTS locked_at;
ALTER TABLE proxies DROP COLUMN IF EXISTS locked_by;
```

`locked_by` stores the process name + goroutine ID (e.g. `census-consume-g3`). `locked_at` is the lock acquisition time. The partial index speeds up `NewProxy()` queries.

## Step 3: ProxyRepository — Locking Methods

**File:** `port/contract/proxy.go` — add to `ProxyRepository` interface:

```go
ClaimProxy(ctx context.Context, owner string, lockTTL time.Duration) (*ProxyRecord, error)
ExtendLock(ctx context.Context, id int64, owner string, lockTTL time.Duration) (bool, error)
ReleaseProxy(ctx context.Context, id int64, owner string) error
```

**File:** `infrastructure/postgres/repository/proxy.go` — implement using `FOR UPDATE SKIP LOCKED` (same pattern as queue claim).

**File:** `mock/repository/proxy.go` — add fake implementations.

## Step 4: Proxy Domain Object

**File:** `domain/proxy/proxy.go` — new file with getters, `IsActive()`, `CanUse()`, `ExtendLock()`, `SetLockTime()`, `Release()`.

## Step 5: ProxyHub Domain Object

**File:** `domain/proxy/hub.go` — new file with `NewProxy()` atomic acquisition, `SetLockTime()`.

## Step 6: Proxy-Aware HTTP Client

**File:** `infrastructure/httpclient/proxy_client.go` — new file

```go
// NewProxyClient creates an HTTPClient that routes requests through the given proxy.
// The proxy address must include the protocol: http://ip:port, socks4://ip:port, socks5://ip:port.
func NewProxyClient(proxyAddr string, timeout time.Duration) (contract.HTTPClient, error)
```

Implementation — protocol-aware transport construction:
1. Parse `proxyAddr` as URL. Extract `Scheme`.
2. Switch on scheme:
   - `"http"` or `"https"`: `transport := &http.Transport{Proxy: http.ProxyURL(parsedURL)}`
   - `"socks4"` or `"socks5"`: create dialer via `golang.org/x/net/proxy.FromURL(parsedURL, proxy.Direct)`, then `transport := &http.Transport{DialContext: dialer.(proxy.ContextDialer).DialContext}`
   - anything else: return `fmt.Errorf("unsupported proxy protocol: %s", scheme)`
3. Return `httpclient.New(&http.Client{Transport: transport, Timeout: timeout})`

**File:** `infrastructure/httpclient/proxy_client_test.go`

Tests: HTTP proxy transport (verify `Proxy` func set), SOCKS5 proxy transport (verify `DialContext` set), SOCKS4 proxy transport, unsupported protocol returns error, invalid address returns error, timeout propagated.

## Step 7: Proxy-Aware Lodestone Client

**File:** `infrastructure/lodestone/lodestone.go` — add constructor:

```go
// NewClientWithProxy creates a LodestoneClient that routes ALL requests
// (including godestone scraper calls) through the given proxy URL.
// The proxyURL must include the protocol (http://, socks4://, socks5://).
// Uses the forked godestone with protocol-aware proxy support.
func NewClientWithProxy(cfg *config.LodestoneConfig, proxyURL string, logger contract.Logger, rateLimiter ...contract.ProviderRateLimiter) (contract.LodestoneClient, error)
```

Implementation: same as `NewClient` but passes `godestone.WithProxy(proxyURL)` to the scraper constructor. The forked godestone's `setCollectorProxy` handles protocol-aware injection (HTTP via `SetProxy`, SOCKS via `WithTransport`).

## Step 8: Proxy-Aware Tomestone Client

**File:** `infrastructure/tomestone/client.go` — add constructor:

```go
// NewClientWithProxy creates a TomestoneClient that routes all requests
// through the given proxy URL. The proxyURL must include the protocol
// (http://, socks4://, socks5://).
func NewClientWithProxy(cfg *config.TomestoneConfig, proxyURL string, logger contract.Logger, rateLimiter ...contract.ProviderRateLimiter) (*Client, error)
```

Implementation: create a proxy-aware `http.Client` using the same protocol-switching logic as `httpclient.NewProxyClient` (HTTP → `Transport.Proxy`, SOCKS → `Transport.DialContext`), then pass it to the Tomestone client.

## Step 9: ProviderRateProxyLimiter

**File:** `infrastructure/provider/proxy_limiter.go` — goroutine-local rate limiter, same interface as `ProviderRateLimiter`.

## Step 10: Config

**File:** `config/config.go` + `config/config.toml` — add `[proxy.consumer]` section with `lock_ttl`, `lodestone_rate_limit`, `request_timeout`.

## Step 11: Consumer `--proxy` Flag

**File:** `cmd/cli/consume.go` — add `--proxy` flag. When set, use `RunEventsWithProxy`.

**File:** `domain/census/worker/worker.go` — add `RunEventsWithProxy` method with per-goroutine proxy lifecycle.

## Step 12: ProxyHandlers Container Wiring

**File:** `container/domain.go` — add `ProxyHandlers(lodestone, tomestone, rateLimiter)`.

## Step 13: id-sweep Proxy Mode

**File:** `domain/census/handler/idsweep.go` — add `proxyMode` field. When true: Lodestone primary, Tomestone fallback on error only.

## Step 14: Protocol Filtering

Filter providers and ClaimProxy to `http`, `https`, `socks4`, `socks5` only.

## Step 15: Container Wiring

**File:** `container/infrastructure.go` — add:

```go
func (s *ServiceContainer) ProxyHub(owner string) *proxydomain.ProxyHub
```

Builds from `ProxyRepository()` + config lock TTL.

**File:** `container/domain.go` — add:

```go
// ProxyHandlers returns a handler registry wired to proxy-aware Lodestone/Tomestone
// clients. Used by proxy-mode consumer goroutines.
func (s *ServiceContainer) ProxyHandlers(lodestone contract.LodestoneClient, tomestone contract.TomestoneClient, rateLimiter contract.ProviderRateLimiter) *handler.Registry
```

Same as `Handlers()` but with the provided proxy-aware clients.

## Step 16: Helm Chart — Proxy-Mode Census Consumer

**File:** `k8s/values.yaml` — add new worker instance to `workers.instances`:

```yaml
    - name: census-proxy
      replicaCount: 2
      command:
        - /app/ffxiv-census
        - consume
        - all
        - --proxy
        - -c
        - "30"
```

This creates a separate Deployment (`census-proxy`) with 2 replicas running the census consumer in proxy mode. The existing `consumer` worker (1 replica, no `--proxy`) and `proxy-consumer` worker (1 replica, proxy event consumer) remain unchanged.

The Helm `workers.yaml` template already iterates over `workers.instances` and creates a Deployment per instance — no template changes needed.

## Step 17: Hexagonal Architecture Compliance

All new code MUST follow the project's hexagonal architecture (see `docs/architecture.md`, `docs/coding-style.md`, `docs/container.md`):

**Layer rules:**
- `port/contract/` — interfaces and DTOs only. `ProxyRecord` is the DTO; `ProxyRepository` is the interface. No concrete types.
- `domain/proxy/` — pure business logic. `Proxy` and `ProxyHub` accept `contract.ProxyRepository` (interface), never the concrete postgres repository. Domain objects collaborate with infrastructure via contracts and DTOs only.
- `infrastructure/` — concrete adapters implementing `port/contract` interfaces. `ProxyRepository` postgres implementation, `ProxyRateLimiter`, proxy-aware HTTP/Lodestone/Tomestone clients.
- `container/` — service locator wires interfaces to concrete implementations. `ProxyHandlers(lodestone, tomestone, rateLimiter)` accepts interfaces, returns handler registry.
- `cmd/cli/` — constructs domain objects directly, resolves infrastructure via container.

**DTO boundary enforcement:**
- `contract.ProxyRecord` flows from infrastructure → domain via the repository interface.
- `contract.ProxyRecord` flows from domain → `Proxy` domain object (which wraps it).
- No infrastructure struct leaks into domain. No domain struct leaks into infrastructure.
- Proxy-aware client constructors (`NewClientWithProxy`) return `contract.LodestoneClient` / `contract.TomestoneClient` interfaces, not concrete types.

**Interface compliance:**
- `ProxyRateLimiter` must satisfy `contract.ProviderRateLimiter` (compile-time check: `var _ contract.ProviderRateLimiter = (*ProxyRateLimiter)(nil)`).
- `Proxy` and `ProxyHub` are domain objects — they don't implement infrastructure interfaces, they consume them.

## Step 18: Dependency

**File:** `go.mod` — add:

```
require golang.org/x/net v0.x.x
replace github.com/xivapi/godestone/v2 => github.com/mihaiflorentin88/godestone/v2 v2.10.0-proxy
```

Run `go get golang.org/x/net/proxy`.

## Step 19: Tests

| File | Tests |
|------|-------|
| `domain/proxy/proxy_test.go` | CanUse (active+owned → true, active+stolen → false, inactive → false), ExtendLock, Release, SetLockTime, getters |
| `domain/proxy/hub_test.go` | NewProxy (success, no available proxy, all locked), SetLockTime |
| `infrastructure/httpclient/proxy_client_test.go` | HTTP proxy dialer setup, SOCKS5 proxy dialer setup, invalid address, timeout |
| `infrastructure/provider/proxy_limiter_test.go` | IsAvailable, Pause, WaitUntilAvailable, Reset |
| `infrastructure/postgres/repository/proxy_test.go` | ClaimProxy (atomic, skips locked, orders by latency), ExtendLock (owner match, owner mismatch), ReleaseProxy |
| `domain/census/worker/worker_test.go` | RunEventsWithProxy: goroutine acquires proxy, CanUse check, proxy re-acquisition on ownership change |
| `domain/census/handler/idsweep_test.go` | Proxy mode: Lodestone primary, Tomestone fallback on error. Non-proxy mode: unchanged behavior. |
| `cmd/cli/consume_test.go` | --proxy flag parsing, proxy mode wiring |

## Step 20: Documentation

**Files to create/update:**
- `docs/superpowers/specs/2026-08-20-proxy-hub-consumer-integration.md` — design spec
- `docs/proxy.md` — add ProxyHub, consumer integration, proxy mode sections
- `docs/events.md` — note proxy-mode behavior for id-sweep
- `docs/architecture.md` — add proxy hub to domain layer description
- `docs/census.md` — add proxy mode documentation

## Step 21: Verification

1. `make test` — all tests pass (existing + new)
2. `make lint` — clean
3. `go test -race ./domain/proxy/... ./infrastructure/httpclient/... ./infrastructure/provider/...` — race detector clean
4. Manual: start consumer with `--proxy`, verify proxies are claimed from DB, ALL Lodestone/Tomestone requests go through proxy IPs
5. Manual: start consumer without `--proxy`, verify behavior identical to current
6. Manual: run 2 proxy consumers + 1 non-proxy consumer simultaneously, verify no proxy conflicts and non-proxy consumer's IP is not shared
7. Verify proxy re-acquisition: kill a proxy's lock (set locked_at to past), verify new consumer claims it and old consumer detects ownership change via CanUse
8. Helm: `helm template` with updated values.yaml, verify census-proxy Deployment renders with 2 replicas and `--proxy` flag

## File Summary

### New files (12)
| File | Purpose |
|------|---------|
| `infrastructure/postgres/migration/query/00009_proxy_locking.sql` | Locking columns migration |
| `domain/proxy/proxy.go` | Proxy domain object |
| `domain/proxy/proxy_test.go` | Proxy tests |
| `domain/proxy/hub.go` | ProxyHub |
| `domain/proxy/hub_test.go` | ProxyHub tests |
| `infrastructure/httpclient/proxy_client.go` | Proxy-aware HTTP client |
| `infrastructure/httpclient/proxy_client_test.go` | Proxy client tests |
| `infrastructure/provider/proxy_limiter.go` | Goroutine-local rate limiter |
| `infrastructure/provider/proxy_limiter_test.go` | Proxy limiter tests |
| `docs/superpowers/specs/2026-08-20-proxy-hub-consumer-integration.md` | Design spec |

### Modified files (17)
| File | Change |
|------|--------|
| `port/contract/proxy.go` | Add ClaimProxy, ExtendLock, ReleaseProxy |
| `infrastructure/postgres/repository/proxy.go` | Implement locking methods |
| `mock/repository/proxy.go` | Add fake implementations |
| `infrastructure/lodestone/lodestone.go` | Add NewClientWithProxy |
| `infrastructure/tomestone/client.go` | Add NewClientWithProxy |
| `infrastructure/proxyscrape/client.go` | Protocol filter |
| `infrastructure/geonode/client.go` | Protocol filter |
| `domain/proxy/service.go` | Reject unsupported protocols |
| `domain/census/handler/idsweep.go` | Proxy mode |
| `domain/census/worker/worker.go` | RunEventsWithProxy |
| `cmd/cli/consume.go` | --proxy flag |
| `container/infrastructure.go` | ProxyHub accessor |
| `container/domain.go` | ProxyHandlers accessor |
| `config/config.go` | ProxyConsumerConfig |
| `config/config.toml` | [proxy.consumer] section |
| `k8s/values.yaml` | Add census-proxy worker (2 replicas, --proxy flag) |
| `go.mod` / `go.sum` | golang.org/x/net + godestone fork |

### External repository
| Repo | Change |
|------|--------|
| `github.com/mihaiflorentin88/godestone/v2` | Fork: proxyURL field + WithProxy + protocol-aware setCollectorProxy (HTTP via SetProxy, SOCKS via WithTransport) in all collectors |
