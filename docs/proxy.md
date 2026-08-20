# Proxy Pool

The proxy pool discovers, scans, and maintains a database of working proxies that can be used by census workers to rotate outbound IPs when hitting The Lodestone, reducing rate-limit pressure.

## Architecture

The proxy feature is a separate bounded context (`domain/proxy/`) with its own CLI commands, event handlers, and worker. It follows the same hexagonal architecture as the census pipeline.

### Components

| Layer | Path | Purpose |
|-------|------|---------|
| Port contracts | `port/contract/proxy.go` | `ProxyProvider`, `ProxyRepository`, `ProxyRecord` |
| Domain service | `domain/proxy/service.go` | Business logic for proxy check and status management |
| Domain handlers | `domain/proxy/handler/` | `new-proxy` and `scan-proxy` event handlers |
| Domain worker | `domain/proxy/worker/worker.go` | Queue consumer with dedicated goroutines per event type |
| Infrastructure | `infrastructure/proxy/` | Proxy checker (HTTP GET through proxy to Lodestone) |
| Infrastructure | `infrastructure/proxyscrape/` | ProxyScrape API v4 client |
| Infrastructure | `infrastructure/geonode/` | Geonode API client |
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
active → dead (scan: inactive > 2 days)
dead → inactive (scan: 3-day window elapsed, re-scan succeeds)
dead → dead (scan: still dead, update last_scanned_at)
```

### Status Definitions

- **active**: Proxy successfully reached The Lodestone on last scan
- **inactive**: Proxy failed last scan but hasn't exceeded failure thresholds
- **dead**: Proxy inactive for >2 days OR fail_count >= threshold (default: 5). Re-scanned at most once every 3 days.

## Proxy Testing

Proxies are tested by making an HTTP GET request through the proxy to `https://na.finalfantasyxiv.com/lodestone/`. If the request returns HTTP 200, the proxy is considered functional. Latency is measured as round-trip time.

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
5. If `CanUse()` returns false (ownership changed), the goroutine acquires a new proxy and retries the job in-place

### Container Accessors

- `container.Load.ProxyHub(owner)` — creates a ProxyHub for the given owner
- `container.Load.ProxyCensusHandlers(lodestone, tomestone, rateLimiter)` — handler registry wired to proxy-aware clients

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
| `lodestone_rate_limit` | `1.0` | `PROXY_CONSUMER_LODESTONE_RATE_LIMIT` | Lodestone rate limit override (req/s) |
| `request_timeout` | `30s` | `PROXY_CONSUMER_REQUEST_TIMEOUT` | HTTP client timeout override |
