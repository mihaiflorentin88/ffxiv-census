# PostgreSQL Migration Implementation Plan

## Goal
Migrate `ffxiv-census` from SQLite to PostgreSQL to resolve NFS lock corruption. Ensure `ffxivbard` remains untouched.

## Phase 1: Preparation & Infrastructure
- [ ] **WARNING**: Verify that all Helm, configuration, and code changes are strictly scoped to `ffxiv-census` and that the `ffxivbard` application is isolated and untouched.
- [ ] Create `infrastructure/postgres` directory.
- [ ] Setup `pgx/v5` connection pooling infrastructure in `infrastructure/postgres/repository`.

## Phase 2: Core Codebase & Dialect Migration
- [ ] Remove `modernc.org/sqlite` dependency.
- [ ] Delete `infrastructure/sqlite` content.
- [ ] Translate all repository queries from SQLite to PostgreSQL syntax:
    - Replace `?` with `$1, $2, etc.` bindings.
    - Adapt date/time/JSON extraction to Postgres equivalents.
    - Fix `ON CONFLICT` syntax.
- [ ] Translate all migration `.sql` files in `infrastructure/postgres/migration/` from SQLite to PostgreSQL dialect.

## Phase 3: Configuration & Local Development
- [ ] Update `config.toml`: Replace `[sqlite]` with `[postgres]` block (`host`, `port`, `user`, `password`, `dbname`, `sslmode`).
- [ ] Update `config/config.go` to support new Postgres configuration struct.
- [ ] Update `Makefile`: Add `make postgres` (starts Postgres 16 container) and `make postgres-stop`.

## Phase 4: Kubernetes Updates
- [ ] Update `k8s/Chart.yaml`: Add `bitnami/postgresql` as a dependency.
- [ ] Update `k8s/values.yaml`: Add `postgresql` configuration block.
- [ ] Update Helm `env` blocks for `webserver`, `workers`, and `cronjobs` to inject Postgres DSN instead of `SQLITE_PATH`.

## Phase 5: Backup & Deployment Updates
- [ ] Update `Dockerfile`: Add `postgresql-client` (for `pg_dump`).
- [ ] Update `cmd/cli/backup.go`:
    - Replace SQLite `VACUUM INTO` with `pg_dump`.
    - Handle `.sql.gz` cleanup.

## Phase 6: Verification
- [ ] Verify `ffxivbard` resources are completely untouched.
- [ ] Run unit tests for Postgres adapter.
- [ ] Run `make postgres` and verify connectivity.
- [ ] Run full test suite and linting.
- [ ] Validate Helm deployment (dry-run).
