# Consumer Resource Optimization Plan

## Context

Optimize the long-running process started by `cmd/cli/consume.go` for lower CPU and memory without changing its operating modes, concurrency, event ownership, retries, provider selection, or rate-limit topology. Direct mode keeps one process-wide Lodestone/Tomestone client set and one shared `ProviderRateLimiter` across all goroutines and selected queues; proxy mode keeps one proxy, Lodestone client/token bucket, and `ProxyRateLimiter` per goroutine, creates a per-goroutine Tomestone client wherever the selected event can use Tomestone, and retains the existing process-wide Tomestone request bucket. Multi-queue direct consumption, single-event proxy consumption, failed-queue replay, per-job proxy ownership extension, fallback rules, queue acknowledgements, and deployment commands remain exactly as implemented. The optimization is limited to state that is provably unused, duplicate initialization, bounded HTTP buffering/connection cleanup, and success-log overhead.

## Approach

1. **Persist this approved execution spec before changing code.** Copy it verbatim to `docs/superpowers/plans/2026-08-22-consumer-resource-optimization.md`. At each edit, reread the current symbol because the repository has evolved beyond historical plans; the mode contracts below are fixed and must not be “simplified” into one shared worker strategy.

2. **Lock the existing direct/proxy workflows with regression assertions and capture an allocation baseline.**
   - Run `TestWorker_MultiQueueDefaultAll`, `TestWorker_LodestoneRateLimit_PausesLodestoneQueues_RunsDualSourceQueuesOnTomestone`, and `TestWorker_AllProvidersPaused_SleepsUntilEarliestCooldown` before edits and retain them unchanged. Together they define one direct consumer spanning all queues, shared cross-queue provider cooldown, fallback progress, and sleep-without-spin behavior.
   - Add `TestRunEventsWithProxyCreatesIsolatedWorkerDependencies` in `domain/census/worker/proxy_worker_test.go`. Run two proxy goroutines with two active fake proxies; record the arguments passed through `newRateLimiter`, `newLodestoneClient`, `newTomestoneClient`, and `newHandlers`. Require exactly two distinct `ProviderRateLimiter` instances, one instance consistently passed to all three factories for a given goroutine, and no instance shared across goroutines. This is a regression guard only; production factory semantics do not change.
   - Keep `TestWorker_DynamicDispatcher_LowConcurrencyRoundRobin` and `TestWorker_DynamicDispatcher_NoStarvationWhenPrimaryAlreadyRunning` green to prove one direct worker still consumes multiple queues fairly.
   - Add `BenchmarkIDSweepNotFoundInfo` in `domain/census/handler/idsweep_test.go`: process a fixed 100-ID all-not-found sweep with an Info-level `slog.TextHandler(io.Discard, ...)`, create payload/fakes outside the timed loop, call `b.ReportAllocs`, and fail on handler error. Before production edits run:
     ```bash
     go test -run '^$' -bench BenchmarkIDSweepNotFoundInfo -benchmem -count=5 ./domain/census/handler
     ```
     Record `ns/op`, `B/op`, and `allocs/op`; this measures the command's highest-frequency success/logging path without changing queue or rate behavior.

3. **Remove duplicate and unused initialization in `runProxyConsumer` while preserving every limiter boundary.**
   - At the start of `runProxyConsumer`, read `cfg := container.Load.Config()` once and retain `lodestoneCfg := cfg.Lodestone`, `tomestoneCfg := cfg.Tomestone`, proxy rate override, and timeout override as immutable captured pointers/values. This removes one mutex-protected `Config()` lookup per proxy goroutine; do not move either client or limiter to process scope.
   - Before `RunEventsWithProxy` launches goroutines, call `container.Load.CensusService()` once and return `census service not initialised` when nil. `ProxyCensusHandlers` currently calls the unsynchronized lazy `CensusService()` accessor concurrently from every starting goroutine; prewarming it on the command goroutine ensures milestone sync and service construction occur once, while every per-goroutine registry still references the same service exactly as intended.
   - Change only the unused base registry passed to the proxy worker:
     ```go
     w := worker.New(q, nil, logger)
     ```
     `RunEventsWithProxy` uses the per-goroutine registry returned by `newHandlers` and never reads `w.handlers`; the direct branch remains exactly:
     ```go
     w := worker.New(
         q,
         container.Load.Handlers(),
         container.Load.Logger(),
         container.Load.ProviderRateLimiter(),
     )
     return w.RunEvents(ctx, eventTypes, concurrency)
     ```
     This avoids constructing an unused direct Lodestone scraper, direct Tomestone client, global handler registry, and global provider limiter in every proxy-only process.
   - Add this conservative helper in `cmd/cli/consume.go`:
     ```go
     func proxyEventsNeedTomestone(eventTypes []string) bool {
         if len(eventTypes) == 0 {
             return true
         }
         for _, eventType := range eventTypes {
             switch eventType {
             case handler.EventAchievementCensus:
                 continue
             case handler.EventIDSweep, handler.EventCharacterCensus:
                 return true
             default:
                 return true
             }
         }
         return false
     }
     ```
     After confirming `tomestoneCfg` is non-nil (preserving the current proxy-mode configuration prerequisite), replace `newTomestoneClient` with a same-signature no-op returning `(nil, nil)` only when the selected set consists exclusively of `achievement-census`. Achievement handling is Lodestone-only by documented contract, so this avoids one unused HTTP client/transport per achievement proxy goroutine. Empty, mixed, dual-source, or unknown event sets conservatively retain Tomestone.
   - Add table tests in `cmd/cli/consume_test.go` for empty/default, achievement-only, ID-sweep-only, character-only, mixed census, and unknown inputs. Do not change the `--events`, positional-event, `--proxy`, or `--concurrency` flags/defaults.
   - Preserve these exact limiter decisions: `newRateLimiter` continues returning `provider.NewProxyRateLimiter()` once per goroutine; each Lodestone client continues creating its own request token bucket from `[proxy.consumer].lodestone_rate_limit`; every proxy Tomestone client continues receiving its goroutine-local `ProviderRateLimiter` plus the existing process-wide `sharedTomestoneLimiter`. Do not replace `WithSharedRateLimiter`, pass the provider limiter into Lodestone, or alter current 429/fallback semantics in this optimization.
   - In `docs/events.md`, retain direct multi-queue shared provider cooldown, proxy per-goroutine proxy/client/provider limiter, Lodestone-only achievements, and dual-source fallback; add only that achievement-only proxy processes omit the unused Tomestone transport. In `docs/proxy.md`, retain per-job `CanUse`/lock extension, immediate replacement, one proxy owner per goroutine, and no direct fallback; document the explicit domain-service prewarm.
   - Do not modify `k8s/values.yaml`, queue topology, failed-consumer deployment, command concurrency, rate-limit values, `GOMEMLIMIT`, retry timings, event lists, or proxy lock cadence.

4. **Bound Tomestone response allocations and release proxy HTTP pools without changing request routing or response mapping.**
   - First add tests in `infrastructure/tomestone/client_test.go` for a normal direct and nested profile response, malformed JSON, a response over 4 MiB, and a multi-megabyte non-2xx body. Preserve every existing status mapping, `Retry-After` wait, shared request bucket, per-client adaptive state, and provider pause.
   - In `infrastructure/tomestone/client.go`, replace success-path `io.ReadAll` + `json.Unmarshal` with one bounded streaming decode:
     ```go
     const (
         maxTomestoneResponseBytes = int64(4 << 20)
         maxTomestoneErrorBytes    = int64(64 << 10)
     )

     func decodeTomestoneResponse(body io.Reader, dst *jsonResponse) error {
         limited := &io.LimitedReader{R: body, N: maxTomestoneResponseBytes + 1}
         decoder := json.NewDecoder(limited)
         if err := decoder.Decode(dst); err != nil {
             if limited.N == 0 {
                 return errors.New("tomestone response exceeds 4194304 bytes")
             }
             return err
         }
         err := decoder.Decode(&struct{}{})
         if limited.N == 0 {
             return errors.New("tomestone response exceeds 4194304 bytes")
         }
         if err == nil {
             return errors.New("tomestone response contains multiple JSON values")
         }
         if !errors.Is(err, io.EOF) {
             return err
         }
         return nil
     }
     ```
     For 401/403 and generic error statuses, retain at most `maxTomestoneErrorBytes` for the existing log/error string; 404 and 429 need no retained body. Keep the existing `defer resp.Body.Close()`.
   - In `NewClientWithProxy`, clone `http.DefaultTransport.(*http.Transport)` and override only `Proxy` or `DialContext`; do not replace proxy protocol handling. This inherits bounded standard idle-connection/TLS timeouts instead of using a zero-value transport. Add `func (c *Client) CloseIdleConnections() { c.httpClient.CloseIdleConnections() }`.
   - In `domain/census/worker/worker.go`, add a private structural closer so the port interfaces and fake clients do not gain a lifecycle method:
     ```go
     type idleConnectionCloser interface {
         CloseIdleConnections()
     }

     func closeIdleConnections(client any) {
         if closer, ok := client.(idleConnectionCloser); ok {
             closer.CloseIdleConnections()
         }
     }
     ```
     Close the old Tomestone client immediately before overwriting it during proxy replacement and close the currently held Tomestone client in `proxyWorkerLoop`'s existing exit defer. A closable fake must prove one close per discarded concrete client. Do not close or share live clients across goroutines; Lodestone's third-party scraper exposes no close method and remains goroutine-local.
   - Update `docs/tomestone.md` with bounded streaming decode and concrete idle-pool closure while retaining the current process-wide shared proxy request bucket and goroutine-local provider cooldown. Do not rewrite source to match the stale statement that all Tomestone request-rate state is per goroutine.

5. **Reduce consumer success-log CPU while retaining one Info completion per fully successful job.**
   - Extend `port/contract.Logger` with `Enabled(ctx context.Context, level slog.Level) bool`; `*slog.Logger` is the sole concrete implementation found by LSP and already conforms. This lets high-frequency Debug details avoid building attributes or scanning results when Info is configured.
   - In both direct and proxy `processJob` paths, move `worker.job_start` to Debug and remove pointer identity fields (`handler`, `scraper`, `lodestone_client`). Keep exactly one Info `worker.job_done` after the handler and every downstream publish succeed; move the current completion call below the publish loop in the normal, proxy, and replacement-retry branches. Retry stays Warn, publish failure stays Error, and lifecycle/provider/proxy state stays Info.
   - Move handler success details to Debug: ID-sweep start/probe/discovered/done; character start/fetched/stored/deleted/done; achievement start/fetched/done. Guard per-ID ID-sweep logs and the achievement-only `latestAchievement` diagnostic with `logger.Enabled(ctx, slog.LevelDebug)`; at Info, `ProcessAchievements` performs the only required earned-list pass. Keep every fetch/store/process warning or error unchanged.
   - Remove `fmt.Sprintf(\"%p\", c.scraper)` from Lodestone logs because formatting currently happens even when Debug is disabled. Retain request ID, attempt, proxy, duration, and error fields.
   - Convert detailed logging tests to use a Debug-enabled logger and add `TestSuccessfulHandlersAreQuietAtInfo`. Add a worker test requiring exactly one `worker.job_done` and no `worker.job_start` for one fully successful job, plus a downstream-publish-failure case requiring no false `worker.job_done`.
   - Update the logging behavior table in `docs/logging-and-middleware.md` in the same edit: Info is one completed queue job; Debug is fetch/store/probe detail. Do not alter logger type or configured level.


## Critical files & anchors

- `cmd/cli/consume.go` — `RunE` and `runProxyConsumer`; the mode boundary whose direct shared dependencies and proxy-isolated factories must remain distinct.
- `domain/census/worker/worker.go` — `RunEventsWithProxy`, `proxyWorkerLoop`, and `replaceProxy`; per-goroutine dependency lifecycle and the only supporting client-cleanup/log-order edits.
- `container/domain.go` — `CensusService`, `Handlers`, and `ProxyCensusHandlers`; proves the shared service is lazily initialized while proxy handler registries/clients are per worker.
- `infrastructure/tomestone/client.go` — `NewClientWithProxy` and `fetchProfile`; bounded response/transport memory without limiter or status-policy changes.
- `domain/census/handler/idsweep.go` — `Handle`; the highest-frequency success logging path measured by the allocation benchmark.

## Verification

1. From the repository root, run the unchanged workflow contracts before edits and after every relevant worker change:
   ```bash
   go test -run 'TestWorker_(MultiQueueDefaultAll|LodestoneRateLimit_PausesLodestoneQueues_RunsDualSourceQueuesOnTomestone|AllProvidersPaused_SleepsUntilEarliestCooldown)' ./domain/census/worker
   go test -run 'TestWorker_DynamicDispatcher_(LowConcurrencyRoundRobin|NoStarvationWhenPrimaryAlreadyRunning)' ./domain/census/worker
   ```
   Direct mode must still consume all three queues through one pool, allow a dual-source queue to progress while Lodestone is paused, and block without spinning when all applicable providers are paused.

2. Run the new proxy-wiring and lifecycle tests:
   ```bash
   go test -run 'TestRunEventsWithProxyCreatesIsolatedWorkerDependencies|TestProxyWorker.*CloseIdleConnections|TestReplaceProxy' ./domain/census/worker
   go test -run 'TestProxyEventsNeedTomestone|TestConsumeCmd_FlagsAndArgs' ./cmd/cli
   ```
   Two proxy goroutines must own different `ProviderRateLimiter` objects; each goroutine must pass its one limiter consistently to its Lodestone, Tomestone, and handler factories. `proxyEventsNeedTomestone` must return false only for a non-empty achievement-only set. Every replaced/final Tomestone concrete client must close its own idle pool without closing another worker's client.

3. Run the bounded-response and logging contracts:
   ```bash
   go test -run 'TestFetchProfile.*(Direct|Nested|Malformed|TooLarge|ErrorBody)' ./infrastructure/tomestone
   go test -run 'TestSuccessfulHandlersAreQuietAtInfo|Test.*Logs' ./domain/census/handler
   go test -run 'TestWorker.*InfoLogging|TestWorker.*PublishFailure' ./domain/census/worker
   ```
   A normal Tomestone profile must map identically to the current DTO; more than 4 MiB must fail with `tomestone response exceeds 4194304 bytes`; non-2xx retained text must never exceed 64 KiB. A successful job with successful downstream publishes emits one `worker.job_done`; a downstream publish failure emits no success record.

4. Compare the post-change hot-path benchmark with the five-run baseline:
   ```bash
   go test -run '^$' -bench BenchmarkIDSweepNotFoundInfo -benchmem -count=5 ./domain/census/handler
   ```
   Record median `ns/op`, `B/op`, and `allocs/op`; all three must decrease. If one does not, generate `-cpuprofile /tmp/consume-cpu.out -memprofile /tmp/consume-mem.out` from the same benchmark and remove remaining success-log work rather than changing queue, limiter, or concurrency behavior.

5. Run focused race coverage, the full suite, and the built command:
   ```bash
   go test -race ./cmd/cli ./domain/census/worker ./domain/census/handler ./infrastructure/tomestone
   make test
   make build
   ```
   With the documented disposable PostgreSQL and RabbitMQ running and all three census queues empty, start:
   ```bash
   ./bin/ffxiv-census consume --events id-sweep,character-census,achievement-census --concurrency 4
   ```
   RabbitMQ queue inspection must show four consumers on each selected queue, proving multi-queue direct topology is unchanged. Send SIGTERM and require exit status 0. Do not publish API work in this topology smoke; handler/provider behavior is covered deterministically by the focused tests.

6. If the current cluster is accessible, capture consumer CPU/RSS at the existing concurrency before rollout and for ten minutes after rollout under a comparable queue backlog. Acceptance is lower steady-state CPU/RSS for proxy achievement workers and lower log volume, with unchanged queue drain correctness, no new restarts, and no rate-limit increase. If cluster access or a comparable backlog is unavailable, report only the benchmark, race tests, client-factory omission tests, and local topology smoke; do not fabricate production improvement.

7. Review the implemented paths against this plan before commit: `k8s/values.yaml`, `infrastructure/rabbitmq`, provider limiter implementations, event lists, flags/defaults, proxy lease validation, retry/failure handling, fallback branches, and database persistence must be untouched. Commit the implementation plan, tests, code, and behavior documentation and push the completed branch as required by repository workflow.

## Assumptions & contingencies

- Current workflow has priority over maximum resource reduction. Concurrency values, Kubernetes resources, queue subscriptions, failed replay, direct multi-queue dispatch, and per-job proxy `CanUse`/lease extension remain unchanged even if reducing them would save more resources.
- “Per-goroutine limiter” in proxy mode means the current source-verified goroutine-local `ProxyRateLimiter` plus each Lodestone client's own request token bucket. The existing process-wide Tomestone request token bucket remains shared across proxy goroutines; this plan does not reinterpret or redesign either layer.
- Achievement census is source-verified as Lodestone-only. Therefore an achievement-only proxy process may omit its unused Tomestone concrete client while still constructing one proxy-local handler registry and limiter per goroutine. Any empty, mixed, dual-source, or unknown event set keeps Tomestone.
- `CensusService()` is prewarmed before goroutines solely to serialize existing lazy initialization; the service remains shared exactly as it is today. If implementation-time synchronization has already been added inside `CensusService`, keep the prewarm as harmless startup validation and do not add a second lock.
- The 4 MiB success and 64 KiB error-body limits are memory safety bounds, not API schema changes. If an existing recorded valid fixture exceeds 4 MiB, set the limit to the smallest whole-MiB value above the fixture plus 25% headroom, encode that exact value in the test, and retain bounded streaming decode.
- No close method is added to `contract.LodestoneClient` or `contract.TomestoneClient`; optional structural cleanup applies only to concrete clients that expose it, preserving every fake and port caller.

