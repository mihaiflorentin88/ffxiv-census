# ffxiv-census

**ffxiv-census** is a high-performance, single-binary FFXIV census engine and data crawler built with Go and pure-Go SQLite (`modernc.org/sqlite`). It follows a hexagonal (ports & adapters) architecture, featuring an embedded HTMX Web UI, REST APIs, OpenAPI/Swagger specifications, Prometheus metrics, and a durable, multi-queue consumer with jittered exponential backoff and provider rate-limiting pause controls.

---

## Quick Start

```bash
go mod tidy
make build
./bin/ffxiv-census server --start --port 8080
```

Access the dashboard at `http://localhost:8080` or API documentation at `http://localhost:8080/swagger/index.html`.

---

## Documentation Index

Detailed architectural and subsystem documentation lives under `docs/`:

- `docs/getting-started.md` — Setup, building, dependencies, and environment notes.
- `docs/architecture.md` — High-level system design, ports & adapters layout, and conventions.
- `docs/container.md` — Explains the Service Locator pattern and dependency wiring.
- `docs/sqlite.md` — SQLite storage, WAL mode, pure-Go driver, and automatic Goose migrations.
- `docs/queue.md` — SQLite-backed durable multi-queue, atomic claims, jittered backoff, and infinite retries.
- `docs/backup.md` — Point-in-time SQLite `VACUUM INTO` snapshots, retention, and Google Drive backups.
- `docs/lodestone.md` — The Lodestone scraper adapter (rate limiting, Cloudflare protection, retries).
- `docs/tomestone.md` — Tomestone.gg integration for fast character lookups and dual-source census.
- `docs/census.md` — Domain models, milestones, active character metrics, and repositories.
- `docs/events.md` — Ingest event pipeline, message payloads, and downstream chaining.
- `docs/http-api.md` — REST API endpoints, query filters, search, and data aggregation.
- `docs/metrics.md` — Prometheus metrics, health checks, and scrape endpoints.
- `docs/logging-and-middleware.md` — Structured slog logging, correlation IDs, and HTTP middleware.
- `docs/ui.md` — Embedded HTMX dashboard, server-side Go templates, and Chart.js visualizations.

---

## CLI Reference

All commands accept `--help` and report build details via `--version`. Database migrations run automatically at boot on the first database operation.

### 1. `server` — Web UI, REST API & Metrics

Starts the HTTP/HTTPS server serving the HTMX Web UI, REST API, Swagger UI, and Prometheus `/metrics`.

```bash
# Start server on default port 8080
./bin/ffxiv-census server --start --port 8080

# Start with TLS encryption
./bin/ffxiv-census server --start --port 8443 --cert-file cert.pem --key-file key.pem

# Enable pprof profiler on :6060
./bin/ffxiv-census server --start --port 8080 --profile
```

### 2. `consume` — Multi-Queue Worker with Provider Rate-Limit Pausing

Runs long-running consumer worker goroutines. By default, consumes from **all** registered event queues concurrently (`id-sweep`, `character-census`, `achievement-census`, `fc-census`).

When an external provider (Lodestone or Tomestone) returns HTTP 429 (rate limit exceeded), the worker automatically pauses consumption of queues dependent on that provider for a cooldown period without blocking the other provider's queues.

```bash
# Consume from ALL event queues concurrently (default 4 workers)
./bin/ffxiv-census consume

# Consume from specific queues using the --events flag
./bin/ffxiv-census consume --events "id-sweep,character-census" --concurrency 8

# Consume a single queue via positional argument
./bin/ffxiv-census consume achievement-census --concurrency 2

# Adjust polling interval for idle queues
./bin/ffxiv-census consume --poll-interval 250ms
```

### 3. `publish` — Enqueue Census Operations

Enqueues census jobs to be processed asynchronously by worker consumers.

#### `publish id-sweep` — Forward Range, Gap-Fill & Kubernetes CronJob Execution
Probes character ID ranges across Lodestone or Tomestone. Designed for both single-shot scheduled execution (e.g. **Kubernetes CronJob**) and continuous loop mode (`--daemon`). Uses chunking and infinite retries (`max_attempts = 0`).

```bash
# Auto-forward mode (recommended for CronJobs): sweeps next 1,000 IDs starting from MaxID + 1
./bin/ffxiv-census publish id-sweep --auto --batch-size 1000 --chunk-size 100

# Gap-fill mode: scans unscanned holes between 1 and MaxID
./bin/ffxiv-census publish id-sweep --fill-gaps --chunk-size 100

# Explicit range: probe character IDs 1 to 10,000 in chunks of 100
./bin/ffxiv-census publish id-sweep --from 1 --to 10000 --chunk-size 100

# Sweep using Tomestone API instead of Lodestone
./bin/ffxiv-census publish id-sweep --auto --source tomestone
```

#### `publish character-census`
Enqueues known characters that have not been updated within a specified duration.
```bash
# Re-census characters older than 30 days
./bin/ffxiv-census publish character-census --older-than 720h --limit 1000
```

#### `publish fc-census`
Enqueues Free Company profile updates.
```bash
# Re-census stale Free Companies
./bin/ffxiv-census publish fc-census --older-than 720h --limit 500
```

### 4. `queue` — Queue Inspection & Dead-Letter Replay

Administrative commands to inspect queue depth, replay failed jobs, or purge historical records.

```bash
# Display live ASCII table with queue depths and sample jobs
./bin/ffxiv-census queue stats

# Display stats for a specific event type with up to 10 samples
./bin/ffxiv-census queue stats --event-type id-sweep --sample-limit 10

# Replay failed dead-letter jobs back to pending
./bin/ffxiv-census queue retry-failed --event-type character-census --limit 500

# Purge completed jobs older than 7 days
./bin/ffxiv-census queue purge --status done --older-than 168h

# Purge all pending jobs immediately
./bin/ffxiv-census queue purge --status pending --all

# Purge entire queue (all statuses, all events)
./bin/ffxiv-census queue purge --all

### 5. `export` — Data Export (CSV & JSON)

Exports census data for characters, free companies, or achievements directly to disk.

```bash
# Export all active characters to CSV
./bin/ffxiv-census export characters --format csv --output ./exports/characters.csv

# Export free companies on a specific datacenter to JSON
./bin/ffxiv-census export free-companies --datacenter Chaos --format json --output ./exports/fc_chaos.json

# Export character achievements
./bin/ffxiv-census export achievements --format csv --output ./exports/achievements.csv
```

### 6. `migrate` — Manual Schema Migrations

Manage database schema versions manually with Goose.

```bash
# Run all pending migrations (default)
./bin/ffxiv-census migrate --direction up

# Roll back all migrations (destructive)
./bin/ffxiv-census migrate --direction down
```

### 7. `tomestone` — Direct Character Inspection

Queries Tomestone.gg directly to inspect character profiles and verify API keys.

```bash
# Inspect character by numeric Lodestone ID
./bin/ffxiv-census tomestone --id 12345678

# Inspect character by server and character name
./bin/ffxiv-census tomestone --server Ragnarok --name "Firstname Lastname"
```

---

## Configuration & Environment Variables

`ffxiv-census` embeds `config.toml` by default and uses Viper with `strings.NewReplacer("-", "_", ".", "_")` and `AutomaticEnv()`. Any configuration key can be overridden using uppercase environment variables (both nested keys with `.` and hyphenated keys with `-` map to underscores `_`).
| Environment Variable | Description | Default |
|---|---|---|
| `SQLITE_PATH` | Path to SQLite database file | `data/ffxiv-census.db` |
| `LOGGING_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `LODESTONE_RATE_LIMIT` | Lodestone scraper rate limit (requests/sec, max 1.0) | `1.0` |
| `TOMESTONE_API_TOKEN` | Bearer API token for Tomestone.gg | `""` |
| `TOMESTONE_RATE_LIMIT` | Tomestone API rate limit (requests/sec, max 20.0) | `5.0` |
| `QUEUE_CLAIM_BATCH_SIZE` | Default number of jobs claimed per batch | `4` |
| `QUEUE_MAX_ATTEMPTS` | Default retry attempts before dead-lettering (0 = infinite) | `5` |
| `QUEUE_BACKOFF_BASE_SECONDS` | Initial backoff delay for retried jobs | `5` |

---

## Architecture & Event Pipeline

The census ingest pipeline is durable and event-driven via the SQLite-backed work queue:

```text
publish id-sweep ──────────► consume (all queues) ──┬──► achievement-census
                                                  └──► fc-census (if in FC)

publish character-census ──► consume (all queues) ──┬──► achievement-census
                                                  └──► fc-census (if in FC)
```

### Ingest Events

| Event Name | Purpose | Payload Schema | Provider(s) | Downstream Event Cascading |
|---|---|---|---|---|
| `id-sweep` | Probes ranges of character IDs to discover and ingest newly created or active characters. Non-existent IDs are skipped without failing the chunk. | `{"from": 1, "to": 1000, "source": "auto"}` | **Dual-Source**: Tomestone.gg (primary, 5 req/s) + Lodestone (fallback) | `achievement-census` (+ `fc-census` if affiliated with an FC) |
| `character-census` | Re-censuses a known character's profile, job levels, and affiliation. Confirmed 404 on both providers marks the character deleted. | `{"character_id": 12345}` | **Dual-Source**: Lodestone (primary) + Tomestone.gg (fallback) | `achievement-census` (+ `fc-census` if affiliated with an FC) |
| `achievement-census` | Fetches character achievements and updates registered expansion and milestone progression. | `{"character_id": 12345}` | **Lodestone-exclusive** | *None (leaf job)* |
| `fc-census` | Fetches Free Company profile and roster details. | `{"fc_id": "9234567890123456789"}` | **Lodestone-exclusive** | *None (leaf job)* |

### Provider Coordination & Automatic Queue Switching

- **Dual-Source Queues (`id-sweep`, `character-census`)**: `id-sweep` uses Tomestone.gg as primary (5 req/s REST API for fast discovery) with Lodestone fallback; `character-census` uses Lodestone as primary (authoritative source) with Tomestone.gg fallback. When Tomestone returns 404 on `id-sweep`, Lodestone is probed as fallback. When Lodestone returns 404 on `character-census`, Tomestone is probed. If the fallback also returns 404, the character is confirmed missing. If the fallback is unavailable, the job retries with exponential backoff.
- **Lodestone-Exclusive Queues (`achievement-census`, `fc-census`)**: When Lodestone encounters HTTP 429 or is paused, workers pause these queues and process dual-source queues via Tomestone.gg.
- **Earliest Cooldown Sleep**: If all external providers are rate-limited simultaneously, workers sleep until the earliest cooldown expires without burning database transactions or CPU cycles.
---

## Build & Test Automation

```bash
# Run all tests
make test

# Format source code
make fmt

# Run static linter
make lint

# Compile production binary
make build
```

---

## Release Workflow

The application release process involves reading current release tags from GitHub, bumping the version tag according to Semantic Versioning (`vMAJOR.MINOR.PATCH`), creating and pushing the Git tag, building cross-compiled Linux/ARM64 artifacts, pushing Docker images, and deploying via Helm to Kubernetes.

> **Important**: Always check existing Git and Docker tags before releasing, and bump the version tag (e.g., `v1.0.0` -> `v1.0.1`). Do not reuse or overwrite existing release tags.

### 1. Read Current Tags from GitHub & Determine Next Version

Fetch and list the latest tags from GitHub:

```bash
# Fetch and inspect all tags from origin
git fetch --tags origin
git tag -l --sort=-v:refname
git ls-remote --tags origin
```

### 2. Create & Push New Git Tag

Determine the next Semantic Version (e.g., `v1.0.2`), create the Git tag, and push it to GitHub:

```bash
git tag -a v1.0.1 -m "Release v1.0.1"
git push origin v1.0.1
```

### 3. Build & Push Docker Image

Build the production image for ARM64 and push both the release tag and `latest` to Docker Hub:

```bash
# 1. Build ARM64 binary and Docker image (tagged as latest)
make docker-build

# 2. Tag image with release version
make docker-tag TAG=v1.0.1

# 3. Push release tag and latest to Docker Hub
make docker-push TAG=v1.0.1
make docker-push TAG=latest
```

### 4. Deploy to Kubernetes

Deploy the release to the Kubernetes cluster using Helm:

```bash
# Deploy via root Makefile
make k8s-release TAG=v1.0.1

# Or directly via Helm Makefile in k8s/
make -C k8s deploy TAG=v1.0.1

# Verify rollout status
make -C k8s post-deploy-check
```
### 5. Internal Cluster Services & Monitoring Endpoints
The Kubernetes cluster provides internal monitoring and metrics services accessible to components within the cluster network:

| Service | Internal Cluster Host / Endpoint | Type | Description |
|---|---|---|---|
| **Prometheus** | `monitoring-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090` | Core / Metrics Scraper | Cluster-wide Prometheus server |
| **StatsD** | `graphite.monitoring.svc.cluster.local:8125` | Graphite / StatsD Exporter | UDP StatsD metrics collector |
| **Grafana** | `http://grafana.local` | Ingress / Web UI | Dashboards & visualization interface |
