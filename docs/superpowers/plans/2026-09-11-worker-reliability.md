# Worker reliability: Lodestone status handling, ack timing, retry handlers, proxy rate limiter

Slug: `worker-reliability`

## Context

The census workers (id-sweep, achievement-census, character-census) misroute and lose messages: the retry consumer spams `no handler registered for event census.id-sweep(.failed)`, 404/403 terminal outcomes get retried, 202/timeout outcomes don't reliably reach the retry queue, the per-proxy rate limiter is ignored by the HTTP client, and consumers appear to ack long after finishing. Investigation is COMPLETE (root causes below are verified against code and pod logs from `ffxiv-census-worker-proxy-id-sweep-c4f46cb69-jm9l9`, `-consumer-55cc6d8f5b-wqh4l`, `-retry-7d7f59cd48-gxdm6`). Goal: faster consumption, fewer errors, correct Lodestone rate-limit management, and NEVER discard a message unless Lodestone says the character/achievement page does not exist or is private (404/403 genuine).

## Root causes (verified this session)

1. **`republishMain` breaks routing keys** — `infrastructure/rabbitmq/queue.go:426-433` publishes via the DEFAULT exchange with routing key `"census."+eventType`. The redelivered message arrives with `msg.RoutingKey = "census.id-sweep"`, but registries register `"id-sweep"` → `no handler registered` → `handleFailure` then parks to the nonexistent queue `census.census.id-sweep.failed` and Acks → **every challenge requeue silently loses its message** (72 losses seen on proxy-id-sweep at 19:29:48).
2. **Failed-queue deliveries dispatched by `msg.RoutingKey`** — `publishToFailed` (queue.go:493-505) publishes to the default exchange with key `census.<et>.failed`; `consumeWorker` (queue.go:388-391) sets `job.Type = msg.RoutingKey`. census-consumer (which subscribes `.failed` queues per `k8s/values.yaml` census-consumer command) gets `job.Type = "census.id-sweep.failed"` → missing handler → parks to `census.census.id-sweep.failed.failed` → Ack → **lost** (14/hour on census-consumer).
3. **`failedWorker` republishes under a non-existent binding** — queue.go:304-345 republishes to the `census` exchange with key `msg.RoutingKey` = `census.<et>.failed`; no binding matches; `mandatory=false` → broker silently drops → Ack → **lost** (`rabbitmq.failed.republished event_type: census.id-sweep.failed` in census-retry logs = lost messages). It also doesn't wait for publisher confirms.
4. **Achievement 404 retried forever** — `FetchAchievements` (`infrastructure/lodestone/client.go` ~line 431) maps HTTP 404 to a generic `HTTP 404` error instead of `contract.ErrCharacterNotFound` → retried up the ladder instead of terminal ack.
5. **`%v` instead of `%w`** — `domain/census/handler/idsweep.go:133`, `character.go:89`: wraps the Lodestone challenge error with `%v`, breaking `errors.As` downstream (`CheckTarget` cooldown path never fires on the dual-source fallback).
6. **Client ignores its own rate limiter** — `doRequest` (client.go:228-358) never calls `c.rateLimiter.WaitUntilAvailable` before requests; on 429 it sets a 30s pause then in-client-retries after 500ms-2s ignoring the pause; on 202/403-challenge it sets NO pause (the comment at `domain/census/worker/worker.go:567-569` claims otherwise). This is the "rate limiter for proxy is not functioning correctly" defect.
7. **Delayed acks** — the ack already happens immediately after the handler returns (`consumeWorker` queue.go:393-398; nothing runs between `worker.job_done` and Ack). The unacked window is inflated INSIDE processing: provider pauses (30s after any 429), `replaceProxy` inside `processJob` (`waitForProxy` backoff 5-60s, triggered en masse by the proxy churn from defect 6), in-client retry loops (4 × 10-30s timeouts = the observed 1m-2m43s jobs), and range id-sweep jobs holding one claim for the whole range. Also `census-consumer` shares ONE client (1 rps token bucket, `maxSafeRate=1.0` client.go:31,163) across its 10 goroutines → every request waits ~10s (the exactly-10.0s job durations in logs). Fixes: 6 removes the churn, and new `queue.slow_ack` logging makes residual waits visible.
8. Retry-park TTL dead-lettering (`x-dead-letter-exchange` on failed queues, queue.go:104-107) works and is kept: parked messages expire back to the main queue automatically.

## Behavior contract (user decisions)

- Lodestone genuine 404 / 403 → acknowledge, never retry. ONLY these discard.
- 202, timeout, 5xx, 429, connection errors → retry queue with exponential backoff (5s → cap 1h), retry forever until success or 404/403. No attempt-based dead-lettering for retryable failures.
- Malformed payload (undecodable / invalid range) → dead park immediately (poison; the only other park).
- Success → DB store → publish child jobs → ack immediately.
- Retry-consumer republishes under canonical routing keys; unknown event types dead-park, never drop.

## Approach (ordered; suite green after each step)

### A. Queue routing + retry semantics — `infrastructure/rabbitmq/queue.go`, `port/contract/queue.go`

1. `port/contract/queue.go`: add sentinels `ErrPoisonPayload = errors.New("poison payload")` and `ErrNoHandler = errors.New("no handler registered")` with doc comments.
2. Add `func canonicalEventType(et string) string` — strips a trailing `.failed` (`"id-sweep.failed"` → `"id-sweep"`); unit-test it.
3. `consumeWorker` (queue.go:357-402): pass explicit consumer tags — `ch.Consume(queueName, et, ...)` (line 371) and in `failedWorker` `ch.Consume(fq, fq, ...)` (line 287). Tags are unique per channel. Build the job as `Type: canonicalEventType(msg.ConsumerTag)` — NEVER `msg.RoutingKey` (default-exchange publishes carry queue-name keys). This fixes causes 1-3 for every consumer at once.
4. `handleFailure(ctx, msg, jobType, handlerErr)` — take the normalized `jobType` parameter (from `consumeWorker`) instead of deriving from `msg.RoutingKey`; failedQueue stays `"census."+jobType+".failed"` (for failed-queue redeliveries this re-parks into the same queue with attempts+1 — correct).
5. Replace `failureAction` (queue.go:407-422) with `retryBackoffSec(attempts int) int` = `min(5*2^(attempts-1), 3600)` — no permanent outcome. In `handleFailure`: if `errors.Is(handlerErr, contract.ErrPoisonPayload)` or `errors.Is(handlerErr, contract.ErrNoHandler)` → log ERROR + publish to `"census."+jobType+".dead"` + Ack; else → `publishToFailed` with `retryBackoffSec(attempts)` + Ack. Delete the `permanent` branch and `q.maxAttempts`'s use there.
6. Delete the `IsChallenge` fast path (queue.go:448-460) and `republishMain` (queue.go:424-433); delete `contract.IsChallenge` (`port/contract/proxy_scan.go:57-64`) and `TestIsChallenge` — challenges now flow the standard retry path (worker's `CheckTarget` branch at worker.go:585-595 still cools the proxy destination in DB first).
7. `failedWorker` (queue.go:273-354): enable confirms on its channel; canonical event type = `canonicalEventType(msg.ConsumerTag)`; validate it is in `eventTypes()` — unknown → park to `<fq>.dead` + ERROR; republish to `exchangeMain` with the CANONICAL key and wait for the deferred confirmation before Acking; dead-park threshold = `attempts >= q.maxAttempts` (repurposes `maxAttempts`=50 from `QUEUE_MAX_ATTEMPTS` as the failedWorker safety-net park threshold, replacing const `maxFailedAttempts=100`; `rabbitmq.New` signature unchanged).
8. `publishToFailed` (queue.go:493-505): wait for broker confirmation like `Publish` does (factor the `q.mu` + `PublishWithDeferredConfirmWithContext` + return-drain sequence into a shared `publishConfirmed(exchange, key string, pub amqp.Publishing) error` used by both `Publish` and `publishToFailed`).

### B. Worker dispatch guard — `domain/census/worker/worker.go`

- In `RunEvents.processJob` (worker.go:120-123) and `proxyWorkerLoop` (worker.go:545-548 and the retry lookup ~629-632): wrap the missing-handler error with `contract.ErrNoHandler` (`fmt.Errorf("%w: no handler registered for event %s", contract.ErrNoHandler, job.Type)`), keep the `worker.missing_handler` ERROR log. Queue then dead-parks instead of dropping.

### C. Client status semantics — `infrastructure/lodestone/client.go`

1. `doRequest` before `c.limiter.Wait(ctx)` (line ~234): `if c.rateLimiter != nil { if err := c.rateLimiter.WaitUntilAvailable(ctx, contract.ProviderLodestone); err != nil { return nil, 0, err } }` — pauses become real (fixes cause 6).
2. 429 branch (lines 285-313): keep `rateLimiter.Pause(ProviderLodestone, RetryAfter>0 ? RetryAfter : rateLimitPause, ...)`; return the typed `ProxyCheckError{Kind: CheckTarget}` immediately — delete the in-client retry/backoff for 429 (queue retry ladder takes over).
3. 202 branch (315-327) and 403-challenge branch (329-350): add `c.rateLimiter.Pause(ProviderLodestone, RetryAfter>0 ? RetryAfter : 5*time.Second, "lodestone challenge")` before returning.
4. `FetchAchievements` (~line 431): `if statusCode == http.StatusNotFound { return nil, contract.ErrCharacterNotFound }` (fixes cause 4). Genuine 403 → `&AchievementSummary{Private: true}` stays.
5. Timeout/dial in-client retries stay unchanged.

### D. Handler terminal semantics — `domain/census/handler/`

1. `achievement.go` Handle: on `errors.Is(err, contract.ErrCharacterNotFound)` → `h.census.MarkCharacterDeleted(ctx, p.CharacterID, time.Now().UTC())` (mirror `character.go:64-71`) and return `nil, nil` (ack). Wrap the decode error (lines 34-35) with `errors.Join(contract.ErrPoisonPayload, err)`.
2. `idsweep.go`: wrap decode error (52-53) and the `p.From > p.To` validation (55) with `errors.Join(contract.ErrPoisonPayload, ...)`; change `%v` → `%w` at line 133.
3. `character.go`: `%v` → `%w` at line 89; wrap decode error (48-49) with `errors.Join(contract.ErrPoisonPayload, err)`.

### E. Retry-worker verbosity — `k8s/values.yaml`, queue logging

- census-retry instance in `workers.instances`: add an `env:` block with `LOGGING_LEVEL: "debug"` and the preserved `RABBITMQ_URL` value (instance `env` replaces `workers.defaults.env` wholesale — both keys required; exact YAML shape documented in the scout excerpt below). `LOGGING_LEVEL` maps to `logging.level` via Viper (`config/config.go:379-382`, `infrastructure/logging/logger.go:29-41`).
- failedWorker lifecycle logs (from A7): `rabbitmq.failed.received` DEBUG (canonical type, attempts, queue), `rabbitmq.failed.republished` INFO (canonical type, attempts), park ERROR.
- Add `queue.slow_ack` WARN in `consumeWorker` when claim→ack exceeds 30s (event_type, attempts, handler duration) — makes any residual delayed-ack visible cluster-wide.

### F. Docs (repo rule: docs stay in sync)

- `docs/queue.md`: new retry semantics (infinite retryable ladder, dead park = poison/no-handler/failedWorker threshold 50, canonical routing keys), `docs/lodestone.md`: 404→ErrCharacterNotFound for achievements, challenge/429 pauses. No queue/topology migration; existing parked messages flow through the fixed failedWorker.

## Critical files & anchors

- `infrastructure/rabbitmq/queue.go` — consumeWorker:371,388-391; failedWorker:273-354; failureAction:407-422; republishMain:424-433 (delete); handleFailure:439-490; publishToFailed:493-505; eventTypes:560-567.
- `infrastructure/lodestone/client.go` — doRequest:228-358 (limiter wait ~234; 429:285-313; 202:315-327; 403-challenge:329-350); FetchAchievements 404 ~431.
- `domain/census/worker/worker.go` — dispatch guards 120-123, 545-548, 629-632.
- `domain/census/handler/{achievement,idsweep,character}.go` — decode wraps, `%v`→`%w`, achievement 404 terminal.
- `port/contract/queue.go`, `port/contract/proxy_scan.go` — sentinels; IsChallenge deletion.
- `k8s/values.yaml` — census-retry env block.

## Tests (strict TDD: each new behavior red first)

- `queue_test.go`: rewrite `TestFailureAction` → `TestRetryBackoffSec` (always ≥5, caps 3600, never parks); add `TestCanonicalEventType`; add a `failureDecision(handlerErr, jobType)` pure helper (extract from handleFailure) tested for: poison→dead, no-handler→dead, challenge/timeout/429/5xx→failed+backoff.
- `client_test.go` (httptest patterns exist): FetchAchievements 404→`ErrCharacterNotFound`; 202→challenge error + limiter paused; 429→immediate typed return + pause (no second HTTP request — assert request count).
- Handler tests (existing fake patterns in `character_test.go`/`idsweep_test.go`): achievement 404 → MarkCharacterDeleted called + nil,nil; decode failure → `errors.Is(err, contract.ErrPoisonPayload)`; dual-source fallback error unwraps to `ProxyCheckError` via `errors.As` (the `%w` fix).
- Postgres repo tests: none needed (CooldownDestination SQL intentionally unchanged — see Assumptions).

## Verification

1. `make fmt && make lint && make test`; plus `go test -race ./infrastructure/rabbitmq/... ./infrastructure/lodestone/... ./domain/census/...`.
2. Local e2e (docker RabbitMQ + Postgres, `make build`, run `./bin/ffxiv-census migrate up` if needed — runtime migrations auto-run):
   - `./bin/ffxiv-census consume --events id-sweep,id-sweep.failed -c 2` (check `publish --help` for exact publish flags; cron-publish uses the same command).
   - Publish an id-sweep job for character id `1` (nonexistent → real 404): expect `handler.id_sweep.done` + ack; queue depth 0 via `rabbitmqctl list_queues name messages_ready messages_unacknowledged`.
   - Publish a malformed payload routed to `id-sweep`: expect it lands in `census.id-sweep.dead` and an ERROR log — NOT dropped.
   - Publish a valid payload directly into `census.id-sweep.failed`: expect `handler.id_sweep.start` in logs (normalization proof — previously `worker.missing_handler`).
3. Cluster (user applies helm): `kubectl logs -f deploy/ffxiv-census-worker-census-retry` shows DEBUG lifecycle logs with canonical event types (`id-sweep`, not `census.id-sweep.failed`); zero `no handler registered` on all workers; census-consumer `worker.missing_handler` gone; `queue.slow_ack` warnings identify any remaining slow acks.

## Assumptions & contingencies

- **Rotation-on-destination-rejection is intended design** (documented `docs/proxy.md:119-122`): `CooldownDestination` releasing the proxy lock so redeliveries land on fresh identities is deliberate for free-proxy pools. Investigation flagged it as a limiter defect; rejected — the client-side pause fixes (C1-C3) are the actual defects. If the user prefers hold-and-wait instead of rotation, that is a follow-up to `proxy_scan.go:417-428` + worker.go:585-595.
- `maxAttempts` (50) becomes the failedWorker dead-park threshold: legacy messages already parked with attempts ≥50 will dead-park on first touch after deploy (inspectable, not dropped). If the user prefers, drain failed queues before deploying.
- Challenge pause default (no Retry-After hint) = 5s; 429 default stays 30s. Config values (`[lodestone] rate_limit = 1.0`, per-connection clients) unchanged — the observed ~10s/request on census-consumer is the shared 1-rps bucket across 10 goroutines, by design; raise `[lodestone] rate_limit` only if the user accepts direct-IP risk (code caps at `maxSafeRate=1.0` — raising requires lifting that cap deliberately).
- Scanning path (`infrastructure/proxy/checker.go`) bypasses rate limiting — separate subsystem, intentionally untouched here; flagged for a follow-up if scan traffic provokes Lodestone 429s.
