# Proxy Pool

The proxy pool discovers, scans, and maintains a database of working proxies that can be used by census workers to rotate outbound IPs when hitting The Lodestone, reducing rate-limit pressure.

## Architecture

The proxy feature is a separate bounded context (`domain/proxy/`) with its own CLI commands, event handlers, and worker. It follows the same hexagonal architecture as the census pipeline.

### Components

| Layer | Path | Purpose |
|-------|------|---------|
| Port contracts | `port/contract/proxy.go` | `ProxyProvider`, `ProxyRepository`, `ProxyRecord` |
| Domain service | `domain/proxy/service.go` | Business logic for proxy check and status management |
| Domain objects | `domain/proxy/proxy.go` | `Proxy` — wraps `ProxyRecord` with lock management, `CanUse()`, `MarkFailed()` |
| Domain objects | `domain/proxy/hub.go` | `ProxyHub` — atomic proxy acquisition with `NewProxy()` |
| Domain handlers | `domain/proxy/handler/` | `new-proxy` and `scan-proxy` event handlers |
| Domain worker | `domain/proxy/worker/worker.go` | Queue consumer with dedicated goroutines per event type |
| Infrastructure | `infrastructure/proxy/` | Proxy checker (HTTP/SOCKS GET through proxy to Lodestone) |
| Infrastructure | `infrastructure/proxyscrape/` | ProxyScrape API v4 client |
| Infrastructure | `infrastructure/geonode/` | Geonode API client |
| Infrastructure | `infrastructure/httpclient/proxy_client.go` | Proxy-aware HTTP client (HTTP/SOCKS4/SOCKS5) |
| Infrastructure | `infrastructure/provider/proxy_limiter.go` | Goroutine-local rate limiter for proxy mode |
| Infrastructure | `infrastructure/postgres/repository/proxy.go` | PostgreSQL repository |
| Mock | `mock/repository/proxy.go`, `mock/proxy/provider.go` | In-memory fakes for tests |

## CLI

```bash
# Discover proxies from all configured providers, publish new-proxy events
./bin/ffxiv-census proxy discover

# Scan proxies from DB, publish scan-proxy events (priority-ordered)
./bin/ffxiv-census proxy scan [--limit 50]

# Consume new-proxy and scan-proxy events (long-running)
./bin/ffxiv-census proxy consume [--concurrency 4] [--poll-interval 500ms]
```

### Cronjob Scheduling

In Kubernetes:
- `proxy discover` runs as a CronJob every hour
- `proxy scan` runs as a CronJob every 20 minutes
- `proxy consume` runs as a long-running Deployment

## Events

| Event | Payload | Purpose |
|-------|---------|---------|
| `new-proxy` | `{"protocol", "ip", "port", "country", "anonymity", "source", "uptime_percent"}` | Register and test a newly discovered proxy |
| `scan-proxy` | `{"proxy_id"}` | Re-test an existing proxy and update its status |

## Proxy Status Lifecycle

```
discover → inactive (new-proxy event)
inactive → active (scan: proxy works)
inactive → dead (scan: fail_count >= threshold OR inactive > 2 days)
active → inactive (scan: proxy fails)
active → inactive (census worker: proxy connection fails → MarkFailed)
active → dead (scan: inactive > 2 days)
dead → inactive (scan: 3-day window elapsed, re-scan succeeds)
dead → dead (scan: still dead, update last_scanned_at)
```

### Status Definitions

- **active**: Proxy successfully reached The Lodestone on last scan
- **inactive**: Proxy failed last scan but hasn't exceeded failure thresholds, or was marked as failed by a census worker
- **dead**: Proxy inactive for >2 days OR fail_count >= threshold (default: 5). Re-scanned at most once every 3 days.

## Proxy Testing (Checker)

Proxies are tested by making an HTTP GET request through the proxy to `https://na.finalfantasyxiv.com/lodestone/`. If the request returns HTTP 200, the proxy is considered functional. Latency is measured as round-trip time.

### Protocol Support

The proxy checker (`infrastructure/proxy/checker.go`) supports all proxy protocols:

- **HTTP/HTTPS**: Uses `http.Transport.Proxy` for standard HTTP proxy tunneling
- **SOCKS4/SOCKS5**: Uses `golang.org/x/net/proxy` to create a SOCKS dialer, wrapped in `http.Transport.DialContext`

This ensures that proxies of all protocols discovered by the providers can be tested and used by the census workers.

## Providers

### ProxyScrape (Primary)

- **API**: `https://api.proxyscrape.com/v4/free-proxy-list/get`
- **No API key required**
- Returns JSON with IP, port, protocol, country, anonymity, uptime
- Refreshes every minute
- Filter by protocol, country, anonymity, timeout

### Geonode (Secondary)

- **API**: `https://proxylist.geonode.com/api/proxy-list`
- **No API key required**
- Returns JSON with pagination
- Fields: IP, port, protocols, anonymityLevel, country, upTime

## Configuration

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

| Field | Default | Env Override | Description |
|-------|---------|-------------|-------------|
| `test_url` | `https://na.finalfantasyxiv.com/lodestone/` | `PROXY_TEST_URL` | URL to test proxies against |
| `test_timeout` | `15s` | `PROXY_TEST_TIMEOUT` | Timeout per proxy test request |
| `scan_batch_size` | `50` | `PROXY_SCAN_BATCH_SIZE` | Max proxies to queue per scan cron |
| `dead_threshold_days` | `2` | `PROXY_DEAD_THRESHOLD_DAYS` | Days inactive before marking dead |
| `dead_scan_interval_days` | `3` | `PROXY_DEAD_SCAN_INTERVAL_DAYS` | Days between dead proxy re-scans |
| `fail_count_threshold` | `5` | `PROXY_FAIL_COUNT_THRESHOLD` | Consecutive failures before dead |

## Worker

The proxy consumer (`proxy consume`) uses the same dispatcher pattern as the census consumer:

- **WorkerID 0**: Dedicated retry goroutine (`ClaimModeRetriesOnly`)
- **WorkerID 1+**: Per-event-type goroutines (`ClaimModeNewOnly`) with `ClaimModeAny` fallback
- **Concurrency**: Clamped to `len(eventTypes) + 1` minimum (3 for proxy: 1 retry + 1 new-proxy + 1 scan-proxy)
- **Startup**: Reclaims claimed jobs from previous crashed consumers
- **Graceful shutdown**: Stops claiming on signal, finishes in-flight jobs

## Scan Priority

The `proxy scan` command queries the database with priority ordering:

1. **Inactive** proxies (oldest scan first)
2. **Active** proxies not scanned in 10 minutes
3. **Dead** proxies not scanned in 3 days

All matching proxies are published as individual `scan-proxy` events, processed FIFO by the consumer.

---

## Census Consumer Integration

The `consume` command supports a `--proxy` flag for per-goroutine proxy isolation:

```bash
# Standard mode (direct requests)
./bin/ffxiv-census consume --concurrency 8

# Proxy mode (each goroutine acquires its own proxy)
./bin/ffxiv-census consume --proxy --concurrency 8
```

### How It Works

1. Each worker goroutine calls `ProxyHub.NewProxy()` to acquire an available proxy from the database
2. The proxy is locked to that goroutine (process name + goroutine ID)
3. Proxy-aware Lodestone and Tomestone clients are created for that goroutine
4. ALL requests route through the proxy — no direct requests
5. If a proxy fails (connection refused, timeout, host unreachable), the goroutine immediately marks it as failed via `Proxy.MarkFailed()` and acquires a fresh proxy
6. If `CanUse()` returns false (ownership changed by lock expiry), the goroutine acquires a new proxy

### ProxyHub — Atomic Proxy Acquisition

`ProxyHub` (`domain/proxy/hub.go`) manages proxy acquisition for worker goroutines. Each call to `NewProxy(ctx, owner)` atomically claims the best available proxy using `FOR UPDATE SKIP LOCKED` in PostgreSQL.

**ClaimProxy priority** (implemented in `infrastructure/postgres/repository/proxy.go`):

1. **Recently alive** — proxies confirmed alive within the last hour (`last_alive_at >= NOW() - 1h`) are preferred
2. **Highest uptime** — ordered by `uptime_percent DESC NULLS LAST`
3. **Lowest latency** — tiebreaker by `latency_ms ASC NULLS LAST`
4. **Lowest failure count** — final tiebreaker by `fail_count ASC`
5. **Fallback** — if no recently-scanned proxy is available, any active proxy is returned

This ensures workers get the most reliable proxies from the scan pool. The scan (`proxy scan`) tests proxies against the Lodestone URL and updates `last_alive_at`, `latency_ms`, and `status` — so the ClaimProxy query always has fresh data.

**Why this design:** Free proxies are inherently unreliable. The scan identifies which proxies can reach Lodestone right now. The ClaimProxy query prioritizes those, reducing wasted time on dead proxies. When a proxy fails during use, `MarkFailed` increments its `fail_count` and sets it to `inactive`, preventing other workers from picking it up until the next scan re-validates it.

### Proxy Domain Object

`Proxy` (`domain/proxy/proxy.go`) wraps a `ProxyRecord` with domain behavior:

- **`CanUse(owner)`** — returns true if the proxy is active and locked by the given owner
- **`ExtendLock(ctx, owner, lockTTL)`** — extends the lock TTL, but only if the lock hasn't expired (checks `locked_at >= NOW() - lockTTL`)
- **`MarkFailed(ctx, owner)`** — releases the lock AND increments `fail_count`, sets status to `inactive`. This prevents the same bad proxy from being immediately re-acquired by another worker
- **`Release(ctx, owner)`** — releases the lock without marking as failed (used during graceful shutdown)

**Why MarkFailed instead of just Release:** When a proxy fails, simply releasing it would make it available again immediately. Since it has the best latency/uptime score (it was just claimed), the same or another worker would pick it up again — wasting time on a known-bad proxy. `MarkFailed` increments the fail count and sets status to `inactive`, so the proxy goes to the back of the queue until the next scan re-validates it.

### Immediate Proxy Switching

When a proxy fails during a job, the worker switches to a fresh proxy **immediately** — not after N consecutive failures. The flow:

1. Handler returns an error that indicates a proxy issue (connection refused, timeout, host unreachable, service unavailable, proxyconnect error)
2. `processJobWithHandlers` returns `true` (bad proxy signal)
3. Worker calls `proxy.MarkFailed()` to prevent re-acquisition
4. Worker calls `proxyHub.NewProxy()` to get a fresh proxy
5. Worker recreates proxy-aware clients with the new proxy
6. Worker continues claiming and processing jobs with the new proxy

**Why immediate switching:** Free proxies fail frequently. Waiting for multiple failures wastes time — each failure costs 10-15 seconds (timeout). Switching on the first failure means the worker quickly rotates through bad proxies until it finds a working one.

### Graceful Shutdown

On SIGTERM, the shutdown sequence is:

1. `stopClaiming` context is canceled — workers stop claiming new jobs
2. In-flight jobs continue processing with a live `childCtx` (not canceled)
3. Each worker goroutine exits independently when its current job finishes
4. `wg.Wait()` blocks until all workers have completed
5. `defer cancel()` cleans up `childCtx`

**Key decisions:**
- **`queue.Retry` and `queue.Complete` use `context.Background()`** — during shutdown, the process context is canceled. If retry/complete used the canceled context, jobs would stay stuck in `claimed` status. Using `context.Background()` ensures jobs always return to the queue.
- **Worker errors don't cancel `childCtx`** — previously, one failing worker would cancel the shared context, killing ALL other workers' in-flight jobs. Now each worker exits independently and the WaitGroup waits for all to finish naturally.

### Protocol Support

All proxy protocols are supported end-to-end:

| Protocol | Checker | Tomestone Client | Lodestone (Godestone) |
|----------|---------|------------------|----------------------|
| HTTP | `http.Transport.Proxy` | `http.Transport.Proxy` | colly `SetProxy` |
| HTTPS | `http.Transport.Proxy` | `http.Transport.Proxy` | colly `SetProxy` |
| SOCKS4 | `golang.org/x/net/proxy` | `golang.org/x/net/proxy` | `golang.org/x/net/proxy` via `WithTransport` |
| SOCKS5 | `golang.org/x/net/proxy` | `golang.org/x/net/proxy` | `golang.org/x/net/proxy` via `WithTransport` |

**Godestone fork** (`github.com/mihaiflorentin88/godestone/v2`): The upstream godestone library doesn't support proxies. Our fork adds:
- `WithProxy(proxyURL)` option on `NewScraper`
- `setCollectorProxy()` helper that routes HTTP/HTTPS via colly's `SetProxy` and SOCKS via `golang.org/x/net/proxy` with a custom `http.Transport`
- `AllowURLRevisit()` on achievement, character, and classjob collectors — fixes a colly race condition where `URL already visited` errors occur when a scraper call times out and the caller retries with a fresh collector

### Container Accessors

- `container.Load.ProxyHub(owner)` — creates a ProxyHub for the given owner (reads lock TTL from `[proxy.consumer]` config)
- `container.Load.ProxyCensusHandlers(lodestone, tomestone, rateLimiter)` — handler registry wired to proxy-aware clients
- `container.Load.ProxyRepository()` — proxy persistence layer
- `container.Load.ProxyScrapeProvider()` / `GeonodeProvider()` — proxy discovery providers
- `container.Load.ProxyChecker()` — proxy health checker

### Configuration

```toml
[proxy.consumer]
lock_ttl              = "5m"     # How long a goroutine holds a proxy lock
lodestone_rate_limit  = 1.0     # Override Lodestone rate limit (req/s) in proxy mode
request_timeout       = "30s"   # Override HTTP timeout for proxy-aware clients
```

| Field | Default | Env Override | Description |
|-------|---------|-------------|-------------|
| `lock_ttl` | `5m` | `PROXY_CONSUMER_LOCK_TTL` | Duration a goroutine holds exclusive proxy lock |
| `lodestone_rate_limit` | `1.0` | `PROXY_CONSUMER_LODESTONE_RATE_LIMIT` | Lodestone rate limit override (req/s) — overrides base `[lodestone].rate_limit` for proxy-mode clients |
| `request_timeout` | `30s` | `PROXY_CONSUMER_REQUEST_TIMEOUT` | HTTP client timeout override — overrides base `[tomestone].timeout` for proxy-mode clients |

**Why separate config:** Proxy-mode consumers operate through different IPs than the non-proxy consumer. The rate limit and timeout may need to be different — proxies add latency, and rate limits may be more relaxed since each goroutine uses a different IP.

### Kubernetes Deployment

The Helm chart deploys three types of census workers:

```yaml
# Standard consumer (direct requests, no proxy)
- name: consumer
  replicaCount: 1
  command: [/app/ffxiv-census, consume, all, -c, "30"]

# Proxy event consumer (new-proxy, scan-proxy events)
- name: proxy-consumer
  replicaCount: 1
  command: [/app/ffxiv-census, proxy, consume, -c, "30"]

# Proxy-mode census consumer (all events through proxies)
- name: census-proxy
  replicaCount: 2
  command: [/app/ffxiv-census, consume, all, --proxy, -c, "30"]
```

The `census-proxy` workers run with `--proxy` flag — each goroutine acquires its own proxy from the database and routes ALL requests through it. The standard `consumer` runs without proxies for direct access.

### Diagnostic Logging

When troubleshooting proxy issues, the following diagnostic fields are logged:

- **`proxy`** — the proxy URL being used (e.g. `socks5://1.2.3.4:1080`)
- **`scraper`** — godestone scraper instance pointer (e.g. `0x1fd97eeaf9d0`) — useful for verifying that each goroutine has its own scraper
- **`lodestone_client`** — LodestoneClient pointer — useful for verifying client isolation
- **`worker_id`** — worker goroutine index
- **`goroutine_id`** — Go runtime goroutine ID
- **`handler`** — handler instance pointer
- **`owner`** — lock owner name (e.g. `census-consume-g5-p43`)

These fields appear in `worker.job_start`, `lodestone.scrape_retry`, `handler.achievement_census`, and `worker.proxy_bad`/`worker.proxy_reacquired` log entries.
