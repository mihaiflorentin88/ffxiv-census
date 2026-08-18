# Dynamic Queue Dispatcher & Cronjob Tuning

## Objective
Replace the current competing-consumer queue worker model with a dynamic Dispatcher and Worker Pool pattern. This ensures 100% resource utilization (zero idle threads) while strictly enforcing concurrency ceilings on specific job categories (like retries and character updates). Additionally, update Kubernetes configuration to align cronjob schedules, batch sizes, and max retry attempts with new operational requirements.

## Architecture: Dynamic Dispatcher & Worker Pool

The `ffxiv-census consume` command will be rewritten to use a single Dispatcher goroutine that feeds a centralized FIFO channel read by a pool of `C` worker goroutines (where `C` is the `--concurrency` flag).

### 1. Job Categories & Ceilings
The Dispatcher categorizes work and enforces dynamic ceilings based on total concurrency `C`:
- **Primary (`id-sweep`)**: 100% of `C` (Can consume all idle workers).
- **Retries (Failed jobs, `attempts > 0`)**: 25% of `C` (Minimum 1).
- **Updates (`character-census`)**: 25% of `C` (Minimum 1).
- **Secondary (`fc-census`, `achievement-census`)**: 25% of `C` (Minimum 1).

### 2. The Dispatch Loop
Every tick (e.g., `poll-interval`), the Dispatcher evaluates the worker pool:
1.  **Calculate Free Capacity**: `Capacity = C - Total_In_Flight_Jobs`.
2.  **Round-Robin Claiming**: It iterates through the categories (Retries -> Updates -> Secondary -> Primary).
3.  **Enforce Limits**: For each category, `Allowed = min(Capacity, Category_Ceiling - Category_In_Flight)`.
4.  **Claim**: If `Allowed > 0`, it requests up to `Allowed` jobs from the SQLite database.
5.  **Dispatch**: Retrieved jobs are pushed to the `jobChan`.
6.  **State Tracking**: Workers signal completion via a `doneChan`, allowing the Dispatcher to decrement the in-flight counters and free up capacity.

### 3. Database Interface Changes
To allow the Dispatcher to differentiate between new jobs and retries:
- Introduce `contract.ClaimMode` (`ClaimModeAny`, `ClaimModeNewOnly`, `ClaimModeRetriesOnly`).
- Update `contract.Queue.Claim(ctx, eventType string, batchSize int, mode ClaimMode)`.
- Update `infrastructure/queue/sqlite.go` to append `AND attempts = 0` (NewOnly) or `AND attempts > 0` (RetriesOnly) to the `UPDATE ... RETURNING` query.

## Kubernetes Configuration Changes (`k8s/values.yaml`)

The following adjustments will be made to the Helm chart values:

1.  **Max Retries**: Set `QUEUE_MAX_ATTEMPTS: "50"` in the `env` section for webserver, workers, and cronjobs. After 50 attempts, jobs safely park in the dead-letter (`failed`) state for future analysis or manual replay.
2.  **`backup` Cronjob**: 
    - Schedule changed to `0 1 * * *` (1 AM daily).
3.  **`publish-character` Cronjob (Updates)**: 
    - Schedule changed to `30 */3 * * *` (Every 3 hours at :30).
    - Command flags changed to `--limit 1000` (removing the incorrect `--auto` and `--count` flags).
4.  **`publish-id-sweep` Cronjob**: 
    - Schedule changed to `0 * * * *` (Hourly).
    - Command flag `--batch-size` increased to `3000`.
5.  **Worker Concurrency**:
    - Update `workers.instances.consumer.command` to explicitly pass `-c` and `20` (or allow it to be easily configurable) so the dispatcher has a healthy pool to divide.

## Edge Cases Handled
- **Low Concurrency**: If `C=1`, all 25% ceilings clamp to `1`. The Dispatcher will claim 1 job from Retries, then 1 from Updates, etc., naturally creating a fair round-robin FIFO loop for the single worker.
- **Queue Starvation**: If no retries or updates exist, `id-sweep` dynamically consumes 100% of the worker pool.
