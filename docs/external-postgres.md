# External PostgreSQL

ffxiv-census uses a standalone PostgreSQL cluster managed by the **pgres** Helm chart. The database runs as a separate CloudNativePG (CNPG) deployment and serves multiple applications.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│  pgres-chart (separate Helm release)                    │
│                                                         │
│  ┌──────────────────────────────────────────────────┐   │
│  │  CloudNativePG Cluster (3 instances)             │   │
│  │  pgres-postgresql-1  (primary, read-write)       │   │
│  │  pgres-postgresql-2  (replica)                   │   │
│  │  pgres-postgresql-3  (replica)                   │   │
│  └──────────────────────────────────────────────────┘   │
│                                                         │
│  Services:                                              │
│    pgres-postgresql-rw  →  primary (ClusterIP)          │
│    pgres-postgresql-ro  →  replicas (ClusterIP)         │
│    pgres-postgresql-external  →  primary (NodePort:30432)│
│                                                         │
│  CronJob: pgres-backup (nightly 1AM, Google Drive)      │
└─────────────────────────────────────────────────────────┘
         ▲                    ▲
         │                    │
    POSTGRES_DSN         POSTGRES_DSN
         │                    │
┌────────┴────────┐  ┌───────┴─────────┐
│  ffxiv-census   │  │  ffxiv-bard     │
│  (webserver,    │  │  (webserver)    │
│   workers,      │  │                 │
│   cronjobs)     │  │                 │
└─────────────────┘  └─────────────────┘
```

## Connection Details

| Parameter | Value |
|---|---|
| Host (in-cluster) | `pgres-postgresql-rw` |
| Host (internal network) | `192.168.50.4` |
| Port (in-cluster) | `5432` |
| Port (internal network) | `30432` |
| Database | `postgres` |
| Owner | `admin` |

Credentials are stored in Vault at `pgres/prod` and synced to Kubernetes via External Secrets Operator.

## How ffxiv-census Connects

The chart uses `externalPostgres` values instead of a bundled PostgreSQL:

```yaml
# k8s/values.yaml
externalPostgres:
  host: pgres-postgresql-rw
  port: "5432"
  database: postgres
```

Workload templates inject these as environment variables:

- `POSTGRES_HOST` — from `externalPostgres.host`
- `POSTGRES_PORT` — from `externalPostgres.port`
- `POSTGRES_DATABASE` — from `externalPostgres.database`
- `POSTGRES_USER` / `POSTGRES_PASSWORD` — from ESO secret (Vault `pgres/prod`)
- `POSTGRES_DSN` — constructed from the above: `postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@$(POSTGRES_HOST):$(POSTGRES_PORT)/$(POSTGRES_DATABASE)?sslmode=$(POSTGRES_SSLMODE)`

The ESO ExternalSecret uses a dual-store pattern:
- **App secrets** (API tokens, etc.) — fetched from `census/prod` via `dataFrom.extract`
- **DB credentials** — fetched from `pgres/prod` via explicit `data` entries with `sourceRef` to the pgres SecretStore

## Backups

Backups are managed by the pgres chart, not ffxiv-census. The backup CronJob runs nightly at 1AM (Europe/Bucharest):

```bash
# Trigger manual backup
kubectl create job --from=cronjob/pgres-backup pgres-backup-manual

# List backups on Google Drive
kubectl run pgres-list --rm -i --restart=Never \
  --image=mihaiflorentin88/pgres:latest \
  --env="BACKUP_GDRIVE_FOLDER_ID=<id>" \
  --env="BACKUP_OAUTH_CLIENT_ID=<id>" \
  --env="BACKUP_OAUTH_CLIENT_SECRET=<secret>" \
  --env="BACKUP_OAUTH_REFRESH_TOKEN=<token>" \
  --command -- /app/pgres list

# Restore latest backup
kubectl run pgres-restore --rm -it --restart=Never \
  --image=mihaiflorentin88/pgres:latest \
  --env-from=secret/pgres-eso-secret \
  --env="POSTGRES_HOST=pgres-postgresql-rw" \
  --env="POSTGRES_PORT=5432" \
  --env="POSTGRES_DATABASE=postgres" \
  --env="POSTGRES_SSLMODE=disable" \
  --command -- /app/pgres restore --latest
```

See the [pgres-chart README](https://github.com/mihaiflorentin88/pgres-chart) for full CLI reference.

## Internal Network Access

The NodePort service exposes PostgreSQL on port **30432** across all cluster nodes:

```bash
# From any machine on the internal network
psql "postgresql://admin:<password>@192.168.50.4:30432/postgres?sslmode=disable"
```

## Vault Configuration

### Secrets at `pgres/prod`

| Key | Description |
|---|---|
| `POSTGRES_USER` | Database owner username |
| `POSTGRES_PASSWORD` | Database owner password |
| `BACKUP_GDRIVE_FOLDER_ID` | Google Drive folder for backups |
| `BACKUP_OAUTH_CLIENT_ID` | OAuth2 client ID |
| `BACKUP_OAUTH_CLIENT_SECRET` | OAuth2 client secret |
| `BACKUP_OAUTH_REFRESH_TOKEN` | OAuth2 refresh token |

### Vault Policy

The `pgres` policy grants read access to the `pgres` KV v2 mount:

```hcl
path "pgres/*" {
  capabilities = ["read"]
}
```

This policy is attached to the `default-namespace` role alongside `census` and `ffxiv-bard`.

## Helm Values Reference

| Key | Description | Default |
|---|---|---|
| `namespace` | Kubernetes namespace | `default` |
| `image.repository` | Docker image | `mihaiflorentin88/pgres` |
| `image.tag` | Image tag | `latest` |
| `postgresql.instances` | CNPG instances | `3` |
| `postgresql.storageClass` | Storage class | `local-path` |
| `postgresql.storageSize` | PVC size | `20Gi` |
| `postgresql.database` | Database name | `postgres` |
| `postgresql.owner` | Owner role | `admin` |
| `postgresql.nodePort` | External port | `30432` |
| `backup.schedule` | Cron schedule | `0 1 * * *` |
| `backup.maxBackups` | Retention count | `10` |
| `vault.fetchAllDataFrom` | Vault path | `pgres/prod` |
