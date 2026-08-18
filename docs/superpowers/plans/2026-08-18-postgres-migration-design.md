# PostgreSQL Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate `ffxiv-census` completely from SQLite to PostgreSQL to resolve NFS lock corruption. Ensure `ffxivbard` remains absolutely untouched.

**Tech Stack:** Go, pgx/v5, PostgreSQL, Kubernetes (Helm).

**Spec:** `docs/superpowers/specs/2026-08-18-postgres-migration-design.md`

## Global Constraints
- **CRITICAL WARNING**: Verify that all Helm, configuration, and code changes are strictly scoped to `ffxiv-census`. The `ffxivbard` application MUST remain isolated and untouched.

---

### Task 1: Setup Infrastructure & Dependencies

**Files:**
- Modify: `go.mod`, `go.sum`, `config.toml`, `config/config.go`
- Create: `infrastructure/postgres/`

- [ ] **Step 1:** Run `go get github.com/jackc/pgx/v5/pgxpool` and remove `modernc.org/sqlite`.
- [ ] **Step 2:** Update `config.toml` by replacing `[sqlite]` with a `[postgres]` block containing `dsn = "postgres://census:secret@localhost:5432/ffxiv_census?sslmode=disable"`.
- [ ] **Step 3:** Update `config/config.go` to parse a `PostgresConfig` struct instead of `SQLiteConfig`.

### Task 2: Implement Postgres Driver & Migrations

**Files:**
- Create: `infrastructure/postgres/driver.go`
- Move & Modify: `infrastructure/sqlite/migration/*.sql` -> `infrastructure/postgres/migration/*.sql`

- [ ] **Step 1:** Create `Driver` in `infrastructure/postgres/driver.go` that initializes a `pgxpool.Pool` and implements a `contract.DatabaseDriver` (replacing `contract.SQLiteDriver`).
- [ ] **Step 2:** Translate all `goose` `.sql` migration files to PostgreSQL syntax (e.g., `AUTOINCREMENT` to `SERIAL` or `BIGSERIAL`, `DATETIME` to `TIMESTAMP WITH TIME ZONE`).
- [ ] **Step 3:** Implement `MigrateUp` and `MigrateDown` in the Postgres driver using `goose.SetDialect("postgres")`.

### Task 3: Migrate Repositories & Queue to Postgres Syntax

**Files:**
- Move & Modify: `infrastructure/sqlite/repository/*.go` -> `infrastructure/postgres/repository/*.go`
- Move & Modify: `infrastructure/sqlite/queue.go` -> `infrastructure/postgres/queue.go`

- [ ] **Step 1:** Update all SQL queries in the repositories and queue:
  - Replace `?` bindings with `$1`, `$2`, etc.
  - Update `ON CONFLICT (id) DO UPDATE SET` clauses to explicitly name the conflict target if required by Postgres syntax.
- [ ] **Step 2:** Ensure JSON extraction logic and date/time formatting aligns with PostgreSQL (e.g., handling Go `time.Time` natively with `pgx`).

### Task 4: Update Dependency Injection & Contracts

**Files:**
- Modify: `container/infrastructure.go`, `container/main.go`, `port/contract/sqlite.go` (Rename to `database.go`)

- [ ] **Step 1:** Rename `contract.SQLiteDriver` to `contract.DatabaseDriver`.
- [ ] **Step 2:** Update `container/infrastructure.go` to instantiate and inject the new `postgres.Driver` instead of SQLite.

### Task 5: Backup Strategy & Dockerfile

**Files:**
- Modify: `Dockerfile`, `cmd/cli/backup.go`

- [ ] **Step 1:** Update `Dockerfile` to `apk add --no-cache postgresql-client`.
- [ ] **Step 2:** Rewrite `cmd/cli/backup.go` to use `exec.Command("pg_dump", "-d", dsn, "-Z", "9", "-f", "/tmp/backup.sql.gz")` instead of SQLite `VACUUM INTO`. Upload `/tmp/backup.sql.gz` to Google Drive and delete the local temp file.

### Task 6: Makefile Local Development

**Files:**
- Modify: `Makefile`

- [ ] **Step 1:** Add `make postgres` command: `docker run --rm -d --name ffxiv-postgres -e POSTGRES_USER=census -e POSTGRES_PASSWORD=secret -e POSTGRES_DB=ffxiv_census -p 5432:5432 postgres:16-alpine`.
- [ ] **Step 2:** Add `make postgres-stop` command: `docker stop ffxiv-postgres`.

### Task 7: Kubernetes Helm Updates

**Files:**
- Modify: `k8s/Chart.yaml`, `k8s/values.yaml`, `k8s/templates/webserver.yaml`, `k8s/templates/workers.yaml`, `k8s/templates/cronjobs.yaml`

- [ ] **Step 1:** Add `bitnami/postgresql` dependency to `k8s/Chart.yaml`.
- [ ] **Step 2:** Add a `postgresql:` configuration block in `k8s/values.yaml`.
- [ ] **Step 3:** Replace `SQLITE_PATH` with `POSTGRES_DSN` in the `env` blocks for webserver, workers, and cronjobs.
- [ ] **Step 4:** Ensure all Helm modifications exclusively target `ffxiv-census` components, leaving `ffxivbard` untouched.

### Task 8: Verification

- [ ] **Step 1:** Run `make postgres` and verify local container starts.
- [ ] **Step 2:** Run full test suite `make test` and ensure all tests pass against the new Postgres driver.
- [ ] **Step 3:** Run `make lint`.
- [ ] **Step 4:** Double-check Kubernetes manifests with `helm template` to ensure `ffxivbard` is not affected.