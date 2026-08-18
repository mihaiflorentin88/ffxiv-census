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

#### `publish id-sweep`
Probes character ID ranges across Lodestone or Tomestone. Uses chunking and infinite retries (`max_attempts = 0`).
```bash
# Probe character IDs 1 to 10,000 in chunks of 100
./bin/ffxiv-census publish id-sweep --from 1 --to 10000 --chunk-size 100

# Sweep using Tomestone API instead of Lodestone
./bin/ffxiv-census publish id-sweep --from 1 --to 5000 --source tomestone
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
```

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

### 6. `backup` — SQLite Snapshot & Google Drive Backup

Performs a point-in-time `VACUUM INTO` snapshot of the SQLite database and saves locally or uploads to Google Drive.

```bash
# Create local backup in ./backups with 14-day retention rotation
./bin/ffxiv-census backup --target local --output /var/backups/census --retention-days 14

# Upload snapshot to Google Drive via Service Account key file
./bin/ffxiv-census backup \
  --target gdrive \
  --gdrive-folder-id "1abc123XYZ..." \
  --service-account-file "/secrets/gdrive-service-account.json"

# Upload using Base64-encoded Service Account string (e.g. in crontab or Docker env)
./bin/ffxiv-census backup \
  --target gdrive \
  --gdrive-folder-id "1abc123XYZ..." \
  --service-account-b64 "$GDRIVE_SERVICE_ACCOUNT_B64"
```

### 7. `migrate` — Manual Schema Migrations

Manage database schema versions manually with Goose.

```bash
# Run all pending migrations (default)
./bin/ffxiv-census migrate --direction up

# Roll back all migrations (destructive)
./bin/ffxiv-census migrate --direction down
```

### 8. `tomestone` — Direct Character Inspection

Queries Tomestone.gg directly to inspect character profiles and verify API keys.

```bash
# Inspect character by numeric Lodestone ID
./bin/ffxiv-census tomestone --id 12345678

# Inspect character by server and character name
./bin/ffxiv-census tomestone --server Ragnarok --name "Firstname Lastname"
```

---

## Configuration & Environment Variables

`ffxiv-census` reads `config.toml` by default and supports overriding any setting via uppercase environment variables (replacing dots with underscores).

| Environment Variable | Description | Default |
|---|---|---|
| `SQLITE_PATH` | Path to SQLite database file | `data/ffxiv-census.db` |
| `LOGGING_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `LODESTONE_RATE_LIMIT` | Lodestone scraper rate limit (requests/sec, max 1.0) | `1.0` |
| `TOMESTONE_API_TOKEN` | Bearer API token for Tomestone.gg | `""` |
| `TOMESTONE_RATE_LIMIT` | Tomestone API rate limit (requests/sec, max 20.0) | `10.0` |
| `QUEUE_CLAIM_BATCH_SIZE` | Default number of jobs claimed per batch | `4` |
| `QUEUE_MAX_ATTEMPTS` | Default retry attempts before dead-lettering (0 = infinite) | `5` |
| `QUEUE_BACKOFF_BASE_SECONDS` | Initial backoff delay for retried jobs | `5` |
| `GDRIVE_FOLDER_ID` | Google Drive folder ID for automated backups | `""` |
| `GDRIVE_SERVICE_ACCOUNT_B64` | Base64-encoded Google service account JSON | `""` |
| `GOOGLE_APPLICATION_CREDENTIALS` | Path to Google service account credentials file | `""` |

---

## Architecture & Event Pipeline

The ingest pipeline is fully event-driven:

```text
publish id-sweep ──────────► consume (all queues) ──► achievement-census
publish character-census ──► consume (all queues) ──► achievement-census
                                                  └──► fc-census
```

| Event | Description | Downstream Chains |
|---|---|---|
| `id-sweep` | Probes ID ranges on Lodestone or Tomestone. Non-existent IDs are skipped. | `achievement-census` per discovered character |
| `character-census` | Fetches full profile; marks character deleted if HTTP 404. | `achievement-census`, `fc-census` |
| `achievement-census` | Fetches character achievements and updates registry milestones. | *None (leaf)* |
| `fc-census` | Fetches Free Company profile and roster details. | *None (leaf)* |

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
