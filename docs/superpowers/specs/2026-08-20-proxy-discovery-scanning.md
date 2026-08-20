# Proxy Discovery, Scanning & Consumer — Design Spec

## Problem Statement

The census ingest pipeline hits The Lodestone at a hard 1 req/s ceiling to avoid Cloudflare IP bans. With large sweep ranges this creates a bottleneck. Free proxy lists exist publicly (ProxyScrape, Geonode, GitHub mirrors) with no API key required. A proxy pool lets census workers rotate outbound IPs and increase throughput without tripping rate limits.

## Overview

A new `proxy` domain bounded context with three CLI subcommands (`discover`, `scan`, `consume`), two new queue event types, a `proxies` database table, and provider clients for fetching free proxy lists.

- **`discover`** (cronjob, hourly) — Fetches proxy lists from configured providers, publishes `new-proxy` events for each unseen proxy.
- **`scan`** (cronjob, every 20 min) — Queries the database for proxies needing verification, publishes `scan-proxy` events ordered by priority (inactive > stale-active > dead-within-window).
- **`consume`** (long-running) — Processes `new-proxy` and `scan-proxy` events by testing proxies against The Lodestone and updating status/latency.

## Architecture

### Bounded Context

Separate from census: `domain/proxy/`. The proxy pool *serves* the census but is a distinct aggregate.

### Layers

| Layer | Path | Contents |
|-------|------|----------|
| Port contracts | `port/contract/proxy.go` | `ProxyProvider`, `ProxyRepository` interfaces + `ProxyRecord` DTO |
| Domain service | `domain/proxy/service.go` | `ProxyService` — business logic for discover, scan, prioritization |
| Domain handlers | `domain/proxy/handler/` | `new-proxy` and `scan-proxy` event handlers |
| Infrastructure providers | `infrastructure/proxyscrape/`, `infrastructure/geonode/` | HTTP clients implementing `ProxyProvider` |
| Infrastructure checker | `infrastructure/proxy/checker.go` | Tests proxies against The Lodestone |
| Infrastructure repository | `infrastructure/postgres/repository/proxy.go` | `ProxyRepository` SQLite implementation |
| Mock | `mock/repository/proxy.go`, `mock/proxy/provider.go` | In-memory fakes |
| CLI | `cmd/cli/proxy.go` | `proxy discover`, `proxy scan`, `proxy consume` subcommands |
| Container | `container/infrastructure.go`, `container/domain.go` | Wire new adapters |
| Config | `config/config.go`, `config/config.toml` | `[proxy]` section |

## Database

### `proxies` Table

| Column | Type | Notes |
|--------|------|-------|
| `id` | SERIAL PK | Auto-increment |
| `protocol` | TEXT NOT NULL | `http`, `https`, `socks4`, `socks5` |
| `ip` | TEXT NOT NULL | |
| `port` | INTEGER NOT NULL | |
| `country` | TEXT | ISO alpha-2 or null |
| `anonymity` | TEXT | `elite`, `anonymous`, `transparent`, or null |
| `latency_ms` | INTEGER | Last measured latency in ms |
| `uptime_percent` | REAL | Provider-reported uptime if available |
| `status` | TEXT NOT NULL DEFAULT 'inactive' | `active`, `inactive`, `dead` |
| `last_scanned_at` | TIMESTAMP UTC | Last successful or failed scan |
| `last_alive_at` | TIMESTAMP UTC | Last time proxy responded successfully |
| `first_seen_at` | TIMESTAMP UTC NOT NULL | Discovery time |
| `source` | TEXT NOT NULL | Provider name (`proxyscrape`, `geonode`, etc.) |
| `fail_count` | INTEGER NOT NULL DEFAULT 0 | Consecutive scan failures |
| `created_at` | TIMESTAMP UTC NOT NULL | Row creation |
| `updated_at` | TIMESTAMP UTC NOT NULL | Last modification |

**Constraints:**
- `UNIQUE(protocol, ip, port)` — dedup across providers
- Index on `(status, last_scanned_at)` for scan prioritization queries

### Status Lifecycle

```
discover → inactive (new-proxy event)
inactive → active (scan: proxy works)
inactive → dead (scan: fail_count >= threshold OR inactive > 2 days)
active → inactive (scan: proxy fails)
active → dead (scan: inactive > 2 days)
dead → inactive (scan: 3-day window elapsed, re-scan succeeds)
dead → dead (scan: still dead, update last_scanned_at)
```

- **Dead definition:** `status = 'inactive'` AND `last_alive_at` is more than 2 days ago (or never alive AND first_seen_at > 2 days ago).
- **Dead scan frequency:** Dead proxies are re-scanned at most once every 3 days.

## Events

### `new-proxy`

Published by `discover`. Payload:

```json
{"protocol": "http", "ip": "1.2.3.4", "port": 8080, "country": "US", "anonymity": "elite", "source": "proxyscrape", "uptime_percent": 95.5}
```

Handler: Upserts the proxy into the database (INSERT OR IGNORE on unique constraint), then tests it against The Lodestone. Updates status and latency.

### `scan-proxy`

Published by `scan`. Payload:

```json
{"proxy_id": 42}
```

Handler: Fetches the proxy from the database, tests it against The Lodestone, updates status/latency/fail_count/last_scanned_at.

## Proxy Testing (Checker)

Tests a proxy by making an HTTP GET request through it to The Lodestone (`https://na.finalfantasyxiv.com/lodestone/`). Measures:

1. **Reachability** — Does the request succeed (HTTP 200)?
2. **Latency** — Round-trip time in milliseconds.

The checker uses the standard `net/http` transport with the proxy URL set. Timeout: 15 seconds (configurable).

## Providers

### ProxyScrape API v4 (Primary)

- **Endpoint:** `https://api.proxyscrape.com/v4/free-proxy-list/get`
- **Params:** `request=display_proxies`, `proxy_format=protocolipport`, `format=json`, `timeout=10000`
- **Response:** JSON with `alive`, `ip`, `port`, `protocol`, `country`, `anonymity`, `uptime`, `timeout`
- **No API key.** Refreshes every minute.

### Geonode API (Secondary)

- **Endpoint:** `https://proxylist.geonode.com/api/proxy-list`
- **Params:** `limit=500`, `sort_by=lastChecked`, `sort_type=desc`
- **Response:** JSON `{"data": [...]}` with `ip`, `port`, `protocols[]`, `anonymityLevel`, `country`, `upTime`
- **No API key.** Paginated.

### GitHub Mirrors (Fallback)

- ProxyScrape: `https://raw.githubusercontent.com/proxyscrape/free-proxy-list/main/proxies/protocols/{protocol}/data.json`
- monosans: `https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/{protocol}.txt`

## CLI

```bash
# Discover proxies from all configured providers, publish new-proxy events
./bin/ffxiv-census proxy discover

# Scan proxies from DB, publish scan-proxy events (priority-ordered)
./bin/ffxiv-census proxy scan

# Consume new-proxy and scan-proxy events (long-running)
./bin/ffxiv-census proxy consume --concurrency 4
```

### Cronjob Scheduling

In Kubernetes, `proxy discover` runs as a CronJob every hour, `proxy scan` every 20 minutes, and `proxy consume` as a long-running Deployment.

## Configuration

```toml
[proxy]
test_url           = "https://na.finalfantasyxiv.com/lodestone/"
test_timeout       = "15s"
scan_batch_size    = 50
dead_threshold_days = 2
dead_scan_interval_days = 3
fail_count_threshold = 5

[proxy.providers]
proxyscrape = true
geonode     = true
```

## Verification

1. `make test` — all tests pass
2. `make lint` — clean
3. Manual: `proxy discover` → events in queue → `proxy consume` → proxies with status `active` in DB
4. Unit tests for: each provider client, repository CRUD, handler logic, scan prioritization, checker
5. Race detector: `go test -race ./domain/proxy/... ./infrastructure/proxy/...`
