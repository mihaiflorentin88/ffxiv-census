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

- `./bin/ffxiv-census server --start --port 8080` — start the HTTP server (migrations run automatically).
- `./bin/ffxiv-census migrate --direction up` — apply all pending SQLite migrations.
- `./bin/ffxiv-census migrate --direction down` — roll back all migrations (destructive).

Happy hacking!
