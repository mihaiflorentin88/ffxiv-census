# Design Spec: Pure-Go SQLite Work Queue & Runtime Goose Migrations, Go-Gitmirror Config & Env Alignment, and Kubernetes CronJob Publisher Execution

## 1. Executive Summary

This specification defines four key architectural enhancements for `ffxiv-census`:
1. **Kubernetes CronJob Publisher Execution**: Structuring the `publish` CLI as a single-shot batch command for scheduled Kubernetes CronJobs, supporting automatic forward range sweeping (`--auto`) starting from `MaxID + 1` and gap filling (`--fill-gaps`).
2. **Durable SQLite Work Queue & Auto-Retries**: SQLite-backed work queue with atomic multi-consumer claims (`BEGIN IMMEDIATE` / `UPDATE ... RETURNING`), exponential backoff with jitter on HTTP 429 rate limits, and infinite retries (`max_attempts = 0` / `-1`) for critical discovery sweeps.
3. **Runtime Goose Migrations with `embed.FS`**: Embedded SQL migrations automatically applied on boot via `goose.Up()` on first SQLite connection acquire, supporting pure-Go compilation (`CGO_ENABLED=0`) with `modernc.org/sqlite`.
4. **`go-gitmirror` Config & Environment Variable Alignment**: Exact Viper pattern with `strings.NewReplacer("-", "_", ".", "_")`, `AutomaticEnv()`, and embedded `config.toml`.

---

## 2. Kubernetes CronJob Publisher Architecture

The publisher is executed periodically (e.g. hourly) as a Kubernetes CronJob rather than a long-running daemon pod.

### 2.1 Forward Range Sweep (`publish id-sweep --auto`)
- Connects to SQLite (self-migrating on boot).
- Resolves `CharacterRepository.MaxID(ctx)`.
- If database is empty (`MaxID == 0`), begins from ID 1.
- Range: `from = MaxID + 1`, `to = MaxID + batchSize` (default: 1,000 IDs).
- Splits range into chunks of `--chunk-size` (default: 100 IDs).
- Enqueues jobs with `MaxAttempts = -1` (infinite retries on rate limits).
- Deduplicates via `INSERT OR IGNORE` on `(type, payload_hash)`.
- Logs progress and exits with status 0.

### 2.2 Gap Fill Sweep (`publish id-sweep --fill-gaps`)
- Detects unscanned gaps below `MaxID` using `repo.FindIDGaps(ctx, maxID, maxGaps)`.
- Enqueues chunked `id-sweep` jobs for each missing range.
- Exits with status 0.

### 2.3 Kubernetes CronJob Manifest
```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: ffxiv-census-publisher
spec:
  schedule: "0 * * * *"
  concurrencyPolicy: Forbid
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: publisher
              image: ffxiv-census:latest
              args: ["publish", "id-sweep", "--auto", "--batch-size", "1000", "--chunk-size", "100"]
              env:
                - name: SQLITE_PATH
                  value: /data/ffxiv-census.db
              volumeMounts:
                - name: data
                  mountPath: /data
          volumes:
            - name: data
              persistentVolumeClaim:
                claimName: census-pvc
```

---

## 3. Queue Auto-Retry Mechanics

- **Persistence**: `queue_jobs` SQLite table with columns for `type`, `payload`, `payload_hash`, `status`, `run_at`, `attempts`, `max_attempts`, `last_error`, `claimed_at`, `created_at`, `failed_at`, `completed_at`.
- **Atomic Claims**: Single atomic `UPDATE ... WHERE id IN (SELECT id ... LIMIT n) RETURNING ...` inside a transaction.
- **Retry Backoff**: Jittered exponential backoff: `backoff = min(base * 2^(attempts-1), 5120s) * (0.9 + 0.3 * rand)`.
- **Infinite Retries**: When `max_attempts == 0` (or `MaxAttempts: -1` on enqueue), jobs are never failed to dead-letter, retrying indefinitely after backoff.

---

## 4. Configuration & Environment Variables

- Embedded `config.toml` via `//go:embed config.toml`.
- Viper initialized with `v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))` and `v.AutomaticEnv()`.
- Supports environment variable overrides for both nested and hyphenated keys.
