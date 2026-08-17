# ffxiv-census

Welcome to **ffxiv-census**, a Go service scaffold generated on 2026-08-16T01:14:18+03:00. The codebase embraces a hexagonal (ports & adapters) architecture so business rules stay decoupled from transport and infrastructure concerns. Clone the repository at https://github.com/mihaiflorentin88/ffxiv-census.git.

## Quick start

```bash
go mod tidy
make build
./bin/ffxiv-census server --start --port 8080
```

Key documentation lives under `docs/`:

- `docs/getting-started.md` — setup, commands, and environment notes.
- `docs/architecture.md` — high-level system design and conventions.
- `docs/container.md` — explains the service locator and wiring patterns.
- `docs/sqlite.md` — SQLite storage, runtime migrations, and configuration.
- `docs/queue.md` — the SQLite-backed durable work queue.
- `docs/lodestone.md` — the Lodestone scraper adapter (rate limiting, retries).
- `docs/census.md` — the census domain model, tables, and repositories.
- `docs/events.md` — the event model and ingest pipeline.
- `docs/data-contracts.md` — DTO guidance for ports and adapters.
- `docs/logging-and-middleware.md` — describes the HTTP pipeline and logging modes.
- `docs/ui.md` — notes on the embedded HTMX sample and how to extend it.

## Make targets

- `make build` — compile the CLI into `bin/`.
- `make build-all` — cross-compile for Linux, macOS, and Windows.
- `make docker-build` — build inside Docker and persist artifacts to `dist/`.
- `make fmt` — run `gofmt` on all Go files.
- `make test` — execute the unit test suite.

## CLI Commands

All commands accept `--help`; the binary reports its build with `--version`. Most commands
need a SQLite database: by default `data/ffxiv-census.db`, override with `SQLITE_PATH`.
Migrations run automatically on first database access — no separate step is required.

### `server` — start the web server

```bash
./bin/ffxiv-census server --start --port 8080            # HTTP on :8080 (default pool: 5 workers)
./bin/ffxiv-census server --start --port 8443 --cert-file cert.pem --key-file key.pem  # TLS
./bin/ffxiv-census server --start --port 8080 --profile  # also serve pprof on :6060
./bin/ffxiv-census server --start --port 8080 --shutdown-max-requests 1000  # graceful drain
```

### `migrate` — SQLite schema migrations

```bash
./bin/ffxiv-census migrate --direction up    # apply all pending migrations (default)
./bin/ffxiv-census migrate --direction down  # roll back ALL migrations (destructive)
```

### `publish id-sweep` — enqueue ID-range probes

Probes every character ID in `[from, to]` in chunks; each job is a bounded range.
```bash
# 10 jobs covering IDs 1..1000 (100 IDs per job)
SQLITE_PATH=/tmp/census.db ./bin/ffxiv-census publish id-sweep --from 1 --to 1000 --chunk-size 100

# single-ID jobs — verbose per-job publish logs (dedup: re-running inserts nothing)
LOGGING_LEVEL=debug SQLITE_PATH=/tmp/census.db ./bin/ffxiv-census publish id-sweep --from 1 --to 10 --chunk-size 1
```

### `publish character-census` — re-census stale characters

Enqueues a `character-census` job per character not seen within `--older-than`.
```bash
SQLITE_PATH=/tmp/census.db ./bin/ffxiv-census publish character-census --older-than 720h --limit 1000
```

### `consume <event>` — run a consumer worker (long-running)

Claims and processes jobs of one event type; Ctrl-C shuts down gracefully.
Valid events: `id-sweep`, `character-census`, `achievement-census`, `fc-census` —
see [Events](#events) for what each one does.

```bash
SQLITE_PATH=/tmp/census.db ./bin/ffxiv-census consume id-sweep                 # 4 workers (default)
SQLITE_PATH=/tmp/census.db ./bin/ffxiv-census consume achievement-census --concurrency 1
LOGGING_LEVEL=debug SQLITE_PATH=/tmp/census.db ./bin/ffxiv-census consume fc-census  # verbose worker/queue logs
```

### End-to-end example

```bash
DB=/tmp/census.db
SQLITE_PATH=$DB ./bin/ffxiv-census publish id-sweep --from 1 --to 100 --chunk-size 10   # 10 jobs
SQLITE_PATH=$DB ./bin/ffxiv-census consume id-sweep                                       # process them
```

### Environment overrides

| Variable | Overrides |
|---|---|
| `SQLITE_PATH` | `[sqlite] path` (default `data/ffxiv-census.db`) |
| `LOGGING_LEVEL` | `[logging] level` — `info` (default), `debug`, `warn`, `error` |
| `LODESTONE_RATE_LIMIT` | `[lodestone] rate_limit` (requests/second) |

Queue/worker/handler progress is logged through the process-wide logger: `Info` for
lifecycle and per-job completion, `Warn` for retries, `Error` for terminal failures,
`Debug` for high-frequency detail (opt-in via `LOGGING_LEVEL=debug`). See
`docs/logging-and-middleware.md` and `docs/queue.md` for details.

## Events

The ingest pipeline is event-driven: publishers enqueue jobs (`publish ...`), workers
claim and dispatch them to the matching handler (`consume <event>`), and handlers can
chain downstream jobs. Each job is retried with exponential backoff and fails
permanently after `[queue] max_attempts` (default 5).

| Event | Payload | What it does | Chains |
|---|---|---|---|
| `id-sweep` | `{"from": N, "to": M}` | Probes every character ID in the inclusive range `[from, to]`. Characters that exist on Lodestone are fetched and upserted; IDs with no character are skipped. | `achievement-census` per discovered character |
| `character-census` | `{"character_id": N}` | Re-censuses a known character: fetches the profile and upserts it; if Lodestone reports the character as gone (404), it is marked deleted. | `achievement-census`, plus `fc-census` when the character belongs to a free company |
| `achievement-census` | `{"character_id": N}` | Fetches the character's achievements, records registry milestones, and updates the latest-achievement / private-profile flags. | none (leaf) |
| `fc-census` | `{"fc_id": "..."}` | Fetches a free company profile and upserts its record. | none (leaf) |

Typical flows:

```text
publish id-sweep ──► consume id-sweep ──► achievement-census
publish character-census ──► consume character-census ──► achievement-census
                                                      └──► fc-census
```

`id-sweep` and `character-census` are the entry points published via the CLI;
`achievement-census` and `fc-census` are produced as chained downstream jobs and are
consumed the same way.

Happy hacking!
