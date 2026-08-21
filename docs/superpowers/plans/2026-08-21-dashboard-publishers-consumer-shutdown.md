# Dashboard, Publishers, and Consumer Shutdown Plan

## Context
Correct the dashboard Race distribution card so its legend and chart are intentionally laid out and the pie chart is larger; audit every UI page so independent database queries used to assemble one response run concurrently. Repair every publisher path so scheduled and manual jobs durably enqueue their intended work, including the manually started ID-sweep job that completed without queueing anything. Verify and, where necessary, correct consumer SIGTERM handling so it stops fetching new jobs, lets all in-flight processing goroutines finish, and releases any held proxy lock in proxy mode while preserving shutdown in direct mode.

## Approach

### 1. Persist the approved execution spec

- Before changing production code, copy this plan verbatim to `docs/superpowers/plans/2026-08-21-dashboard-publishers-consumer-shutdown.md`; use that repository file as the implementation checklist.

### 2. Correct the dashboard Race distribution card

- In `cmd/http/ui/dashboard_test.go`, first add `TestDashboardHandler_RaceChartLayout`. Render `/ui/dashboard` with non-empty race data and assert the response emits the responsive two-card grid, a 340px race-chart container, and Chart.js options `maintainAspectRatio: false`, `cutout: "65%"`, and a bottom/center legend. Run this test against the current template and observe it fail before editing the template.
- In `cmd/http/ui/templates/dashboard.html`, reuse the working Race page conventions rather than introducing another chart style:
  - replace the fixed `grid-template-columns: 1fr 1fr` wrapper with `repeat(auto-fit, minmax(360px, 1fr))`;
  - raise the Race chart container from 280px to 340px;
  - move the legend from the right to the bottom, set `align: "center"`, and use the Race page's 12px label padding and 11px font;
  - set `maintainAspectRatio: false` and `cutout: "65%"`;
  - remove the asymmetric 20px left/right chart padding.
  Keep the existing dashboard palette, tooltip percentage callback, panel title, and empty state; the visual defect is layout, not chart data or theme.

### 3. Run independent UI data queries concurrently

- Add deterministic red tests before each service/handler edit. In `domain/census/service_test.go`, add test-local repository decorators with a shared channel barrier—no timing/sleep assertions—to prove that all expected query methods enter before any is released:
  - `TestService_SummaryQueriesRunConcurrently` expects the total count, active count, and max-level count to enter together;
  - `TestService_WorldDetailQueriesRunConcurrently` expects all seven world queries to enter together.
  In `cmd/http/ui/expansions_test.go`, add `TestExpansionsHandlerQueriesRunConcurrently`, sharing a barrier between the three `Summary` repository calls and `ExpansionCompletions`. Each test must fail by timing out at the barrier on the current serial implementation, then pass after its corresponding edit.
- Change `domain/census/service.go` `(*Service).Summary` to precompute the activity cutoff and max level, start one goroutine for each of its three repository queries, and join them with `sync.WaitGroup`. Store each result and error in a dedicated slot, then return errors after the join in the existing deterministic precedence: total, active, max-level. Do not cancel sibling queries after one failure; all already-started calls must finish.
- Change `domain/census/service.go` `(*Service).WorldDetail` to compute `filter`, `since`, `now`, and `since30d` before fan-out, then start the seven independent calls together: total, active, Chocobo milestone count, race breakdown, expansion completions, timeline, and one-character metadata lookup. Join all goroutines before constructing `WorldDetailStats`. Preserve the existing error precedence for the first six calls and preserve the current non-fatal treatment of the metadata lookup error.
- Change `cmd/http/ui/expansions.go` `(*UIController).Expansions` to start `Summary` and `ExpansionCompletions` concurrently, join them, and only then build `countMap` and percentage/retention rows. Preserve the current partial-page behavior: a failed summary leaves total at zero, while a failed completion query logs `ui.expansions.completions` and leaves completion counts empty.
- Keep `cmd/http/ui/dashboard.go`'s existing five-way `sync.WaitGroup` fan-out, but remove the expansion goroutine's unsynchronized read of `total` when it creates `ExpansionCard.Percent`; set percentages only in the existing post-`Wait` loop. Add a barrier-backed dashboard test if the existing dashboard test does not deterministically trigger the race, and prove the handler with `go test -race`.
- Record the completed route audit in the repository plan while implementing: `/ui/races`, `/ui/worlds`, `/ui/partials/world-breakdown`, and `/ui/methodology` perform at most one database query and need no goroutines. Do not alter the character detail/list services solely for this request because their routes are explicitly not registered in `cmd/http/ui/routes.go`.

### 4. Make every RabbitMQ publisher wait for durable broker acceptance

- Preserve the observed failure as the red contract: Pod `default/ffxiv-census-cron-publish-id-sweep-manual-20260821-151706-9jzsd` ran image `v1.1.0`, logged `publish.enqueued count: 800`, and exited 0 after roughly one second, while no work appeared in RabbitMQ. The common cause is `infrastructure/rabbitmq.Queue.Publish` using `PublishWithContext`, whose pinned `amqp091-go v1.14.0` contract explicitly does not confirm broker receipt.
- Add `infrastructure/rabbitmq/queue_test.go` integration cases gated by `RABBITMQ_TEST_URL`:
  - `TestQueuePublishWaitsForBrokerConfirmation` publishes a persistent known event and verifies it is present after the publisher connection closes;
  - `TestQueuePublishRejectsUnroutableEvent` publishes a unique unknown routing key and expects a non-nil `rabbitmq unroutable` error instead of the current nil result;
  - `TestQueuePublishReconnectRestoresReliableSession` closes the adapter connection, publishes a known event, and proves reconnect re-declares topology and restores confirm/return handling.
  Run them against a fresh local RabbitMQ vhost and observe the unroutable/reconnect contracts fail before changing the adapter.
- Add `func (q *Queue) openSession() (*amqp.Connection, *amqp.Channel, <-chan amqp.Return, error)` in `infrastructure/rabbitmq/queue.go`, and make both `New` and `reconnect` use it. The helper must dial, open a channel, idempotently declare the complete topology, call `Channel.Confirm(false)`, and register `NotifyReturn(make(chan amqp.Return, 1))` before exposing the session. On any setup error, close the partially opened channel/connection and leave the existing queue state unchanged.
- Change `(*Queue).Publish` while retaining its existing mutex serialization:
  - clear any stale return notification before the next serial publish;
  - call `PublishWithDeferredConfirmWithContext(ctx, "census", job.Type, true, false, publishing)` with `mandatory=true`;
  - wait with `DeferredConfirmation.WaitContext(ctx)`;
  - return an error if the deferred confirmation is unexpectedly nil, the broker nacks/closes it, or the context expires;
  - after an ack, non-blockingly read the return listener (RabbitMQ notifies returns before confirming mandatory messages) and return `rabbitmq unroutable: code=%d text=%q exchange=%q routing_key=%q` when present.
  A successful return from `Queue.Publish` must therefore mean the durable target queue accepted the message, not merely that bytes reached a client socket.
- Update `(*Queue).Close` to close the publishing channel before the connection and return `errors.Join` of both close errors. Keep persistent delivery and the existing `x-attempts: 0` header unchanged.
- In `cmd/cli/publish.go`, change the existing helper to `func publishAll(q contract.Queue, logger contract.Logger, ctx context.Context, jobs []contract.QueueJob) (int, error)`. Return the number confirmed before failure and wrap the error as `publish %q job %d of %d: %w`. Migrate ID sweep, character census, achievement census (including single-ID mode), proxy discovery, and proxy scan to this helper so every normal publisher has identical confirmed/fail-fast behavior without buffering results across providers.
- In `cmd/cli/proxy.go`, extract `func publishDiscoveredProxies(ctx context.Context, q contract.Queue, logger contract.Logger, providers []contract.ProxyProvider) (int, error)`. Fetch providers sequentially as today, build and release one provider's job slice at a time, and call `publishAll` for that slice. Continue past provider fetch failures, but return `proxy discovery failed: all providers failed (%d errors)` when no provider publishes anything and at least one provider failed. Proxy scan should map its already-bounded repository result to jobs and call `publishAll` once. Legitimately empty successful providers, no stale characters, no characters, no ID gaps, and no proxies due for scanning remain successful zero-work runs with their existing explicit logging.
- Add a deferred `Queue.Close` immediately after queue acquisition in every short-lived publisher: the three `publish` subcommands, `proxy discover`, `proxy scan`, and the legacy `migrate queue` path. Each defer logs a close failure as `queue.close_error`; confirmed messages remain successful even if final connection teardown reports an error. The two long-lived census consumer commands and `proxy consume` receive the same defer in the shutdown step below.
- Extend `cmd/cli/publish_test.go` with an error-injecting queue fake and `TestPublishAllStopsOnFirstUnconfirmedJob`, asserting the returned confirmed count, event type, one-based job position, and that later jobs are not attempted. Add `cmd/cli/proxy_test.go` cases around `publishDiscoveredProxies` for a successful empty provider set, partial provider-fetch failure with confirmed output, all providers failing, and a queue publish failure; these tests define every zero-output exit status without mutating the global service container.

### 5. Make SIGTERM, in-flight completion, and proxy release one ordered lifecycle

- Add a test-local `blockingClaimQueue` in `domain/census/worker/worker_test.go`. It implements `contract.Queue`, hands exactly one claimed job to the handler with a dedicated non-signal processing context, exposes channels for "claimed", "handler started", and "handler release", stops handing out work when the outer claim context is cancelled, and records `Close`; use it for the direct and proxy shutdown contracts below.
- Add red coordination tests before production edits:
  - in `infrastructure/rabbitmq/queue_test.go`, `TestRunConsumerPoolStopsClaimsAndWaitsForInflight` must cancel the signal context, observe the claim context cancel immediately, observe the processing context remain live, and prove the pool does not return until a blocked handler is released;
  - `TestRunConsumerPoolWorkerErrorStopsSiblings` must prove an AMQP worker error stops sibling claiming, joins every sibling, and returns the original error instead of hanging;
  - in `domain/census/worker/worker_test.go`, `TestRunEventsWaitsForClaimedProviderJobOnShutdown` must use `blockingClaimQueue` to prove a claimed job waiting on a provider uses the non-cancelled processing context;
  - `TestRunEventsWithProxyReleasesEveryLockAfterInflightShutdown` must use the same queue, cancel while proxy work is blocked, then verify the fake proxy repository remains locked until the work finishes and is unlocked before `RunEventsWithProxy` returns;
  - `TestProxyWorkerReleasesInitialClaimWhenClientCreationFails` must expose the current early-return lock leak and verify it is cleared.
- In `infrastructure/rabbitmq/queue.go`, extract the duplicated `Consume`/`ConsumeFailed` orchestration into:
  `func runConsumerPool(ctx context.Context, concurrency int, worker func(stopClaiming, processCtx context.Context, workerID int) error) error`.
  It must create a signal-linked claim context and an independent processing context, start all workers, cancel claiming on SIGTERM or the first infrastructure worker error, wait for every worker, cancel the processing context only after the join, and return joined infrastructure errors only when shutdown was not signal-driven. Use `context.AfterFunc` (and stop it on return) instead of leaving a goroutine blocked forever on `ctx.Done`.
- Keep `consumeWorker`/`failedWorker` prefetch at one and their pre-handler cancellation check: a delivery already handed to the client but not started is nacked/requeued, while a handler that already started receives `processCtx`, completes, publishes downstream work or retry state, and acks before its AMQP channel closes.
- In `domain/census/worker/worker.go` direct mode, remove the separate `shutdownCtx` capture and call `waitForProviders(processCtx, job.Type)`. This prevents SIGTERM from aborting an already claimed job during a rate-limit wait; cancellation only stops new claims.
- Give proxy lock owners process-wide uniqueness. In `cmd/cli/consume.go`, require `os.Hostname()` and build `ownerPrefix := fmt.Sprintf("census-consume-%s-p%d", hostname, os.Getpid())`; change `RunEventsWithProxy` to accept that prefix after `concurrency` and derive each owner as `fmt.Sprintf("%s-w%d", ownerPrefix, workerID)`. Remove the `runtime.NumGoroutine`/stack-derived goroutine-ID code. Remove the unused `owner string` parameter from `container.(*ServiceContainer).ProxyHub` and update its sole caller to `container.Load.ProxyHub()`.
- Register the proxy-release defer immediately after the initial `ProxyHub.NewProxy` succeeds, before either proxy-aware client is constructed. The defer must use a fresh 10-second background timeout, log `worker.proxy_release_error` on failure, and ensure `RunEventsWithProxy` cannot return before every worker's release attempt finishes.
- Make proxy rotation single-owner-safe:
  - when `CanUse` is false, release the old proxy successfully before claiming a replacement; do not hold an untracked second lock;
  - when a handler reports a proxy error, call `MarkFailed` with a bounded context and propagate a mark failure instead of ignoring it;
  - after marking failed, if the claim context is cancelled, return the job error without acquiring a replacement that shutdown will never use;
  - after any replacement is assigned, client-construction failures are covered by the already-registered defer and release that replacement.
- In `cmd/cli/consume.go` (`consume` and `consume failed`) and `cmd/cli/proxy.go` (`proxy consume`), defer queue close immediately after successful acquisition. Queue close therefore occurs only after the queue pool has joined in-flight handlers and, in proxy mode, after `RunEventsWithProxy` has joined all proxy-release defers.

## Critical files & anchors

- `infrastructure/rabbitmq/queue.go` — `New`, `Publish`, `Consume`, `ConsumeFailed`, `Close`, and `reconnect`; the common reliability and shutdown boundary for every publisher/consumer.
- `domain/census/worker/worker.go` — `RunEvents` and `RunEventsWithProxy`; owns claimed-job contexts and per-goroutine proxy locks.
- `domain/census/service.go` — `Summary` and `WorldDetail`; the active UI service methods with serial independent database calls.
- `cmd/http/ui/templates/dashboard.html` — Race card grid/container and `racePieChart` Chart.js options.
- `cmd/cli/publish.go` — shared `publishAll` path and the three census publishers; proxy and migration publishers must conform to this behavior.

## Verification

- From the repository root, run the new red/green contracts individually:
  - `go test -run 'TestDashboardHandler_RaceChartLayout|TestExpansionsHandlerQueriesRunConcurrently' ./cmd/http/ui`
  - `go test -run 'TestService_(SummaryQueriesRunConcurrently|WorldDetailQueriesRunConcurrently)' ./domain/census`
  - `go test -race -run 'TestDashboardHandler|TestRunEvents|TestProxyWorker|TestRunConsumerPool' ./cmd/http/ui ./domain/census/worker ./infrastructure/rabbitmq`
- Start a disposable RabbitMQ broker when `RABBITMQ_TEST_URL` is not already supplied:
  `docker run --rm -d --name ffxiv-census-rabbit-test -e RABBITMQ_DEFAULT_USER=census -e RABBITMQ_DEFAULT_PASS=secret -e RABBITMQ_DEFAULT_VHOST=ffxiv-census -p 5672:5672 rabbitmq:4-management`.
  Then run:
  `env RABBITMQ_TEST_URL=amqp://census:secret@127.0.0.1:5672/ffxiv-census go test -run 'TestQueue(Publish|Reconnect)' ./infrastructure/rabbitmq`.
  The known event must remain queued after `Queue.Close`, the unknown routing key must return `rabbitmq unroutable`, and reconnect must publish through freshly declared/confirmed topology.
- Build the actual CLI with `make build`, then against the fresh broker run:
  `env RABBITMQ_URL=amqp://census:secret@127.0.0.1:5672/ffxiv-census ./bin/ffxiv-census publish achievement-census --character-id 1`.
  `docker exec ffxiv-census-rabbit-test rabbitmqctl list_queues -p ffxiv-census name messages_ready messages_unacknowledged` must show `census.achievement-census` with one ready and zero unacknowledged messages after the CLI has exited.
- Purge the achievement smoke message with `docker exec ffxiv-census-rabbit-test rabbitmqctl purge_queue -p ffxiv-census census.achievement-census`. With the same broker and a disposable PostgreSQL started by `make postgres`, run the built direct consumer through the harness process manager:
  `env POSTGRES_DSN=postgres://census:secret@127.0.0.1:5432/ffxiv_census?sslmode=disable RABBITMQ_URL=amqp://census:secret@127.0.0.1:5672/ffxiv-census ./bin/ffxiv-census consume achievement-census -c 1`.
  Send SIGTERM and require exit status 0. The blocking unit contracts above must separately prove that a claimed handler is joined before exit and that proxy-mode return is delayed until its repository lock is cleared.
- For the dashboard visual proof, first start the built server with:
  `env POSTGRES_DSN=postgres://census:secret@127.0.0.1:5432/ffxiv_census?sslmode=disable RABBITMQ_URL=amqp://census:secret@127.0.0.1:5672/ffxiv-census ./bin/ffxiv-census server --start --port 8080`.
  After startup migrations complete, seed visible race data with:
  `docker exec ffxiv-postgres psql -U census -d ffxiv_census -c "INSERT INTO characters (id,name,world,datacenter,region,race,first_seen_at) SELECT i, 'Character ' || i, 'Balmung', 'Crystal', 'NA', (ARRAY['Hyur','Elezen','Lalafell','Miqo''te','Roegadyn','Au Ra','Hrothgar','Viera'])[i], NOW() FROM generate_series(1,8) AS g(i) ON CONFLICT (id) DO UPDATE SET race=EXCLUDED.race, deleted_at=NULL;"`.
  Browser-drive `/ui/dashboard` at 1440×900 and 390×844. At desktop width the Race and expansion cards must balance in the responsive grid; the doughnut must be visibly larger than the former 280px/right-legend rendering; the legend must be centered beneath it with no clipping. At mobile width the cards must stack without horizontal overflow. Request `/ui/worlds/Balmung` and `/ui/expansions` and require HTTP 200 with their seeded/empty-state content rendered.
- Run `go test -p 1 ./...` and `go test -race ./domain/census/worker ./infrastructure/rabbitmq ./cmd/http/ui` after the focused proofs. Compare the final diff line-by-line with this plan: every registered multi-query UI path is parallel, every `Queue.Publish` caller inherits confirms, every consumer command closes after joining, and no disabled personal-data route or unrelated chart theme changed.

## Assumptions & contingencies

- "A bit bigger" is implemented by reusing the existing 340px chart container and bottom-legend pattern, rather than introducing a new dashboard-only size or redesigning the palette.
- "All UI pages" means routes currently registered by `UIController.RegisterRoutes`. The disabled character routes remain audited but unchanged until they are re-enabled.
- Publisher batches fail fast on the first message the broker does not confirm; already confirmed messages may be duplicated by Kubernetes `OnFailure` retry, which is safe because handlers persist idempotently. Legitimate empty source selections remain successful and explicitly logged.
- The queue adapter waits indefinitely only when its caller supplies an unbounded context, matching the current API. Kubernetes retains the existing 600-second CronJob deadline and 180-second worker termination grace as external safety limits.
- If `RABBITMQ_TEST_URL` is supplied, use that isolated test vhost instead of starting Docker; never point the unroutable/reconnect integration cases at the production vhost. If neither an isolated broker nor Docker is available, publisher reliability verification is blocked rather than silently skipped.
