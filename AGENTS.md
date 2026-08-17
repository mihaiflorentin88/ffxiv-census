# AGENTS.md

## Quick Reference

```bash
# Build & run
make build && ./bin/ffxiv-census server --start --port 8080

# Test (strict TDD enforced — red/green/refactor)
make test                    # all tests
go test -v ./path/to/pkg     # single package
go test -run TestName ./pkg  # single test

# Verify before commit
make lint                    # golangci-lint
make fmt                     # gofmt

# Migration (runs automatically on boot, but manual ops available)
./bin/ffxiv-census migrate --direction up|down
```

## Architecture

**Hexagonal (ports & adapters) with service locator:**

- `port/contract` — all interfaces (MySQLDriver, Queue, LodestoneClient, etc.)
- `domain/` — pure business logic, tech-agnostic, never imports infrastructure
- `infrastructure/` — concrete adapters implementing port contracts
- `container/` — service locator with global `container.Load`; lazy accessor pattern
- `cmd/cli` — cobra commands; `cmd/http` — net/http server + middleware

**Key patterns:**

- Service locator resolves adapters via `container.Load.MySQL()`, `container.Load.Queue()`, etc. — not DI
- Adapters implement interfaces from `port/contract`; domain depends only on contracts
- `cmd/` constructs domain objects directly; everything else stays decoupled
- Config embedded via `//go:embed config.toml` + Viper; env overrides: `SQLITE_PATH`, `LODESTONE_RATE_LIMIT` (dots → underscores)

## Build Constraints

Cross-compile targets use `CGO_ENABLED=0` (Makefile build-linux-amd64, etc.). **Do not use CGO-dependent libraries** (e.g., mattn/go-sqlite3). Use pure-Go alternatives (modernc.org/sqlite).

## Migrations

**Runtime migrations:** `infrastructure/sqlite` driver runs `goose.Up()` on first `SQLite()` use. Every binary (server, workers) self-migrates at boot. No separate migration step needed.

Manual ops: `./bin/ffxiv-census migrate --direction down` rolls back all migrations (destructive).

## Testing Discipline

**Strict TDD required:** write failing test first, watch it fail, then minimal code to pass. No production code without a failing test. User will enforce this in reviews.

- Every port gets a fake in `mock/` (two adapters per port rule)
- SQLite tests use temp-file DBs (real SQL, not mocks)
- Worker pool tests: `go test -race`
- Table-driven tests for handlers/domain services

## Documentation

`docs/` is living documentation. When adding features, update the relevant doc alongside the code. New docs needed for new subsystems (e.g., `docs/events.md`, `docs/queue.md`).

## Web UI

Server-rendered Go templates + HTMX in `cmd/http/ui/` (embedded via `//go:embed`). No separate frontend build pipeline. Vendored Chart.js for charts, no CDN dependencies.

## Workflow

- **Always prompt before implementation** — user will ask "proceed?" before dispatching implementation subagents
- **Specs/plans live in `docs/superpowers/`** — always create the plan file in `docs/superpowers/plans/` (design spec → implementation plan → code)
- **Commit and push** — always commit all changes (code, tests, plans, specs) and push to remote on completion
- **Commit skills artifacts** (`.agents/`, `.claude/`, `skills-lock.json`) — they're project-local skill installs
## Subagent Delegation

- **Always delegate data-gathering** — file reading, broad codebase exploration, web searches, and MCP queries must be delegated to `scout` / `librarian` subagents running a tiny LM Studio model via `task` to conserve main model tokens.
- **Main model responsibilities** — code edits, mechanical formatting, architectural decisions, planning, implementation, and test suites are handled directly by the main model.
