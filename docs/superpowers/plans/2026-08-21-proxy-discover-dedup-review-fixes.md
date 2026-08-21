# Proxy Discover Database Deduplication and Review Fixes

## Context

Prevent the `proxy discover` cronjob from publishing a `new-proxy` event when PostgreSQL already contains the same exact `(protocol, ip, port)` tuple; the publisher remains read-only with respect to proxy rows and writes only to RabbitMQ. The `new-proxy` consumer must independently check the same tuple before calling the repository write path, while the repository write remains conflict-safe for two duplicate deliveries racing after both checks. Add an idempotent PostgreSQL migration that removes any legacy exact duplicates and restores the existing tuple uniqueness invariant.

The uncommitted review also found correctness gaps to repair: initial proxy claims leak when proxy-client construction fails; proxy-mode Lodestone/Tomestone clients never receive their per-worker provider cooldown limiter although the worker assumes they do; rotating discovery can stop after selecting an already-attempted proxy while another active proxy exists; the shared Tomestone request bucket has per-client adaptive state plus an unclamped construction path; repository test cleanup silently fails because it truncates tables removed by migrations; and streaming validation still permits a nil-consumer request side effect or incomplete JSON tails. The Lodestone documentation also contradicts the implementation by claiming tokens are charged per HTTP request even though the forked godestone `FetchCharacter` performs two internal requests behind one scraper call and exposes no request hook.

## Approach

0. **Persist the approved execution plan before production edits.** Copy this plan verbatim to `docs/superpowers/plans/2026-08-21-proxy-discover-dedup-review-fixes.md`; keep the implementation and verification results aligned with that record.

1. **Lock the proxy identity and insert-only repository contracts with failing tests.** Treat identity as the exact tuple `(protocol, ip, port)`, matching `UNIQUE(protocol, ip, port)` in `00008_create_proxies.sql`; do not add case folding, whitespace normalization, or “same IP/port across protocols” matching. In `port/contract/proxy.go`, add the read-only method and cleanly replace the misleading proxy-specific `Upsert` contract:

   ```go
   type ProxyRepository interface {
       Exists(ctx context.Context, protocol, ip string, port int) (bool, error)
       InsertIfAbsent(ctx context.Context, rec ProxyRecord) (id int64, inserted bool, err error)
       // existing Get, status, scan, lock, and random-selection methods...
   }
   ```

   Migrate every proxy `Upsert` caller found in `domain/proxy/service.go`, `domain/proxy/*_test.go`, `infrastructure/httpclient/rotating_proxy_client_test.go`, and test setup code to `InsertIfAbsent`; do not leave an `Upsert` alias. In `mock/repository/proxy.go`, implement exact tuple matching and return `(0, false, nil)` without modifying an existing record. Add injectable `ExistsErr` / `InsertErr` fields plus `ExistsCalls` / `InsertCalls` counters so CLI and service tests can prove fail-closed and no-write behavior.

2. **Make PostgreSQL checks read-only and inserts conflict-safe.** In `infrastructure/postgres/repository/proxy.go`, implement:

   ```go
   func (r *ProxyRepository) Exists(
       ctx context.Context, protocol, ip string, port int,
   ) (bool, error) {
       db, err := r.driver.Acquire(ctx)
       if err != nil {
           return false, err
       }
       var exists bool
       err = db.QueryRowContext(ctx, `
           SELECT EXISTS (
               SELECT 1
               FROM proxies
               WHERE protocol = $1 AND ip = $2 AND port = $3
           )`,
           protocol, ip, port,
       ).Scan(&exists)
       if err != nil {
           return false, fmt.Errorf("proxy exists: %w", err)
       }
       return exists, nil
   }
   ```

   Implement `InsertIfAbsent` as one `INSERT ... ON CONFLICT (protocol, ip, port) DO NOTHING RETURNING id`. `sql.ErrNoRows` means another delivery already inserted the tuple and must return `(0, false, nil)`; any other error is wrapped as `proxy insert if absent`. Do not update country, anonymity, uptime, source, status, or timestamps on conflict—the user-required consumer behavior is “no database write for an already-existing proxy.” Retain `status='inactive'`, `fail_count=0`, and current UTC timestamps for a newly inserted row.

3. **Add an idempotent cleanup migration before relying on the tuple invariant.** Create `infrastructure/postgres/migration/query/00012_deduplicate_proxies.sql`. Its `Up` keeps the most recently updated row (`updated_at DESC, id DESC`) for each exact tuple, deletes the other rows, then restores the tuple uniqueness constraint only if PostgreSQL has no equivalent unique constraint:

   ```sql
   -- +goose Up
   WITH ranked AS (
       SELECT id,
              ROW_NUMBER() OVER (
                  PARTITION BY protocol, ip, port
                  ORDER BY updated_at DESC, id DESC
              ) AS duplicate_rank
       FROM proxies
   )
   DELETE FROM proxies p
   USING ranked r
   WHERE p.id = r.id
     AND r.duplicate_rank > 1;

   DO $$
   BEGIN
       IF NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conrelid = 'proxies'::regclass
             AND contype = 'u'
             AND pg_get_constraintdef(oid) = 'UNIQUE (protocol, ip, port)'
       ) THEN
           ALTER TABLE proxies
               ADD CONSTRAINT proxies_protocol_ip_port_key
               UNIQUE (protocol, ip, port);
       END IF;
   END
   $$;

   -- +goose Down
   -- Irreversible data cleanup; retain the original tuple uniqueness invariant.
   ```

   No proxy table has a foreign-key reference in the current migrations, so deleting the losing duplicate rows requires no dependent-row rewrite. Fix `cleanTables` in `infrastructure/postgres/repository/helpers_test.go` to truncate only tables that still exist after migrations `00010` and `00011`, and include `proxies`: `proxies, characters, character_jobs, character_gear, character_milestones, milestone_achievements, census_runs`. Remove the dropped `queue_jobs` and `free_companies` names; their presence currently makes the entire ignored `TRUNCATE` fail, so repository tests are not isolated.

4. **Filter database-resident proxies inside the streaming publisher without buffering provider output.** Change the helper signature and its only production/test callers:

   ```go
   func publishDiscoveredProxies(
       ctx context.Context,
       q contract.Queue,
       repo contract.ProxyRepository,
       logger contract.Logger,
       providers []contract.ProxyProvider,
   ) (int, error)
   ```

   In `proxyDiscoverCmd.RunE`, resolve `container.Load.ProxyRepository()` and fail with `proxy repository not initialised` before fetching providers. Inside each provider callback, call `repo.Exists` before constructing `handler.NewProxyJob`; an existing tuple increments `skippedExistingForProvider` and returns `nil` without calling `Queue.Publish`. Keep publication one record at a time and do not add a run-wide `seen` slice/map, because that would reintroduce memory growth proportional to provider output.

   ```go
   var errLookupFailed = errors.New("proxy lookup failed")

   err := p.FetchProxies(ctx, func(rec contract.ProxyRecord) error {
       exists, err := repo.Exists(ctx, rec.Protocol, rec.IP, rec.Port)
       if err != nil {
           return fmt.Errorf("%w: %w", errLookupFailed, err)
       }
       if exists {
           skippedExistingForProvider++
           return nil
       }
       if err := q.Publish(ctx, handler.NewProxyJob(handler.NewProxyPayload{
           Protocol: rec.Protocol, IP: rec.IP, Port: rec.Port,
           Country: rec.Country, Anonymity: rec.Anonymity,
           Source: rec.Source, UptimePercent: rec.UptimePercent,
       })); err != nil {
           return fmt.Errorf("%w: %w", errPublishFailed, err)
       }
       publishedForProvider++
       totalPublished++
       return nil
   })
   ```

   A lookup error is fail-closed: publish nothing for that record, stop that provider, log `proxy.discover.lookup_failed`, increment the provider error count once, and continue with the next provider under the existing partial-success policy. Preserve `proxy.discover.publish_failed`, `proxy.discover.provider_failed`, and the terminal `proxy discovery failed: all providers failed (%d errors)` rule. Extend `proxy.discover.provider_done` and the final completion log with `skipped_existing`; a provider whose records are all skipped is a successful provider with zero publications. Use a recording `slog.Handler` in CLI tests to assert the lookup-failure classification and skipped counter rather than changing the helper’s `(int, error)` return contract.

5. **Make the consumer perform its own pre-insert check and preserve race safety.** In `domain/proxy/service.go::ProcessNewProxy`, call `repo.Exists` before `InsertIfAbsent`. If it returns true, log `proxy.process_new.skipped_exists` with protocol, IP, and port and return before any insert or checker call. If it returns an error, log `proxy.process_new.exists_failed` and return it. If `InsertIfAbsent` returns `inserted=false`, treat that as the concurrent-delivery race path, emit the same skipped log, and return without checking the proxy. Only an actually inserted row is loaded by ID and passed to `processProxyCheck`.

   Add service tests that seed one tuple, construct the service with a discard `slog.Logger` and nil checker, call `ProcessNewProxy`, and prove the duplicate path neither mutates stored metadata nor dereferences the checker. Add an `ExistsErr` case proving `InsertCalls == 0`. Add a PostgreSQL integration test with two concurrent `InsertIfAbsent` calls for the same tuple; exactly one must return `inserted=true`, neither may return a unique-constraint error, and the table count for that tuple must be one.

6. **Guarantee three distinct discovery-proxy attempts.** The current rotating client gives up when `SwapActive` randomly returns any previously attempted proxy even if another active proxy exists. Change `ProxyRepository.RandomActive` to accept all excluded IDs:

   ```go
   RandomActive(ctx context.Context, excludeIDs []int64) (*ProxyRecord, error)
   ```

   PostgreSQL must use `AND (cardinality($1::bigint[]) = 0 OR id <> ALL($1::bigint[]))`; the fake filters against an exclusion set before its deterministic sorted/random choice. Add `ProxyHub.RandomActiveExcluding(ctx, excludeIDs)` with the same explicit Lodestone prohibition comment; retain `RandomActive` and `SwapActive` as convenience methods delegating to it with zero or one exclusion.

   In `RotatingProxyClient.GetStream`, maintain `attemptedIDs []int64`, request the next proxy with all attempted IDs excluded, and stop after three actual request attempts or when no unattempted active proxy remains. Add a private constructor accepting `func(string, time.Duration) (contract.HTTPClient, error)` so tests inject proxy clients instead of dialing fake IPs. Tests must assert exact attempt order `[11, 22, 33]` for two transport/status failures followed by success, consumer callback errors stop after one attempt, nil callbacks make zero requests, and an empty active pool makes one direct request.

7. **Repair proxy-worker claim cleanup and per-provider cooldown injection.** Refactor initial runtime creation to reuse the same acquire/build helper as replacement, or install cleanup immediately after `NewProxy`; if Lodestone or Tomestone client construction fails, release the claimed proxy with a bounded cleanup context and return `errors.Join(constructionErr, releaseErr)` when release also fails. For replacement, failure to release/mark the previous proxy must release the newly built replacement and return the cleanup error instead of letting one owner retain two claims.

   Change `RunEventsWithProxy`, `proxyWorkerLoop`, and `replaceProxy` factory types so the newly created per-worker limiter is passed into both provider clients:

   ```go
   newLodestoneClient func(
       proxyURL string,
       limiter contract.ProviderRateLimiter,
   ) (contract.LodestoneClient, error)

   newTomestoneClient func(
       proxyURL string,
       limiter contract.ProviderRateLimiter,
   ) (contract.TomestoneClient, error)
   ```

   In `cmd/cli/consume.go`, pass that limiter to `lodestone.NewClientWithProxy` and to the Tomestone constructor. This makes the existing client-side `Pause(ProviderLodestone, ...)` / `Pause(ProviderTomestone, ...)` calls update the exact limiter consulted by `proxyWaitForProviders`; keep the worker’s current rule that it does not infer a provider from a combined handler error. Add tests proving a Tomestone 429 pauses only Tomestone, a Lodestone 429 pauses only Lodestone, and initial/replacement constructor failures release every claimed proxy.

8. **Replace Tomestone’s untyped options and per-client adaptive state with one shared request-rate controller.** `opts ...any` silently accepts unsupported values, the proxy CLI builds a shared limiter without the client’s `maxSafeRate` clamp, and separate `consecutive429s` counters can raise a shared bucket while another client is still backed off. In `infrastructure/tomestone/client.go`, add typed functional options:

   ```go
   type ClientOption func(*clientOptions)

   func WithProviderRateLimiter(l contract.ProviderRateLimiter) ClientOption
   func WithRequestRateController(c *RequestRateController) ClientOption
   ```

   Add `RequestRateController` containing the mutex, token bucket, configured/clamped rate, and global consecutive-429 count. `NewRequestRateController(configured float64)` must apply the existing default and `maxSafeRate` clamp once; `Wait`, `RecordRateLimit`, and `RecordSuccess` own all token acquisition and adaptive `SetLimit` behavior. Replace `Client.limiter`, `Client.configuredRate`, `Client.mu`, and `Client.consecutive429s` with `requestRate *RequestRateController`.

   The process-wide direct client gets its own controller through default construction. `runProxyConsumer` creates exactly one controller from `tomestoneCfg.RateLimit` and injects it into every proxy client together with the worker-local cooldown limiter:

   ```go
   sharedTomestoneRate := tomestone.NewRequestRateController(tomestoneCfg.RateLimit)
   newTomestoneClient := func(
       proxyURL string,
       limiter contract.ProviderRateLimiter,
   ) (contract.TomestoneClient, error) {
       return tomestone.NewClientWithProxy(
           tomestoneCfg,
           proxyURL,
           logger,
           tomestone.WithProviderRateLimiter(limiter),
           tomestone.WithRequestRateController(sharedTomestoneRate),
       )
   }
   ```

   Tests must prove two clients consume one shared burst token, a 429 in either client lowers the same controller, success recovery cannot exceed the shared global backoff state, and a configured rate above 20 is clamped for injected/shared use.

9. **Finish the streaming boundary review fixes.** In `infrastructure/httpclient/client.go::GetStream`, reject a nil consumer before building or sending the request; test with a counting `RoundTripper` that the request count remains zero. In the ProxyScrape and PubProxy token decoders, always consume and validate the top-level closing `}` even when the expected array field is absent, validate that consumed closing tokens are `]` and `}`, and reject trailing malformed JSON instead of returning success after the last complete proxy. Add new `infrastructure/proxyscrape/client_test.go` cases and extend PubProxy tests with missing-array, truncated-tail, emit-error, and valid-trailing-field inputs; provider callback errors must remain unwrapped so CLI sentinels survive `errors.Is`.

10. **Correct behavior-defining documentation.** Update `docs/events.md` and the discovery/status sections of `docs/proxy.md` to state that each emitted tuple is checked read-only against PostgreSQL before RabbitMQ publication, existing rows are counted as `skipped_existing`, lookup errors fail closed per provider, and the consumer independently checks then inserts conflict-safely. Document the cleanup migration’s “keep newest exact tuple” rule. In `docs/tomestone.md`, describe the shared process-wide request-rate controller and per-worker provider cooldown limiter. In `docs/lodestone.md`, remove the contradictory sentence that tokens are charged per HTTP request: the current fork exposes only scraper methods, so retries consume new tokens but `FetchCharacter` still performs two internal HTTP requests behind one token; keep the existing explicit limitation paragraph rather than claiming unimplemented per-request throttling.

## Critical files & anchors

- `cmd/cli/proxy.go` — `publishDiscoveredProxies` is the read-only database-check and individually confirmed RabbitMQ boundary.
- `port/contract/proxy.go` — `ProxyRepository` defines exact tuple existence, insert-only semantics, and excluded-ID selection used by every adapter/fake.
- `infrastructure/postgres/repository/proxy.go` — tuple lookup, conflict-safe insert, and distinct random selection must agree with the PostgreSQL uniqueness invariant.
- `domain/proxy/service.go` — `ProcessNewProxy` is the consumer-side second check and the only production insertion caller.
- `domain/census/worker/worker.go` — proxy runtime construction must release failed claims and inject the limiter that clients pause on 429.

## Assumptions & contingencies

- Duplicate identity is exact `(protocol, ip, port)`. Different protocols on the same IP/port are distinct proxies; existing values are not normalized or rewritten.
- The publisher is intentionally read-only for proxy rows. Therefore “already exists” means visible to its `Exists` query; it cannot reserve an unseen tuple against a concurrent insertion. The consumer’s independent check plus `InsertIfAbsent ... ON CONFLICT DO NOTHING` guarantees the database still stores one row and duplicate deliveries perform no update.
- Do not add a run-wide in-memory deduplication set: bounded-memory streaming takes precedence, and duplicate records not yet visible in PostgreSQL remain harmless because the consumer is conflict-safe.
- Duplicate cleanup is irreversible and keeps the newest row by `updated_at`, then `id`. If the production database already has the `00008` unique constraint intact, the deletion is a no-op and the migration only verifies the invariant.
## Verification

1. Run the publisher contract tests:

   ```bash
   go test -count=1 -v ./cmd/cli \
     -run 'TestPublishDiscoveredProxies_(SkipsExisting|PublishesNew|LookupFailureFailsClosed|AllExistingSucceeds|PartialProviderFailure|QueuePublishFailure)'
   ```

   Required observations:
   - Seeded `http/1.2.3.4/8080` produces zero queue calls and one skipped-existing count.
   - A new tuple produces exactly one `new-proxy` job with the original metadata.
   - Same IP/port with protocol `socks5` remains distinct from existing `http` and publishes.
   - A repository lookup error produces zero queue calls, stops that provider, and is classified as `proxy.discover.lookup_failed`.
   - A provider containing only existing tuples returns `(0, nil)` rather than the all-providers-failed error.

2. With PostgreSQL available at the repository-test defaults (`localhost:5432`, database `ffxiv_census`, user `census`, password `secret`), run:

   ```bash
   go test -count=1 -v ./infrastructure/postgres/repository \
     -run 'TestProxyRepository_(Exists|InsertIfAbsent|InsertIfAbsentConcurrent)'
   ```

   The concurrent test must report one inserted result, one conflict/no-op result, no unique-constraint error, and one stored row. Tests may skip only when PostgreSQL is genuinely unavailable; a skip is not release evidence.

3. Run consumer and migration behavior tests:

   ```bash
   go test -count=1 -v ./domain/proxy \
     -run 'TestService_ProcessNewProxy_(SkipsExistingWithoutWrite|ExistsError|ConcurrentConflict)'
   go test -count=1 -v ./infrastructure/postgres/migration
   ```

   The seeded duplicate service test must pass with a nil checker, proving the existing tuple exits before insertion/checking. After applying migrations to a disposable database, this query must return zero rows:

   ```bash
   psql "$POSTGRES_DSN" -v ON_ERROR_STOP=1 -c \
     'SELECT protocol, ip, port, COUNT(*) FROM proxies GROUP BY protocol, ip, port HAVING COUNT(*) > 1;'
   ```

4. Prove distinct rotation, callback handling, and complete JSON consumption:

   ```bash
   go test -count=1 -v ./infrastructure/httpclient \
     -run 'TestRotatingProxyClient|TestClient_GetStream'
   go test -count=1 -v ./infrastructure/proxyscrape ./infrastructure/pubproxy ./infrastructure/textproxy ./infrastructure/geonode
   ```

   The injected rotating-client test must observe `[11, 22, 33]`; malformed/truncated JSON must return a decode error; and nil stream consumers must cause zero HTTP requests.

5. Prove proxy claims and cooldowns remain correct under concurrency:

   ```bash
   go test -count=1 -race -v ./domain/census/worker ./infrastructure/lodestone ./infrastructure/tomestone ./infrastructure/provider
   ```

   Required cases: initial and replacement constructor failures release the newly claimed proxy; Lodestone 429 pauses only the worker’s Lodestone provider; Tomestone 429 pauses only that worker’s Tomestone provider; two Tomestone clients share one burst token and one adaptive backoff state.

6. Run the repository-wide compile and behavior gate:

   ```bash
   go build ./...
   go test -count=1 ./...
   ```

7. In a disposable environment with RabbitMQ and PostgreSQL configured, insert a tuple known to be emitted by a controlled provider fixture, run `./bin/ffxiv-census proxy discover`, and verify the command logs `skipped_existing=1` while the RabbitMQ `new-proxy` queue count does not increase for that tuple. Then remove the row, rerun discovery, and verify exactly one event is published; run the proxy consumer twice against duplicate copies of that event and verify PostgreSQL still contains one row and the second delivery logs `proxy.process_new.skipped_exists`.
