# PostgreSQL Migration Design

## Objective
Migrate the `ffxiv-census` application completely from SQLite to PostgreSQL. This change resolves the persistent NFS lock corruption (`database disk image is malformed`) seen when running SQLite in WAL mode on Kubernetes `ReadWriteMany` volumes across multiple pods.

We will remove SQLite entirely to avoid maintaining dual SQL dialects, relying instead on a local Dockerized PostgreSQL instance for development, and a Helm-managed PostgreSQL instance for production.

## 1. Codebase & Persistence Migration

- **Remove SQLite**: 
  - Remove the `modernc.org/sqlite` dependency.
  - Delete `infrastructure/sqlite` and move its contents to `infrastructure/postgres`.
- **New Postgres Adapter**:
  - Implement drivers and repositories using `github.com/jackc/pgx/v5` (pgxpool) for high-performance PostgreSQL access.
- **SQL Dialect Translation**:
  - Convert all parameter bindings from `?` to `$1, $2, etc.`
  - Update any SQLite-specific date/time functions or JSON extraction logic to their Postgres equivalents.
  - Translate the `ON CONFLICT(id) DO UPDATE SET` clauses (Postgres supports this, but the syntax requires explicit target constraints, e.g., `ON CONFLICT (id) DO UPDATE SET`).
- **Migrations**:
  - Rewrite all `.sql` files in `infrastructure/postgres/migration/` from SQLite dialect to Postgres dialect. 
  - `goose` will continue to handle migrations on boot.

## 2. Configuration & Local Development

- **Configuration Updates**:
  - Replace the `[sqlite]` block in `config.toml` with a `[postgres]` block.
  - New fields: `host`, `port`, `user`, `password`, `dbname`, `sslmode`.
  - Update `config/config.go` struct mappings accordingly.
- **Makefile Commands**:
  - Add `make postgres`: Starts a local Postgres 16 container via Docker (`docker run --rm -d --name ffxiv-postgres -e POSTGRES_USER=census -e POSTGRES_PASSWORD=secret -e POSTGRES_DB=ffxiv_census -p 5432:5432 postgres:16-alpine`).
  - Add `make postgres-stop`: Stops the local container (`docker stop ffxiv-postgres`).

## 3. Kubernetes Deployment (Helm)

- **Helm Dependency**:
  - Add `bitnami/postgresql` as a dependency to `k8s/Chart.yaml`. This provisions a dedicated StatefulSet and PVC for the database automatically.
- **Values Configuration**:
  - Configure `k8s/values.yaml` with a `postgresql:` block (credentials, storage).
  - Update the `env` blocks for `webserver`, `workers`, and `cronjobs` to inject the new Postgres connection variables instead of `SQLITE_PATH`.

## 4. Backup Strategy (Google Drive)

- **Dockerfile Update**:
  - Add `postgresql-client` to the `apk add` layer in `Dockerfile` to install the `pg_dump` utility.
- **Backup Logic (`cmd/cli/backup.go`)**:
  - Remove the SQLite `VACUUM INTO` command.
  - Execute a local shell command: `pg_dump -d <dsn> -Z 9 -f /tmp/backup.sql.gz`.
  - Pass the resulting `.sql.gz` file to the existing Google Drive uploader logic. 
  - Clean up the local `/tmp/backup.sql.gz` file after upload.