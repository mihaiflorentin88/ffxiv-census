# Adaptive, replica-scalable proxy scanning

Date: 2026-09-09
Status: Written specification approved by the user on 2026-09-09; implementation plan pending review.

## Objective

Maximize the pool of recently verified, functioning proxies by improving candidate selection, maintaining fresh health evidence, and using additional scanner replicas for additional work. Optimize fresh availability and successful consumer use, not the number of rows carrying a historical `active` label.

Every proxy in the database has worked at some point according to the user. A NULL `last_alive_at` means missing locally recorded success, not that the proxy never worked. Neither missing history nor repeated failure removes a proxy from background scanning.

No probe can guarantee that a proxy remains alive after verification. The contract is explicit freshness plus verification before destination-specific use.

## Current evidence and defects

A read-only database snapshot during design found 18 active proxies, 907 inactive proxies, and 1,626,903 dead proxies. All dead rows had 5–9 consecutive failures; 21,254 had locally recorded success. These are snapshots, not population constants or recovery probabilities.

Current code:

- `domain/proxy/worker/scan.go` splits workers into regular and dead pools and prefetches records.
- `infrastructure/postgres/repository/proxy.go` hardcodes regular eligibility windows and a seven-day dead window. Claims stamp `last_scanned_at`, so that field mixes claims and completions.
- `config/config.go` declares `DeadScanIntervalDays`, but repository eligibility does not use it.
- `ListActive` filters by stored status without freshness. Acquisition prefers recent success but falls back to any active row.
- `domain/proxy/hub.go` checks before handout when a checker is configured.
- `infrastructure/proxy/checker.go` checks Lodestone, accepts HTTP 200 without consuming and validating the body, and has a SOCKS fallback whose dial ignores context.
- `UpdateStatus` accepts unversioned updates, allowing competing observations to overwrite one another.

Strict cohort-first sorting with continuously eligible rows is rejected: the first cohort can starve all others. A claim timestamp is not an in-flight lease. Fixed long background windows are rejected because they can leave extra replicas idle.

## Chosen algorithm

Use three logical queues with borrowable concurrent-worker reservations. Scheduling is domain policy; PostgreSQL implements atomic selection and persistence.

| Queue | Candidates | Order |
| --- | --- | --- |
| Verification | Proxies whose latest conclusive general-health result succeeded, including those whose freshness has expired | Earliest verification deadline, then ID |
| Recovery | Failed proxies with general-health success within the last hour and a due retry | Earliest retry deadline, then ID |
| Background | All proxies whose latest conclusive general-health result is not success | Oldest completed attempt, NULL first, then ID |

A recovery candidate may also belong to background. A shared per-proxy lease excludes concurrent claims across queues. Any completed attempt advances background position, including recovery attempts. Therefore a background check can discover recovery earlier than its extra retry deadline; backoff governs additional recovery attention, not eligibility for the universal sweep.

Successful general-health checks move a proxy to verification immediately. Any later conclusive general-health failure moves it out of usable availability and into recovery if its last success is recent, otherwise background alone. Existing inactive/dead classification thresholds may remain for historical classification, but never control access to the background sweep.

### Active verification and freshness

Defaults:

- Start a recheck 60 seconds after the preceding successful verification completed.
- Successful evidence expires 120 seconds after completion.
- Full general-health check deadline: 10 seconds.

A usable active proxy has a successful latest accepted conclusive general-health observation, a verification age strictly below the freshness limit, and no newer accepted general-health failure. Expiration removes eligibility without falsely recording a network failure. Expired successful records remain in the verification queue.

Apply the predicate to active lists, active counts, random-active selection, acquisition, and user-facing active summaries. Raw stored status is historical state, not the authoritative live count. Historical status can remain `active` after expiry, but all live-facing results must classify it as stale rather than alive. Raw `last_alive_at` remains historical evidence; use the new general-health verification timestamp for this predicate.

Keep already fresh evidence available during a normal recheck. Once expired, fail closed even if verification capacity is overloaded. One completed conclusive failure removes availability immediately; no requirement for multiple failures before removal.

### Recovery policy

After failure following success, additional retry delays are 1, 2, 4, 8, and then 15 minutes, capped at 15 minutes. Compute the next deadline from completion, not claim time. Success resets the retry sequence. Inconclusive results do not increment it.

Eligibility for this extra attention ends one hour after the latest successful general-health check. A new success restores the same treatment regardless of source or missing historical data. These defaults are explainable heuristics, not learned recovery probabilities.

### Capacity allocation

Default reservations are verification 10%, recovery 45%, background 45% of per-replica concurrency. They allocate concurrent checks, not equal completion counts: a ten-second timeout consumes more capacity than an immediate refusal.

Each replica has one dispatcher and a bounded executor. Before admitting borrowed work, fill any due queue below its reservation. Allocate remaining slots among nonempty queues in round-robin order. Empty queues lend all unused capacity. When reserved work arrives, reclaim borrowed slots as checks finish; do not cancel running checks. Discard accumulated entitlement for empty queues rather than creating a burst when they return.

For concurrency of at least three, reserve at least one slot per queue and distribute remaining slots by the configured weights using largest remainders with deterministic ties. For one or two workers, use weighted round-robin admission with carried credits across completions; do not permanently round a queue to zero. Percentage reservations are approximate at small concurrency and during transitions, not promises about instantaneous completion rates.

No intentional idle wait is allowed while an admissible queue has work and an executor slot is free. Empty due queues must not cause the continuous background queue to sleep. Endpoint-health pauses, shutdown, database failures, active leases, and a configurable one-second per-proxy repeat floor are legitimate reasons for temporarily unavailable work.

Checks are nonpreemptive. Verification can wait for borrowed work to finish; freshness still expires on time. Reservation fairness assumes progressing database operations and bounded check lifetimes. It is not a fixed sweep-time guarantee under unlimited insertion or infrastructure failure.

## General-health check: ipify

Default URL: `https://api64.ipify.org?format=json`.

The official ipify site documents IPv4/IPv6 support and unlimited use, including millions of requests per minute. This supports choosing it over an arbitrary public page, but is not an SLA. One direct design-time request returned HTTP 200, 22 bytes, in approximately 0.70 seconds. This was not a proxy or load benchmark.

Success requires:

1. The request uses the selected proxy with no direct fallback.
2. Normal HTTPS certificate and hostname validation succeeds.
3. Redirects are rejected.
4. The HTTP status is 200.
5. The complete body is at most 1 KiB and is a single valid JSON object containing a string `ip` that parses as an IP address. Reject trailing non-whitespace data. Additional JSON fields are allowed.
6. All phases, including dialing, TLS, body consumption and validation, complete within the ten-second deadline.

Do not require the echoed address to match the advertised proxy address: a proxy may use different egress. Send no-cache request directives. Do not infer anonymity from the echoed IP. Latency measures the complete successful check.

The URL is configurable but must use HTTPS and satisfy the same JSON contract. Do not add separate short TCP pre-probes: they duplicate successful connections and introduce an unmeasured false-negative cutoff. Context cancellation must terminate or otherwise strictly bound underlying network activity for every supported protocol, including SOCKS4. Starting abandoned dial goroutines is not an acceptable timeout implementation.

### Outcome classification and endpoint protection

Represent success, conclusive proxy failure, and inconclusive check separately. Preserve typed failure reasons instead of interpreting log strings.

- Refused proxy connections and proxy-protocol negotiation failures are conclusive proxy failures.
- Completed request deadline failures count as proxy failures only while the endpoint-health guard considers the target healthy. Attribution remains imperfect; record the phase and reason.
- Target HTTP errors, including 429 and 5xx, invalid endpoint payloads, and ambiguous target TLS failures are inconclusive. They never promote the proxy or increment failure history. A 403 from ipify does not prove the proxy is dead.
- Caller shutdown/cancellation and persistence failures are inconclusive and never increment failure history.
- An inconclusive attempt does not renew success freshness or erase a still-fresh earlier success. It advances attempt position and schedules the next attempt no sooner than 60 seconds later. Honor a longer valid Retry-After for that proxy/endpoint.

Each replica performs a low-rate direct control request to the configured endpoint at startup and every 30 seconds, with jitter. Until a control request succeeds, pause general-health claims. A failed control request pauses new general-health claims until a later successful control request. Permit at most one control request per replica in flight. Control success never marks a proxy healthy; it only establishes a local baseline. Do not rotate endpoints to evade throttling.

Tag checks with a local guard generation; results from checks spanning an observed unhealthy generation cannot commit a conclusive failure. Transport-refusal diagnostics can still be logged. Other replicas independently detect endpoint trouble. This guard reduces correlated misclassification; it cannot distinguish all destination outages from proxy-specific path failures. During a pause, existing evidence expires normally and no stale row is advertised as fresh.

## Lodestone-specific usability

Split checker wiring in the service locator: general-health checks use ipify; Lodestone acquisition keeps a separately configured Lodestone checker. Do not mutate the shared checker URL and accidentally change both purposes.

Before handing a proxy to a Lodestone consumer, require fresh general health and a successful destination check. A successful Lodestone check establishes destination usability only and does not renew ipify verification time.

Distinguish destination rejection from general failure:

- Lodestone 429, 403, and target 5xx do not mark general health dead. Exclude the proxy from Lodestone acquisition for a default 60-second cooldown, or a longer valid Retry-After. Preserve existing application-level rate limiting; cooldown is not permission to bypass it with another identity.
- Unambiguous proxy connection/protocol failures invalidate general availability and fence older general-health results.
- Ambiguous target errors withhold handout and apply destination cooldown, without inventing a general-health failure.
- Application parsing/business errors are not proxy-health observations.

Consumer-detected general failures use the same observation-version discipline as scanner results. Capture the general-health version before the consumer check and require that version when persisting a failure; discard an observation superseded by an accepted newer result. An accepted failure increments the version and invalidates older scan ownership. Update every caller of the existing failure API so destination responses cannot silently become global proxy death.

## Claims, observations and crash recovery

Store scan lease ownership independently of existing consumer-use locks. Required persisted scheduling state consists of:

- Last completed attempt timestamp, separate from the legacy mixed claim timestamp.
- Latest successful general-health verification timestamp.
- Next verification/retry eligibility timestamp and recovery step.
- General-health observation version.
- Unique scan lease token, lease expiry, and version captured at claim.
- Lodestone acquisition cooldown deadline.

Use database time for ordering, eligibility, lease expiry, cooldown and freshness comparisons. Application monotonic time bounds network execution.

Claim protocol:

1. In a short transaction, select available candidates in queue order using `FOR UPDATE SKIP LOCKED`, excluding unexpired scan leases and per-proxy attempt cooldowns.
2. Persist a new token, captured observation version, and lease expiry; return claimed records.
3. Commit before network work. Never hold a database connection across a check.
4. Complete using a compare-and-set requiring the same token, captured version and an unexpired lease. Atomically update observation, timestamps, retry state and lease release. Increment the observation version for an accepted conclusive result; an inconclusive result preserves the conclusive observation and its version.
5. A superseding consumer general failure increments the version and invalidates the older claim. A late completion cannot overwrite it.

Default lease duration is 30 seconds. Claim only for currently free executor capacity; do not preserve the old large claimed prefetch buffer. A database persistence stall may cause lease expiry and discard a result; it must not make stale ownership valid. An expired claim is available for retry without a separate reaper. Never count a claim or discarded completion as a completed observation.

On graceful shutdown, stop claims, cancel checks, and best-effort release owned claims using a bounded cleanup context. Otherwise leases expire. Duplicate network attempts remain possible around expiry and process stalls; fenced persistence, not exactly-once networking, is the guarantee.

Use index-supported queue selection, not a dynamic full-table probability sort. Migration indexes must support verification/recovery deadlines and background completion order with deterministic ID ties. Validate representative plans and claim latency against a realistically sized local dataset, including leased rows and concurrent claims. Do not run write-heavy benchmark queries against production.

## Configuration and clean cutover

Retain existing `proxy.test_url` and `proxy.test_timeout` as general-health settings; set defaults to the ipify URL and `10s`. Add a separate consumer destination-check URL retaining the current Lodestone URL and a ten-second check timeout. Keep application request timeouts separate.

Expose scheduler settings under `proxy.scan`: verification interval `60s`, freshness TTL `120s`, recovery base `1m`, recovery cap `15m`, recovery horizon `1h`, reservation weights `10/45/45`, lease duration `30s`, minimum repeat interval `1s`, inconclusive retry `60s`, endpoint control interval `30s`. Expose destination cooldown under `proxy.consumer`, default `60s`. Derive doubled recovery intervals from base and cap; do not introduce a list of arbitrary per-failure settings.

Follow the existing Viper environment naming convention; document exact generated names in the implementation plan and operational docs. Parse once at configuration load. Invalid URLs, nonpositive durations, weights not summing to 100, freshness not exceeding verification interval plus check timeout, or a lease not exceeding check timeout must fail startup clearly. Do not silently substitute defaults for invalid supplied values.

Remove unused `dead_scan_interval_days`, obsolete regular/dead scan contracts, `--dead-scan-percentage`, split-pool implementation and corresponding deployment arguments. Migrate all callers and fakes without compatibility shims. Retain `-c` as per-replica concurrent network-check capacity and correct its help text if needed.

Preserve the user's five replicas, concurrency 150, resource settings and unrelated values. Instance environment lists replace chart defaults rather than merge; retain required entries. Do not modify the unrelated untracked logging test.

## Migration and rollout

Add scheduling state through an embedded Goose migration. Do not infer ipify success from historical Lodestone results or mixed claim timestamps. Existing rows start without fresh general verification and enter the initial background sweep. Preserve historical last-alive data and failure counts for inspection; do not seed fake recent successes. Initial availability may drop until genuine ipify checks succeed.

Initialize new completion timestamps as unknown. Deterministic ID order produces an initial sweep; subsequent completions establish real rotation. Do not grant recent-recovery priority from unverified historical data at cutover.

Old and new scanner/consumer binaries must not run together during semantic cutover: old writers do not understand leases, observation versions or freshness. Stop old proxy writers and consumers, apply the migration through the new binary, start new scanners, observe fresh availability, then start updated consumers and update live-facing reads. Document a coordinated deployment procedure rather than assume a rolling upgrade is safe. No release or production rollout is authorized by writing this spec.

Do not roll back to old writers against the new semantics while traffic continues. Operational rollback requires stopping affected processes and an explicitly reviewed database/application rollback; never automatically drop health evidence.

## Replica scaling and resource limits

No fixed database shards or central dispatcher are required. Each replica applies the same local reservation policy and claims from shared queues. Scale-down requires no rebalance; expired leases recover work.

Five replicas at concurrency 150 offer 750 concurrent check slots. With ten-second occupancy for every check, an idealized network-only model yields 75 checks/second before overhead. At about half capacity, a 1.6M-row background population takes about 12 hours; this is illustrative, not a production bound. Recovery/background overlap and successful promotions change the workload. Additional replicas improve throughput only while CPU, network, destination and database capacity permit it.

PostgreSQL is shared and limited to 100 connections. Use a scanner-specific default maximum pool of two open connections and one idle connection per replica, since network checks release connections. At five replicas this permits ten scanner connections, not fifty. Treat pool size as tunable after measurement, not proof of adequate throughput. Before any scale-up, budget replica count times per-pod maximum alongside other applications and explicit operational headroom. An arbitrary replica count cannot be promised safe.

Do not raise concurrency automatically on timeout spikes. Record check-phase timing and completed throughput so local saturation can be distinguished from increased useful capacity.

## Architecture and affected surfaces

- `port/contract`: claim/completion and typed check-outcome contracts; proxy scheduling state.
- `domain/proxy`: queue classification, retry transitions, fresh availability and destination/general failure distinction.
- `domain/proxy/worker`: bounded executor, borrowable reservations, endpoint guard and shutdown behavior.
- `infrastructure/proxy`: cancellation-safe network checks and endpoint-specific response validation.
- `infrastructure/postgres`: atomic lease/version persistence, queue indexes, freshness queries and migrations.
- `container`: separate general and destination checkers through existing lazy service-locator patterns.
- `mock`: fake implementations matching the new contracts and transitions.
- `config`, `cmd/cli`, `k8s`: validated settings, accurate flags and coordinated deployment configuration.
- HTTP/UI and all active-selection consumers: consistent fresh counts and eligibility; preserve historical data without calling stale entries alive.
- `docs/proxy.md` and affected operational/configuration guides: new semantics and rollout guidance.

Keep domain code infrastructure-independent and all builds compatible with `CGO_ENABLED=0`. This is one cohesive feature, not a new generic job-queue framework.

## Verification and acceptance

Use strict red/green TDD for behavior changes. Permanent tests defend uncertain concurrency, fairness, deadlines and health transitions, not source text, SQL spelling or incidental defaults.

Required behavioral coverage:

1. Sustained recovery work cannot starve background; due verification receives its reservation after bounded in-flight checks complete.
2. Empty queues lend capacity; one/two-worker configurations do not permanently starve a queue.
3. Recovery delays progress and cap; success resets them; history uncertainty never excludes background candidates.
4. Every live-facing list/count/selection rejects expired evidence with scanners stopped; exact expiry boundary is excluded.
5. HTTP headers without a valid complete response, oversize/trailing bodies, invalid IP data and redirects fail the validation contract.
6. Different advertised and egress addresses remain valid; no direct proxy fallback occurs.
7. Dial, TLS and body stalls respect the deadline on every protocol without unbounded lingering operations.
8. Cancellation, endpoint errors, guard pauses and database failures do not increment proxy failure history or renew freshness.
9. Concurrent PostgreSQL claims exclude valid leases; expiry permits recovery; stale tokens and superseded versions cannot commit.
10. Consumer transport failure fences an older scanner success; destination rate limiting does not label general health dead.
11. Migration never grants fresh ipify status from historical data; obsolete callers/configuration are removed.

Use real PostgreSQL for persistence races and lease tests, an isolated test database, deterministic clocks where domain policy allows, controlled local HTTP/proxy fixtures, and `go test -race` for worker behavior. Do not use the user's unrelated PostgreSQL instance or silently accept skipped integration tests as passes.

Run a controlled one-, two-, and five-replica experiment at equal per-replica concurrency against successful, refused and delayed local endpoints. Measure accepted completed checks, background progress, verification lateness and database connections. Kill a replica mid-check. Require increased completed throughput when the controlled environment has spare resources; report measured scaling rather than asserting linearity. Public ipify receives only modest smoke checks, never the synthetic load experiment.

Measure production usefulness through fresh general-health pool size over time, successful destination handouts, proxy-related consumer failures, recovered/newly successful proxies per worker-second, oldest completed-attempt age with NULL counts, verification lateness, outcome reasons, and accepted completion throughput. Use existing logging/metrics mechanisms; no new monitoring platform or autoscaler is in scope.

Existing unrelated full-suite errors from the untracked logging test and a missing local golangci-lint executable were reported earlier. Preserve that work and report verification limitations accurately; do not claim a fully green suite without evidence.

## Alternatives and non-goals

Pure FIFO supplies coverage but gives recent recoveries no extra attention. Strict priority without reservations starves lower-ranked candidates. Long fixed dead windows defeat capacity-driven sweep speed. Per-proxy UCB or discounted bandits require more meaningful outcome history and validation than the current fields provide; they are not part of this implementation.

No proxy deletion, provider-quality blacklist, automatic Kubernetes scaling, additional public fallback endpoints, separate TCP pre-probe, learned probability model, or guarantee of universal destination reachability is included.

## References

- ipify official API and published usage claims: https://www.ipify.org/
- PostgreSQL SELECT locking and SKIP LOCKED: https://www.postgresql.org/docs/current/sql-select.html
- PostgreSQL explicit consistency and lock limitations: https://www.postgresql.org/docs/current/applevel-consistency.html
- Shreedhar and Varghese, Efficient Fair Queueing using Deficit Round Robin: https://openscholarship.wustl.edu/cgi/viewcontent.cgi?article=1339&context=cse_research (fairness background; this design uses concurrent-slot reservations rather than claiming packet-DRR guarantees).
- Garivier and Moulines, On Upper-Confidence Bound Policies for Non-Stationary Bandit Problems: https://arxiv.org/abs/0805.3415 (alternative considered, not an implemented model).
