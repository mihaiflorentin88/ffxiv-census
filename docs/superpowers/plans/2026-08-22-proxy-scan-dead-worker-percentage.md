# Reserve Proxy Scan Workers for Dead Proxies

## Context

The `proxy scan` worker currently fetches active, inactive, and dead proxies through one priority query, so concurrency cannot be reserved for dead proxies that may become usable again at particular hours. Add one percentage flag that partitions total scan concurrency into exclusive non-dead and dead-only pools while preserving the existing status transitions: successful checks become `active`, failed checks can still become `inactive` or `dead` exactly as before. Production will run with 20% of its 300 scan slots reserved for dead proxies and the other 80% prohibited from fetching dead proxies.

## Approach

1. Before changing production code, save this approved execution plan verbatim as `docs/superpowers/plans/2026-08-22-proxy-scan-dead-worker-percentage.md`, matching the repository's required plan-first workflow.

2. Add failing repository contract tests for exclusive scan populations.
   - In `port/contract/proxy.go`, plan the clean-cutover contract as two methods: retain `ListForScan(ctx context.Context, limit int) ([]ProxyRecord, error)` but redefine it to return only eligible `inactive` and `active` proxies, and add `ListDeadForScan(ctx context.Context, limit int) ([]ProxyRecord, error)` for eligible `dead` proxies only. No equivalent dead-only repository method exists.
   - Create `infrastructure/postgres/repository/proxy_test.go` using the existing `newTestDriver(t)` real-PostgreSQL fixture. Seed two inactive rows scanned 21 and 22 minutes ago, two active rows scanned 11 and 12 minutes ago, and two dead rows scanned 8 and 9 days ago by using `DatabaseDriver.Execute` after insertion. Assert `ListForScan` returns only the four active/inactive rows, `ListDeadForScan` returns only the two dead rows, each population is oldest-scan-first, and `limit=1` returns exactly its oldest eligible row.
   - Extend `TestFakeRepo_ListForScan_PriorityOrder` in `domain/proxy/service_test.go` to assert the same separation against `mock/repository.FakeProxyRepository`: non-dead listing excludes an eligible dead record, dead listing excludes active/inactive records, and dead results are oldest-scan-first and limit-bounded.
   - Extend the worker-local `fakeScanRepo` in `domain/proxy/worker/scan_test.go` with independent regular/dead batch queues, call counters, last-seen limits, and a real `ListDeadForScan` method; keep all other unused repository methods as panic stubs.
   - Run `go test ./infrastructure/postgres/repository ./domain/proxy/...` and record the expected red result caused only by the absent `ListDeadForScan` contract/implementations and the still-mixed `ListForScan` behavior.

3. Split persistence into mutually exclusive non-dead and dead-only queries.
   - Update the `ProxyRepository` comments in `port/contract/proxy.go` to state the exclusive populations and retain the existing status-age rules.
   - In `infrastructure/postgres/repository/proxy.go`, remove the dead branch from `ListForScan`; its SQL must select eligible `inactive` rows (existing 20-minute rule) and eligible `active` rows (existing 10-minute rule), order inactive before active and then `last_scanned_at ASC NULLS FIRST`, and preserve optional `LIMIT` and current row/error handling.
   - Add `func (r *ProxyRepository) ListDeadForScan(ctx context.Context, limit int) ([]contract.ProxyRecord, error)` reusing `proxyColumns` and `scanProxy`. Its SQL must select only `status = 'dead'` rows satisfying the existing production eligibility rule `last_scanned_at < NOW() - INTERVAL '7 days'`, order by `last_scanned_at ASC NULLS FIRST`, preserve the same optional-limit behavior, and wrap errors with dead-specific context such as `proxy list dead for scan` and `proxy scan dead row`.
   - In `mock/repository/proxy.go`, remove dead records from `ListForScan` and add `ListDeadForScan` using the fake's existing dead eligibility interval and `scannedBefore` ordering, applying the same `limit > 0` convention. Do not change dead eligibility intervals or wire the unrelated currently-unused `dead_scan_interval_days` config as part of this feature.
   - Add `ListDeadForScan` to exactly the three implementations found in this session: PostgreSQL `ProxyRepository`, `mock/repository.FakeProxyRepository`, and the worker-test `fakeScanRepo`; no compatibility adapter or mixed-query fallback remains.

4. Add failing worker and CLI tests for percentage allocation before implementing the scheduler.
   - Change `(*ScanWorker).RunScan` expectations to the exact signature `RunScan(ctx context.Context, concurrency, deadScanPercentage int) error`; update its seven current callers found by LSP (six in `domain/proxy/worker/scan_test.go`, one in `cmd/cli/proxy.go`). Existing worker tests pass percentage `0` unless they exercise the dead pool.
   - Add table-driven allocation cases in `domain/proxy/worker/scan_test.go`: `(concurrency=10, percentage=20) => regular limit 8, dead limit 2`; `(10, 0) => regular 10 and no dead-list call`; `(10, 90) => regular 1, dead 9`; `(10, 91)` and `(10, 200) => the same capped 1/9 split`; `(10, negative) => regular 10 and no dead-list call`; and `(0, 20) => normalized concurrency 4, integer-floor split regular 4/dead 0`.
   - Add worker behavior tests proving the two pools are independent: an empty dead pool waits without stopping regular batches; an empty regular pool does not stop eligible dead batches; a repository error from either pool cancels the sibling pool and is returned; context cancellation stops both pools; and a per-record scan failure pauses only its own pool while the other continues.
   - In `cmd/cli/proxy_test.go`, add a flag-registration test asserting `proxyScanCmd.Flags().Lookup("dead-scan-percentage")` exists, is an integer flag, and has default string value `"0"`.
   - Run `go test -race ./domain/proxy/worker ./cmd/cli` and record the expected red result caused only by the not-yet-implemented `RunScan` signature, split scheduler, and CLI flag.

5. Partition scan concurrency into independent long-running pools.
   - Add a private helper in `domain/proxy/worker/scan.go` named `splitScanConcurrency(concurrency, deadScanPercentage int) (regular, dead int)`. Normalize non-positive concurrency to the existing default `4`; clamp negative percentages to `0`; cap every value above `90` at `90`; calculate `dead = concurrency * deadScanPercentage / 100` using integer-floor division; and assign the exact remainder to `regular = concurrency - dead`. No existing percentage allocator can be reused.
   - Change `RunScan` to compute the split once at startup and launch up to two independent pool loops: the regular pool calls `ListForScan` with `regular` concurrency, and the dead pool is launched only when `dead > 0` and calls `ListDeadForScan` with `dead` concurrency. Because the percentage is capped at 90, the regular pool always retains capacity after concurrency normalization.
   - Define `type scanListFunc func(context.Context, int) ([]contract.ProxyRecord, error)` and extract the existing fetch/bounded-goroutine/batch-barrier/idle-delay logic into `runScanPool(ctx context.Context, pool string, concurrency int, list scanListFunc) error`. Each pool keeps its own semaphore, batch barrier, idle timer, and `atomic.Bool` error marker so per-record goroutines do not race while deciding whether that pool should pause; dead-pool emptiness or record failures cannot consume or pause regular capacity, and vice versa.
   - In `RunScan`, derive a child context with `context.WithCancel`, create a result channel buffered to the exact number of launched pools, and have each pool goroutine send its single terminal error before exiting. Receive one result per launched pool; on the first non-nil repository error, save it and cancel the sibling, then continue draining results before returning the saved error. Parent cancellation makes both pool runners return nil, and the fully buffered channel prevents a terminal send from blocking. Do not add a third-party coordination dependency.
   - Keep `Scanner.ProcessScanProxy(ctx, record)` and `domain/proxy/service.go` unchanged. Both pools use the existing service path, so successful checks still write `active` and failed checks still use the existing fail-count/age rules to choose `inactive` or `dead`.

6. Expose and deploy the percentage control.
   - Register exactly `proxyScanCmd.Flags().Int("dead-scan-percentage", 0, "percentage of scan concurrency reserved for dead proxies (0-90; values above 90 are capped)")` in `cmd/cli/proxy.go`.
   - In `proxyScanCmd.RunE`, read `deadScanPercentage`, include `"dead_scan_percentage", deadScanPercentage` in the existing `proxy.scan.start` log, and call `RunScan(ctx, concurrency, deadScanPercentage)`. Keep `splitScanConcurrency` private; after splitting, `RunScan` emits `scan_worker.pool_allocation` with exact fields `"regular_workers", regular` and `"dead_workers", dead`, so clamp/allocation math is not duplicated in the CLI.
   - In the `proxy-scan` command array under `workers.instances` in `k8s/values.yaml`, append the string arguments `--dead-scan-percentage` and `"20"` after `-c "300"`. This produces exactly 240 regular-only and 60 dead-only scan slots. Do not add any dead-marking suppression flag, and do not add the percentage flag to `proxy-new` or other worker deployments.

## Critical files & anchors

- `port/contract/proxy.go` — `ProxyRepository.ListForScan` currently promises one mixed priority population and needs the new dead-only port.
- `infrastructure/postgres/repository/proxy.go` — `ListForScan` contains all three hard-coded status branches and the production seven-day dead eligibility rule.
- `domain/proxy/worker/scan.go` — `RunScan` currently owns one loop, one SQL limit, and one semaphore for all statuses.
- `cmd/cli/proxy.go` — `proxyScanCmd.RunE` and `init` own scan concurrency parsing and runtime wiring.
- `k8s/values.yaml` — `workers.instances[name=proxy-scan].command` is the sole production invocation and currently runs at concurrency 300.

## Verification

Run all commands from `/home/mihai/Workspace/ffxiv-census`.

1. Prove persistence separation against both adapters:
   ```bash
   go test ./infrastructure/postgres/repository ./domain/proxy
   ```
   Expected: eligible active/inactive records appear only in `ListForScan`; eligible dead records appear only in `ListDeadForScan`; limits and oldest-first ordering hold. If PostgreSQL is unavailable, the existing fixture reports a skip; the mock test must still pass and the PostgreSQL test must be rerun where the configured local test database is available before delivery.

2. Prove allocation and concurrency behavior under the race detector:
   ```bash
   go test -race ./domain/proxy/worker
   ```
   Expected: 20% of 10 yields limits 8 regular/2 dead; 0% never calls the dead repository method; values above 90 behave as 90; the pools make progress independently, propagate repository errors, cancel cleanly, and report no races.

3. Run the focused CLI/domain suite and then the full repository suite:
   ```bash
   go test ./cmd/cli ./domain/proxy/...
   make test
   ```
   Expected: the new integer flag defaults to 0, every `ProxyRepository` implementation satisfies the expanded interface, all `RunScan` callers use the percentage argument, and existing proxy status-transition tests remain unchanged and passing.

4. Build the binary and inspect the actual CLI surface:
   ```bash
   make build
   ./bin/ffxiv-census proxy scan --help
   ```
   Expected: help lists `--dead-scan-percentage` with default `0` and documents the 0-90 range and cap; no dead-marking suppression flag exists.

5. Render the production worker manifest without deploying:
   ```bash
   helm template ffxiv-census k8s -f k8s/values.yaml --show-only templates/workers.yaml
   ```
   Expected: only `ffxiv-census-worker-proxy-scan` contains `--dead-scan-percentage "20"`; at concurrency 300 this maps to 240 regular slots and 60 dead slots, and no command contains a dead-marking suppression flag.

## Assumptions & contingencies

- The percentage applies to the `proxy scan --concurrency` value, not to Kubernetes replica counts. Pool sizes use integer-floor division so the dead pool never exceeds the requested percentage; production's 300 × 20% split is exact.
- The flag defaults to 0: manual invocations scan active/inactive proxies only unless they explicitly reserve dead capacity. Values below 0 behave as 0, and values above 90 behave as 90, guaranteeing at least the remainder for non-dead scanning.
- The regular and dead pools are exclusive even when one population is empty; unused capacity is not borrowed by the other pool. This guarantees the configured 80% production pool never scans dead proxies and the configured 20% pool never scans active/inactive proxies.
- Existing dead-marking behavior remains unchanged. No `disable-dead-marking` flag, service signature change, status-policy change, database migration, or new configuration key is part of this implementation.
