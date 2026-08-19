# Plan: Extract PostgreSQL into Standalone Helm Chart

## Context

The ffxiv-census Helm chart previously bundled its own CloudNativePG (CNPG) PostgreSQL cluster, ESO secrets, and Google Drive backup CronJob. This coupling meant the database could only serve one application. Extracting PostgreSQL into a standalone chart (`pgres-chart`) allows the database to serve multiple applications (ffxiv-census, ffxiv-bard, future services) with shared HA infrastructure and centralized backup management.

The standalone chart lives at `/home/mihai/Workspace/pgres-chart` and is published as `github.com/mihaiflorentin88/pgres-chart`.

## What Changed

### New: pgres-chart (standalone PostgreSQL)

A complete Helm chart + Go backup tool providing:

- **CloudNativePG 3-instance HA cluster** (`pgres-postgresql`) with streaming replication and automatic failover
- **Vault/ESO integration** — secrets synced from `pgres/prod` in Vault
- **Automated nightly backups** — CronJob at 1AM (Europe/Bucharest) using `pg_dump` + Google Drive upload
- **Count-based rotation** — default 10 backups retained, oldest deleted automatically
- **Restore tool** — `pgres restore --latest` downloads and pipes through `zcat | psql`
- **NodePort service** — `pgres-postgresql-external` on port 30432 for internal network access
- **Structured JSON logging** via `slog` with configurable `--log-level`

### Modified: ffxiv-census chart

Removed all PostgreSQL infrastructure from the census chart:

| Removed | Replaced By |
|---|---|
| `templates/postgres-cluster.yaml` | pgres-chart CNPG Cluster |
| `templates/postgres-credentials.yaml` | ESO secret from `pgres/prod` |
| `postgresql:` values block | `externalPostgres:` config |
| Backup cronjob | pgres-chart CronJob |
| `pg-credentials` secretRef in workloads | ESO secret (`ffxiv-census-eso-secret`) |

New `externalPostgres` config in `values.yaml`:

```yaml
externalPostgres:
  host: pgres-postgresql-rw
  port: "5432"
  database: postgres
```

### Modified: Vault

- **New policy** `pgres` created: `path "pgres/*" { capabilities = ["read"] }`
- **Updated role** `default-namespace`: policies now include `census`, `ffxiv-bard`, `pgres`
- **ExternalSecret** uses dual-store pattern:
  - App secrets from `census/prod` (via `dataFrom.extract`)
  - DB credentials from `pgres/prod` (via explicit `data` entries with `sourceRef` to pgres SecretStore)

## Approach

### Phase 1: Uninstall ffxiv-census

1. `helm uninstall ffxiv-census` — removed CNPG cluster, PVCs, deployments, cronjobs, ESO resources
2. Cleaned up orphaned ESO resources (ExternalSecret, SecretStore) that survived due to hook annotations
3. Verified namespace clean of ffxiv-census resources

### Phase 2: Create pgres-chart scaffolding

Created at `/home/mihai/Workspace/pgres-chart/`:

```
├── Chart.yaml              # apiVersion: v2, name: pgres, appVersion: 16.8
├── values.yaml             # namespace, image, postgresql, vault, backup config
├── .helmignore             # excludes Go source, Dockerfile, Makefile
├── Makefile                # deploy and build-image targets
├── Dockerfile              # Alpine 3.22 + postgresql-client + Go binary (ARM64)
├── go.mod / go.sum         # github.com/mihaiflorentin88/pgres
├── cmd/main.go             # cobra: backup, restore, list, auth
├── internal/
│   ├── backup/             # backup.go, restore.go, list.go, auth.go, gdrive.go
│   ├── config/config.go    # env-based config
│   └── logger/logger.go    # slog JSON handler
├── templates/
│   ├── _helpers.tpl         # generic Helm helpers (copied from ffxiv-census)
│   ├── vault.yaml           # ESO SecretStore + ExternalSecret (pgres/prod)
│   ├── postgres-cluster.yaml # CNPG Cluster CRD (3 instances)
│   ├── backup-cronjob.yaml  # Nightly backup CronJob
│   ├── serviceaccount.yaml  # pgres-sa
│   ├── postgres-external-svc.yaml # NodePort 30432
│   └── NOTES.txt
└── README.md               # CLI reference, Helm values, release workflow
```

### Phase 3: Helm templates

- **vault.yaml** — ESO SecretStore + ExternalSecret with explicit `data` mapping for CNPG bootstrap keys (`username`, `password`) and app/backup keys from `pgres/prod`
- **postgres-cluster.yaml** — CNPG Cluster with configurable instances, storage, parameters
- **backup-cronjob.yaml** — CronJob running `pgres backup --max-backups 10`
- **postgres-external-svc.yaml** — NodePort service for internal network access

### Phase 4: Go backup tool

Standalone binary with 4 commands:

| Command | Description |
|---|---|
| `pgres backup --max-backups N` | pg_dump → upload → rotation |
| `pgres restore --latest` / `--file` / interactive | download → `zcat \| psql` |
| `pgres list` | list backups on Google Drive |
| `pgres auth --client-secret-file <path>` | OAuth2 refresh token generator |

Key implementation details:
- Uses `zcat` pipe for restore (psql can't read .gz files directly)
- File names use `filepath.Base()` to avoid full temp paths in Drive
- Structured JSON logging via `slog` with `--log-level` flag
- Cross-compiled for ARM64 (Raspberry Pi cluster)

### Phase 5: Deploy pgres-chart

1. Built and pushed ARM64 Docker image to `mihaiflorentin88/pgres:v0.1.3`
2. Deployed Helm chart — CNPG bootstrapped with credentials from Vault
3. Added `pgres` policy to Vault and updated `default-namespace` role
4. ESO synced `pgres-eso-secret` with all required keys
5. Verified: 3/3 instances healthy, backup uploads to Drive, restore recovers dropped tables

### Phase 6: Update ffxiv-census chart

1. Removed `postgres-cluster.yaml` and `postgres-credentials.yaml`
2. Removed `postgresql:` block, added `externalPostgres:` config
3. Removed backup cronjob (handled by pgres-chart)
4. Updated workload templates to use `externalPostgres` values
5. Updated vault.yaml with dual-store pattern (census/prod + pgres/prod)
6. Deployed — all pods running, health checks passing

## Critical Files

| File | Action | Purpose |
|---|---|---|
| `pgres-chart/templates/postgres-cluster.yaml` | New | CNPG 3-instance HA cluster |
| `pgres-chart/templates/vault.yaml` | New | ESO SecretStore + ExternalSecret |
| `pgres-chart/templates/backup-cronjob.yaml` | New | Nightly backup CronJob |
| `pgres-chart/cmd/main.go` | New | Cobra CLI (backup/restore/list/auth) |
| `pgres-chart/internal/backup/backup.go` | New | pg_dump + upload + rotation |
| `pgres-chart/internal/backup/restore.go` | New | download + zcat \| psql |
| `ffxiv-census/k8s/templates/postgres-cluster.yaml` | Deleted | Replaced by pgres-chart |
| `ffxiv-census/k8s/templates/postgres-credentials.yaml` | Deleted | Replaced by ESO secret |
| `ffxiv-census/k8s/values.yaml` | Modified | `externalPostgres` config |
| `ffxiv-census/k8s/templates/vault.yaml` | Modified | Dual-store ESO pattern |
| `ffxiv-census/k8s/templates/webserver.yaml` | Modified | Uses externalPostgres |
| `ffxiv-census/k8s/templates/workers.yaml` | Modified | Uses externalPostgres |
| `ffxiv-census/k8s/templates/cronjobs.yaml` | Modified | Uses externalPostgres |

## Verification

| Check | Command | Expected |
|---|---|---|
| CNPG cluster healthy | `kubectl get clusters` | 3/3 ready |
| Backup uploads | `kubectl create job --from=cronjob/pgres-backup test` | `Backup uploaded: pgres_backup_*.sql.gz` |
| Restore works | Drop table → `pgres restore --latest` → query | Data recovered |
| Census health | `kubectl logs deploy/ffxiv-census-www` | 200 OK on /health |
| Census connects to pgres | Check POSTGRES_DSN env in pod | Points to pgres-postgresql-rw |
| External access | `psql -h 192.168.50.4 -p 30432 -U admin -d postgres` | Connected |

## Assumptions

- **Vault `pgres/prod`** contains `POSTGRES_USER`, `POSTGRES_PASSWORD`, `BACKUP_GDRIVE_FOLDER_ID`, `BACKUP_OAUTH_CLIENT_ID`, `BACKUP_OAUTH_CLIENT_SECRET`, `BACKUP_OAUTH_REFRESH_TOKEN`
- **CNPG and ESO operators** already installed cluster-wide
- **ARM64 cluster** — Docker images cross-compiled with `GOARCH=arm64` and `alpine:3.22` base
- **Single PostgreSQL for all apps** — ffxiv-census and future services share the same cluster, using the `postgres` database
- **Backup rotation is count-based** (default 10), not age-based
- **NodePort 30432** for internal network access — no TLS (internal network only)
