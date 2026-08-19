# Plan: Replace Split-Brain PostgreSQL with CloudNativePG HA Cluster

## Context

Production queue endpoint (`GET /api/v1/queue`) returns different counts on every request. Root cause: `k8s/templates/postgres.yaml` deploys a **vanilla `postgres:16-alpine` StatefulSet with 2 replicas and zero replication**. Each pod has its own independent `ReadWriteOnce` PVC. The ClusterIP Service load-balances across both pods — every request is a coin flip between two completely separate databases.

Fix: replace the hand-rolled StatefulSet with **CloudNativePG (CNPG)** — the CNCF-incubated Kubernetes operator for PostgreSQL. CNPG provides streaming replication, automatic failover, and managed services out of the box. Old data is intentionally discarded (split-brain divergence).

## Approach

### Step 1: Scale down stateful workloads

Before touching the database, stop all traffic to Postgres:

```bash
kubectl scale deployment ffxiv-census-www --replicas=0
kubectl scale deployment ffxiv-census-worker-consumer --replicas=0
kubectl patch cronjob ffxiv-census-cron-publish-character -p '{"spec":{"suspend":true}}'
kubectl patch cronjob ffxiv-census-cron-publish-id-sweep -p '{"spec":{"suspend":true}}'
kubectl patch cronjob ffxiv-census-cron-publish-fc-census -p '{"spec":{"suspend":true}}'
```

### Step 2: Install the CNPG operator (separate Helm release)

The CNPG operator is installed as its own Helm chart, not as a chart dependency:

```bash
helm repo add cnpg https://cloudnative-pg.github.io/charts
helm upgrade --install cnpg cnpg/cloudnative-pg \
  --namespace cnpg-system --create-namespace
```

Verify: `kubectl get pods -n cnpg-system` — expect `cnpg-controller-manager` running.

### Step 3: Delete hand-rolled postgres.yaml

**File:** `k8s/templates/postgres.yaml` — **DELETE entirely**

The CNPG `Cluster` CRD replaces this. The hand-rolled StatefulSet would conflict.

### Step 4: Create CNPG Cluster resource

**New file:** `k8s/templates/postgres-cluster.yaml`

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: {{ include "manifest.fullname" . }}-postgresql
  namespace: {{ .Values.namespace }}
  labels:
    {{- include "manifest.labels" . | nindent 4 }}
    app.kubernetes.io/component: postgresql
spec:
  imageName: ghcr.io/cloudnative-pg/postgresql:16.8
  instances: 3

  bootstrap:
    initdb:
      database: ffxiv_census
      owner: census
      secret:
        name: {{ include "manifest.fullname" . }}-pg-credentials

  storage:
    size: 20Gi
    storageClass: {{ .Values.postgresql.primary.persistence.storageClass | default "local-path" }}

  resources:
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: "1"
      memory: 1Gi

  affinity:
    enablePodAntiAffinity: true
    topologyKey: kubernetes.io/hostname
    podAntiAffinityType: preferred

  postgresql:
    parameters:
      max_connections: "100"
      shared_buffers: "128MB"

  monitoring:
    enablePodMonitor: {{ .Values.serviceMonitor.enabled | default false }}
```

### Step 5: Create bootstrap credentials secret

**New file:** `k8s/templates/postgres-credentials.yaml`

CNPG's `initdb.secret` expects a secret with `username` and `password` keys. But the app reads `POSTGRES_*` env vars (via Viper `AutomaticEnv()` — see `config/config.go:161`). Create one secret that satisfies both:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: {{ include "manifest.fullname" . }}-pg-credentials
  namespace: {{ .Values.namespace }}
  labels:
    {{- include "manifest.labels" . | nindent 4 }}
type: Opaque
stringData:
  # CNPG bootstrap keys
  username: census
  password: "CHANGEME"  # replaced by Vault/ESO in production
  # App env vars (Viper reads these)
  POSTGRES_USER: census
  POSTGRES_PASSWORD: "CHANGEME"
  POSTGRES_DATABASE: ffxiv_census
  POSTGRES_SSLMODE: disable
```

Post-boot, the `POSTGRES_DSN` and `POSTGRES_HOST` env vars will be injected via the CNPG app secret (Step 6).

### Step 6: Wire app pods to CNPG services

CNPG auto-creates a secret `<cluster-name>-app` with keys: `host`, `port`, `username`, `password`, `dbname`, `uri`, `jdbc-uri`. It also creates services:
- `<cluster-name>-rw` — primary (read/write)
- `<cluster-name>-ro` — replicas (read-only)
- `<cluster-name>-r` — any instance

**Edit:** `k8s/templates/webserver.yaml`, `k8s/templates/workers.yaml`, `k8s/templates/cronjobs.yaml`

Add a second `envFrom` to each container spec, after the existing ESO secret ref:

```yaml
envFrom:
  - secretRef:
      name: {{ $fullName }}-eso-secret
  - secretRef:
      name: {{ $fullName }}-postgresql-app
```

The CNPG `-app` secret injects `host`, `port`, `username`, `password`, `dbname`, `uri`. The app's Viper config reads `POSTGRES_DSN` (for the full DSN) and individual `POSTGRES_HOST`, `POSTGRES_USER`, etc.

**Problem:** CNPG's secret uses lowercase keys (`host`, `password`), but the app expects `POSTGRES_*` prefixed keys. To bridge this, add an explicit `env` block mapping CNPG secret values to the app's expected keys:

```yaml
env:
  - name: POSTGRES_HOST
    valueFrom:
      secretKeyRef:
        name: {{ $fullName }}-postgresql-app
        key: host
  - name: POSTGRES_PORT
    valueFrom:
      secretKeyRef:
        name: {{ $fullName }}-postgresql-app
        key: port
  - name: POSTGRES_USER
    valueFrom:
      secretKeyRef:
        name: {{ $fullName }}-postgresql-app
        key: username
  - name: POSTGRES_PASSWORD
    valueFrom:
      secretKeyRef:
        name: {{ $fullName }}-postgresql-app
        key: password
  - name: POSTGRES_DATABASE
    valueFrom:
      secretKeyRef:
        name: {{ $fullName }}-postgresql-app
        key: dbname
```

Keep the existing ESO `envFrom` for non-postgres secrets (auth tokens, etc.). The explicit `env` entries take precedence for postgres keys.

### Step 7: Remove postgresql block from values.yaml

**File:** `k8s/values.yaml`

Remove the entire `postgresql:` block (~lines 36-60). Replace with minimal CNPG config:

```yaml
postgresql:
  instances: 3
  storageClass: local-path
  storageSize: 20Gi
```

These values are referenced by `postgres-cluster.yaml` only.

### Step 8: Deploy

```bash
cd k8s && make deploy TAG=<current-version>
```

The app pods will crash-loop until the CNPG cluster is ready (expected: ~30s for primary, ~60s for replicas). Migrations run automatically on boot via goose (`infrastructure/postgres/driver.go`).

### Step 9: Verify

1. **CNPG cluster healthy:**
   ```bash
   kubectl get clusters -n default
   kubectl get pods -l cnpg.io/cluster=ffxiv-census-postgresql
   ```
   Expect 3 pods: `ffxiv-census-postgresql-1` (primary), `-2`, `-3` (replicas).

2. **Schema created:**
   ```bash
   kubectl exec ffxiv-census-postgresql-1 -- psql -U census -d ffxiv_census -c '\dt'
   ```

3. **Queue endpoint consistent** (10 identical calls):
   ```bash
   for i in (seq 1 10)
     curl -s 'https://census.ffxivbard.com/api/v1/queue' \
       -H 'accept: application/json' \
       -H 'Authorization: 09136457d0ad6ba4653db4ab5cf00c454b799fbee4b926df8d994a0361040df0' | jq '.total'
   end
   ```

4. **Replication lag < 1s:**
   ```bash
   kubectl exec ffxiv-census-postgresql-2 -- psql -U census -d ffxiv_census \
     -c "SELECT now() - pg_last_xact_replay_timestamp() AS lag;"
   ```

### Step 10: Destroy old PostgreSQL data

Once Step 9 confirms CNPG is healthy and in sync:

```bash
kubectl delete statefulset ffxiv-census-postgresql --ignore-not-found
kubectl delete svc ffxiv-census-postgresql --ignore-not-found
kubectl delete pvc data-ffxiv-census-postgresql-0 data-ffxiv-census-postgresql-1 --ignore-not-found
```

### Step 11: Redeploy CNPG cluster fresh

Destroy the CNPG cluster and its PVCs to start with a completely clean database (no test data from verification):

```bash
kubectl delete cluster ffxiv-census-postgresql
# CNPG finalizers will clean up StatefulSets; wait for PVCs to be released
kubectl delete pvc -l cnpg.io/cluster=ffxiv-census-postgresql --ignore-not-found
```

Re-deploy the Helm chart — CNPG will bootstrap a fresh cluster from scratch:

```bash
cd k8s && make deploy TAG=<current-version>
```

Verify the new cluster is healthy:

```bash
kubectl get clusters
kubectl get pods -l cnpg.io/cluster=ffxiv-census-postgresql
```

Expect 3 pods. Migrations run automatically on boot (goose Up in `infrastructure/postgres/driver.go`). Queue endpoint should return zero counts.

### Step 12: Verify backup to Google Drive

The backup cronjob runs `ffxiv-census backup --target gdrive` (see `k8s/values.yaml` cronjobs.instances). It uses `pg_dump` (installed in the image via `postgresql-client` — see `Dockerfile`) and uploads to Google Drive via service account credentials from Vault.

Trigger a manual backup job to verify the full pipeline works with the new CNPG database:

```bash
kubectl create job --from=cronjob/ffxiv-census-cron-backup ffxiv-census-backup-test
```

Wait for completion and check logs:

```bash
kubectl wait --for=condition=complete job/ffxiv-census-backup-test --timeout=120s
kubectl logs job/ffxiv-census-backup-test
```

Expected output: `Backup completed successfully: <path>`.

If the job fails, check:
1. `pg_dump` can reach the CNPG primary: `kubectl exec ffxiv-census-postgresql-1 -- psql -U census -d ffxiv_census -c 'SELECT 1'`
2. Google Drive credentials are present in the ESO secret: `kubectl get secret ffxiv-census-eso-secret -o jsonpath='{.data}' | jq keys`
3. The backup cronjob has the CNPG app secret wired (Step 6 env wiring applies to cronjobs too)

Clean up the test job:

```bash
kubectl delete job ffxiv-census-backup-test
```

### Step 13: Scale everything back up

```bash
kubectl scale deployment ffxiv-census-www --replicas=2
kubectl scale deployment ffxiv-census-worker-consumer --replicas=1
kubectl patch cronjob ffxiv-census-cron-publish-character -p '{"spec":{"suspend":false}}'
kubectl patch cronjob ffxiv-census-cron-publish-id-sweep -p '{"spec":{"suspend":false}}'
kubectl patch cronjob ffxiv-census-cron-publish-fc-census -p '{"spec":{"suspend":false}}'
```

### Step 14: Update Vault secret

Remove `POSTGRES_DSN`, `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DATABASE` from Vault at `census/prod`. These are now managed by CNPG's generated secret. Keep all non-postgres keys (auth tokens, Google Drive credentials, etc.) in Vault.

### Step 15: Final consistency verification

Run queue endpoint 10 times — counts must be identical:

```bash
for i in (seq 1 10)
  curl -s 'https://census.ffxivbard.com/api/v1/queue' \
    -H 'accept: application/json' \
    -H 'Authorization: 09136457d0ad6ba4653db4ab5cf00c454b799fbee4b926df8d994a0361040df0' | jq '.total'
end
```

Check replication lag:

```bash
kubectl exec ffxiv-census-postgresql-2 -- psql -U census -d ffxiv_census \
  -c "SELECT now() - pg_last_xact_replay_timestamp() AS lag;"
```

### Step 16: Commit plan to repo

Copy this plan file to `docs/superpowers/plans/2026-08-19-cloudnativepg-ha-postgres.md` so it's tracked in the repository alongside other specs and plans. Commit and push all changes (code, templates, plan file) together.

## Critical Files

| File | Action | Why |
|---|---|---|
| `k8s/templates/postgres.yaml` | **Delete** | Replaced by CNPG Cluster CRD |
| `k8s/templates/postgres-cluster.yaml` | **New** | CNPG Cluster resource (3 instances, HA) |
| `k8s/templates/postgres-credentials.yaml` | **New** | Bootstrap secret for CNPG + app env vars |
| `k8s/templates/webserver.yaml` | **Edit** | Add `envFrom` for CNPG app secret + `env` mappings |
| `k8s/templates/workers.yaml` | **Edit** | Same env wiring as webserver |
| `k8s/templates/cronjobs.yaml` | **Edit** | Same env wiring as webserver |
| `k8s/values.yaml` | **Edit** | Replace postgresql block with CNPG config |
| `config/config.go` | No change | `GetDSN()` works with any host; Viper reads `POSTGRES_*` envs |
| `infrastructure/postgres/driver.go` | No change | Migrations auto-run on boot |

## Assumptions

- **CNPG operator installed globally:** Step 2 installs the operator in `cnpg-system` namespace. This is a one-time cluster-wide operation. If the operator is already installed, skip Step 2.
- **Storage class:** Uses `local-path`. If unavailable, CNPG will fail to provision PVCs — fall back to the cluster's default StorageClass.
- **3 instances for quorum:** CNPG requires odd-numbered instances for Raft consensus. 3 is the production minimum. If only 2 nodes are available, use `instances: 2` with `primaryUpdateStrategy: unsupervised` (degraded HA).
- **Clean slate:** Old data intentionally discarded. No data migration from old pods. The fresh redeploy (Step 11) ensures no test data leaks into production.
- **Backup credentials in Vault:** Google Drive service account / OAuth2 credentials remain in Vault at `census/prod`. Only postgres-specific keys are removed in Step 14. If the backup cronjob fails after CNPG migration, verify the ESO secret still contains `GDRIVE_*` / `OAUTH_*` keys.
- **Vault cleanup:** Step 14 removes postgres keys from Vault. If other apps share the same Vault path, only remove the postgres-specific keys.
