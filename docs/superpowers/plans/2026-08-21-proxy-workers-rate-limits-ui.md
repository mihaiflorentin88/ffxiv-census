# Proxy Workers, Rate Limits, and Race Legend

## Context
Fix the dashboard Race Distribution doughnut legend so its point markers remain circular rather than stretched. Make `proxy discover` route provider traffic through active non-Lodestone proxies, swap away from a blocked/failing proxy, and stream each discovered record directly to RabbitMQ so the cronjob no longer retains complete response/record/job collections and OOMs; Kubernetes memory limits remain unchanged. Correct the proxy census workers' replacement/cooldown behavior and the ordinary census consumer's request-rate enforcement, then bring canonical documentation up to date with HEAD commit `f5462be0b301bfcdd1bbf97ccfb4dcf9604dbdc2` and these fixes. Cluster logs are not accessible from this workstation, so the user-reported OOM and incorrect limiter/swap behavior are authoritative; post-deployment log checks are specified below.

## Approach

1. **Persist this approved execution plan before changing code.** Copy it verbatim to `docs/superpowers/plans/2026-08-21-proxy-workers-rate-limits-ui.md`; this is the implementation record required by the repository workflow.

2. **Lock the Race Distribution legend contract with a failing UI test, then apply the square point-style configuration.** In `cmd/http/ui/dashboard_test.go`, extend `TestDashboardHandler_RaceChartLayout` to require `usePointStyle: true` and `pointStyle: "circle"`, and to reject `pointStyleWidth`, whose fixed 10-pixel width differs from the Chart.js font-derived marker height. In `cmd/http/ui/templates/dashboard.html`, change the race chart labels object to the exact configuration `labels: { color: "#94a3b8", padding: 12, font: { size: 11 }, usePointStyle: true, pointStyle: "circle" }`; retain the current bottom/center legend, 340-pixel chart height, responsive sizing, and 65% cutout. Do not replace the circles with the rectangular legend boxes used by `races.html` and `world_detail.html`.

3. **Add an unlocked random-active lookup for generic provider scraping, explicitly separated from Lodestone acquisition.** Start with failing tests in `domain/proxy/hub_test.go`, `infrastructure/postgres/repository/proxy_test.go`, and `mock/repository/proxy.go`. Extend `contract.ProxyRepository` in `port/contract/proxy.go` with `RandomActive(ctx context.Context, excludeID *int64) (*ProxyRecord, error)`. Implement PostgreSQL selection with `status = 'active'`, supported protocols (`http`, `https`, `socks4`, `socks5`), optional `id <> excludeID`, `ORDER BY RANDOM() LIMIT 1`, returning `nil, nil` when no row matches; the fake must filter identically and choose using an injectable/deterministic test RNG rather than flaky statistical assertions. Add to `domain/proxy/hub.go`:
   - `RandomActive(ctx context.Context) (*Proxy, error)`, delegating with no exclusion.
   - `SwapActive(ctx context.Context, current *Proxy) (*Proxy, error)`, delegating with `current.Record().ID` so a returned proxy is different; a nil current behaves like `RandomActive`.
   Both exported comments must state: **“This method must not be used for Lodestone APIs; Lodestone workers must use NewProxy so proxies are checked and atomically owner-locked.”** These methods intentionally neither claim nor mutate locks and do not mark a proxy inactive when an external provider blocks it. Keep every existing `ClaimProxy`/`NewProxy` caller unchanged for Lodestone census traffic.

4. **Introduce a bounded streaming GET path and a rotating discovery-only HTTP adapter.** First add failing adapter tests. Extend `contract.HTTPClient` in `port/contract/httpclient.go` with `GetStream(ctx context.Context, url string, queryParams, headers map[string]string, consume func(statusCode int, body io.Reader) error) error`; update `infrastructure/httpclient/client.go` and `mock/httpclient/httpclient.go` so the real adapter passes the live response body to `consume`, always closes it after the callback, never calls `io.ReadAll`, and propagates context, request, callback, and close errors consistently with existing `Get`. Add `infrastructure/httpclient/rotating_proxy_client.go` implementing the same contract and tests in `rotating_proxy_client_test.go`: it lazily obtains `ProxyHub.RandomActive`, reuses that proxy, builds the concrete transport through existing `httpclient.NewProxyClient`, and on a transport error or HTTP 403, 429, or 5xx discards/closes the response and retries through `ProxyHub.SwapActive`. Limit one request to three distinct proxy attempts; if no active proxy exists on the first attempt, use the existing direct client to preserve empty-database bootstrap, while exhaustion after a failed proxy returns the last error. Its package/type comments must repeat that it is only for public proxy-list providers and must not be used for Lodestone APIs. `Do`, `Get`, and `Post` retain current behavior; only discovery providers use the rotating streaming path.

5. **Clean-cut proxy providers from collection-returning to callback streaming, then publish inside the callback.** Change `contract.ProxyProvider.FetchProxies` to `FetchProxies(ctx context.Context, emit func(ProxyRecord) error) error`; migrate the sole command caller, all four concrete implementation families, constructors, and test fakes—`infrastructure/proxyscrape`, `infrastructure/geonode`, `infrastructure/pubproxy`, `infrastructure/textproxy`, `cmd/cli/proxy.go`, and `cmd/cli/proxy_test.go`—with no compatibility method or retained `[]ProxyRecord` path. Inject the rotating discovery HTTP client into all provider accessors in `container/infrastructure.go`, leaving the ordinary `HTTPClient()` and Lodestone/Tomestone client wiring untouched.
   - `textproxy` must scan each live response with `bufio.Scanner`, parse one line, emit one `ProxyRecord`, then discard it; after one URL/protocol completes, its body is closed before fetching the next.
   - `proxyscrape` and `pubproxy` must decode JSON from the live reader with `json.Decoder` and emit records during decoding rather than building a second output slice.
   - `geonode` may retain only one 100-record API page, emit that page, release it, then request the next page; remove `allRecords`.
   - In `publishDiscoveredProxies`, the emit callback must create one `handler.NewProxyJob` and immediately call `q.Publish`; increment confirmed counts only after success. A callback publish error stops that provider, logs `proxy.discover.publish_failed`, and continues to the next provider under the existing partial-success policy. Remove the `jobs` slice, `publishAll` call from this path, `proxies = nil`, and `runtime.GC()`; database `Upsert` remains the cross-provider deduplication boundary.
   Provider/CLI tests must prove the first queue publish happens before a provider emits/fetches its next record/page/source, an emit error prevents subsequent fetches, partial provider failures still continue, and the all-failed error remains `proxy discovery failed: all providers failed (%d errors)`. This behavioral ordering—not a brittle heap assertion—is the bounded-memory test.

6. **Make rate limits govern actual outbound requests and preserve the longest cooldown.** Write red tests in `infrastructure/lodestone`, `infrastructure/tomestone`, and `infrastructure/provider/proxy_limiter_test.go` using fake clocks/servers already used by those packages. Move Lodestone token acquisition to immediately before every underlying outbound godestone request and every retry attempt, so a character operation that makes profile and class/job requests consumes two tokens; keep the process-wide non-proxy client at `lodestone.rate_limit = 1` request/second and each owner-locked proxy client at `proxy.consumer.lodestone_rate_limit = 1` request/second. Keep Tomestone's configured 5 requests/second process-wide: change proxy Tomestone client construction so all goroutines in a process share one injected token bucket instead of creating 20–40 independent 5 requests/second buckets, while the per-proxy provider cooldown state remains goroutine-local. Make `ProxyRateLimiter.Pause` match `RateLimiter.Pause`: never replace an existing later `pausedUntil` with an earlier one. Tests must assert per-request spacing, retry attempts acquiring new tokens, aggregate Tomestone calls across two proxy clients staying within one shared rate, and repeated pauses never shortening a cooldown.

7. **Correct Lodestone proxy-worker cooldown and replacement flow without using the generic random/swap APIs.** In `domain/census/worker/worker.go`, factor the duplicated initial/lost/bad replacement sequence into a worker-local helper that always calls `ProxyHub.NewProxy(ctx, owner)`, releases the previous owner lock only after a replacement has been claimed, recreates the per-proxy Lodestone client and handlers, and logs/returns `MarkFailed` and release errors instead of discarding them. Before each proxy-mode handler call, apply the same event-aware provider wait used by `waitForProviders`: achievement waits for Lodestone; dual-source events proceed when either provider is available and wait until the earliest cooldown only when both are paused. On a transport-class `isProxyError`, mark the old proxy failed, acquire/rebuild through `NewProxy`, and retry the same delivery once through the replacement before returning an error to RabbitMQ; a 429/rate-limit is a provider cooldown, not a dead proxy and must never trigger a swap. If no checked/locked proxy is available or rebuilding a client fails, return the delivery error for requeue but keep the worker goroutine's acquisition loop alive with context-cancellable bounded backoff (start 250 ms, double to a 5 s ceiling, reset after successful acquisition) instead of terminating the consumer slot. Tests in `domain/census/worker/worker_test.go` and `rate_limiting_test.go` must cover same-delivery retry on a different proxy, no swap on 429, no retry-budget spin while both providers are paused, retained lock ownership ordering, surfaced `MarkFailed` errors, and recovery after temporary pool exhaustion.

### Implementation examples

These examples define the intended code shape. Reuse existing package error/logging conventions and exact DTO field names when filling in elided construction details; do not substitute a different architecture.

**Race legend — copy this labels object exactly:**

```javascript
legend: {
    position: "bottom",
    align: "center",
    labels: {
        color: "#94a3b8",
        padding: 12,
        font: { size: 11 },
        usePointStyle: true,
        pointStyle: "circle"
    }
}
```

The corresponding test should assert that the rendered template contains `usePointStyle: true` and `pointStyle: "circle"` and does not contain `pointStyleWidth`.

**Repository and hub API — random selection is unlocked and discovery-only:**

```go
// port/contract/proxy.go
RandomActive(ctx context.Context, excludeID *int64) (*ProxyRecord, error)

// domain/proxy/hub.go
// RandomActive returns an unlocked random active proxy for public provider scraping.
// This method must not be used for Lodestone APIs; Lodestone workers must use
// NewProxy so proxies are checked and atomically owner-locked.
func (h *ProxyHub) RandomActive(ctx context.Context) (*Proxy, error) {
    rec, err := h.repo.RandomActive(ctx, nil)
    if err != nil || rec == nil {
        return nil, err
    }
    return New(rec, h.repo), nil
}

// SwapActive returns an unlocked random active proxy different from current.
// This method must not be used for Lodestone APIs; Lodestone workers must use
// NewProxy so proxies are checked and atomically owner-locked.
func (h *ProxyHub) SwapActive(ctx context.Context, current *Proxy) (*Proxy, error) {
    if current == nil {
        return h.RandomActive(ctx)
    }
    rec, err := h.repo.RandomActive(ctx, ptr.To(current.Record().ID))
    if err != nil || rec == nil {
        return nil, err
    }
    return New(rec, h.repo), nil
}
```

Do not introduce `ptr.To` solely for this code if no repository helper exists; use a local `excludeID := current.Record().ID` and pass `&excludeID`. The PostgreSQL query shape is:

```sql
SELECT <the existing proxy column list>
FROM proxies
WHERE status = 'active'
  AND protocol IN ('http', 'https', 'socks4', 'socks5')
  AND ($1::bigint IS NULL OR id <> $1)
ORDER BY RANDOM()
LIMIT 1
```

Use the repository's existing no-row handling to return `(nil, nil)`. Tests should insert an active HTTP proxy, an inactive proxy, an unsupported-protocol record, and the excluded active proxy, then assert only the eligible non-excluded record can be returned and that `locked_by`/`locked_at` remain unchanged.

**Streaming HTTP body — callback owns parsing only for the duration of the call:**

```go
// port/contract/httpclient.go
GetStream(
    ctx context.Context,
    url string,
    queryParams, headers map[string]string,
    consume func(statusCode int, body io.Reader) error,
) error
```

The real adapter must close the body exactly once and must not buffer it. First extract current `Do` lines 33–69 (nil-context handling, method/URL validation, query encoding, optional timeout, request creation, and headers) into `func (c *client) buildRequest(ctx context.Context, req requestdto.HTTPRequest) (*http.Request, context.CancelFunc, error)`. Make existing `Do` call that helper without changing its buffered-response contract, then implement:

```go
func (c *client) GetStream(
    ctx context.Context,
    rawURL string,
    queryParams, headers map[string]string,
    consume func(int, io.Reader) error,
) error {
    req, cancel, err := c.buildRequest(ctx, requestdto.HTTPRequest{
        Method:      http.MethodGet,
        URL:         rawURL,
        QueryParams: queryParams,
        Headers:     headers,
    })
    if err != nil {
        return err
    }
    defer cancel()

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return fmt.Errorf("execute request %s %s: %w", req.Method, req.URL.String(), err)
    }
    defer resp.Body.Close()

    if consume == nil {
        return fmt.Errorf("stream response consumer is required")
    }
    return consume(resp.StatusCode, resp.Body)
}
```

Adapter tests should wrap the response body in a counting `io.ReadCloser`, return success and callback errors, and assert one close in both cases. Add a nil-callback test expecting `stream response consumer is required`; `GetStream` deliberately passes every HTTP status to the callback because the rotating client/provider decides whether a status means rotate, provider failure, or parse.

**Rotating discovery request — bounded, distinct attempts:**

```go
current, err := c.hub.RandomActive(ctx)
if err != nil {
    return err
}
if current == nil {
    return c.direct.GetStream(ctx, rawURL, query, headers, consume)
}

var lastErr error
for attempt := 0; attempt < 3 && current != nil; attempt++ {
    proxied, err := NewProxyClient(current.Address(), c.timeout)
    if err == nil {
        err = proxied.GetStream(ctx, rawURL, query, headers, func(status int, body io.Reader) error {
            if status == http.StatusForbidden || status == http.StatusTooManyRequests || status >= 500 {
                return retryableStatusError(status)
            }
            return consume(status, body)
        })
    }
    if err == nil {
        return nil
    }
    lastErr = err
    current, err = c.hub.SwapActive(ctx, current)
    if err != nil {
        return errors.Join(lastErr, err)
    }
}
return lastErr
```

Use a private typed/sentinel retryable-status error so the adapter can distinguish rotation statuses from a parser callback error; parser/emit errors must return immediately without rotating. A test selector should return proxy IDs `11`, `22`, `33`; make attempts 11 and 22 fail, let 33 succeed, and assert the exact attempt order `[11, 22, 33]`. A separate test must return nil initially and assert one direct request.

**Provider-to-queue streaming — no provider or job slice:**

```go
// port/contract/proxy.go
type ProxyProvider interface {
    Name() string
    FetchProxies(ctx context.Context, emit func(ProxyRecord) error) error
}

// cmd/cli/proxy.go, inside the provider loop
publishedForProvider := 0
err := p.FetchProxies(ctx, func(rec contract.ProxyRecord) error {
    job := handler.NewProxyJob(handler.NewProxyPayload{
        Protocol:      rec.Protocol,
        IP:            rec.IP,
        Port:          rec.Port,
        Country:       rec.Country,
        Anonymity:     rec.Anonymity,
        Source:        rec.Source,
        UptimePercent: rec.UptimePercent,
    })
    if err := q.Publish(ctx, job); err != nil {
        return err
    }
    publishedForProvider++
    totalPublished++
    return nil
})
```

On a non-nil provider error, log either `proxy.discover.publish_failed` when the error came from the emit callback or `proxy.discover.provider_failed` for fetch/decode/status errors, increment `totalErrors` once for that provider, and continue. Preserve the provider completion logs with fetched/published counts, but derive counts incrementally rather than from `len(proxies)`.

For text sources, parse and emit one line at a time:

```go
return c.http.GetStream(ctx, sourceURL, nil, nil, func(status int, body io.Reader) error {
    if status != http.StatusOK {
        return fmt.Errorf("%s returned HTTP %d", c.name, status)
    }
    scanner := bufio.NewScanner(body)
    for scanner.Scan() {
        rec, ok := parseProxyLine(scanner.Text(), protocol, c.name)
        if !ok {
            continue
        }
        if err := emit(rec); err != nil {
            return err
        }
    }
    return scanner.Err()
})
```

For Geonode, decode one page, emit every valid record, clear the page-local response, and only then request `page+1`. A test should make the page-2 HTTP fake fail unless the queue fake has already observed every page-1 publish; this fails under the old `allRecords` implementation and passes only with true page-by-page streaming.

**Cooldown monotonicity and shared Tomestone bucket:**

```go
func (r *ProxyRateLimiter) Pause(p contract.Provider, d time.Duration, reason string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    newUntil := time.Now().Add(d)
    if current := r.pausedUntil[p]; current.After(newUntil) {
        return
    }
    r.pausedUntil[p] = newUntil
    r.reasons[p] = reason
}
```

Test cooldown monotonicity without adding a clock abstraction: call `Pause(provider, time.Hour, "long")`, capture `PausedUntil`, then call `Pause(provider, time.Minute, "short")` and assert the stored timestamp and reason are still the first values. The Tomestone construction shape is one process-scoped bucket injected into every client:

```go
sharedTomestoneLimiter := rate.NewLimiter(rate.Limit(cfg.RateLimit), 1)
newTomestoneClient := func(proxyURL string) (contract.TomestoneClient, error) {
    return tomestone.NewClientWithProxy(cfg, proxyURL, logger, sharedTomestoneLimiter)
}
```

Change both direct and proxy constructors consistently so tests can inject a limiter; do not create a package-global limiter. For a deterministic aggregate-rate test, inject `rate.NewLimiter(rate.Every(time.Hour), 1)` into two clients: let client A consume the initial burst token, invoke client B with a 20-millisecond context, and require `context.DeadlineExceeded`. With separate per-client limiters, client B incorrectly succeeds immediately, so the test proves sharing without wall-clock tolerance. Lodestone tests should count limiter waits around each actual profile/class-job request and each retry, not merely one wait per exported method call.

**Lodestone proxy replacement — state-machine shape and ordering:**

```go
// Generic RandomActive/SwapActive are intentionally absent here.
replacement, err := proxyHub.NewProxy(ctx, owner)
if err != nil || replacement == nil {
    return errNoCheckedProxy
}
clients, handlers, limiter, err := buildProxyRuntime(replacement)
if err != nil {
    _ = replacement.Release(cleanupCtx, owner)
    return err
}
if previous != nil {
    if markBad {
        if err := previous.MarkFailed(cleanupCtx, owner); err != nil {
            _ = replacement.Release(cleanupCtx, owner)
            return err
        }
    } else if err := previous.Release(cleanupCtx, owner); err != nil {
        _ = replacement.Release(cleanupCtx, owner)
        return err
    }
}
return replacement, clients, handlers, limiter, nil
```

Preserve the old proxy until a replacement has been successfully claimed and its clients/handlers built; if building fails, release the new claim and leave the old state available for cleanup. For one queue delivery, use at most two handler executions: initial proxy, then one replacement only when `isProxyError(err)` is true. A `429`/`rate limit` error waits on the appropriate limiter and returns/retries under normal queue policy without calling `MarkFailed` or acquiring a replacement. The acquisition loop should use `time.NewTimer(backoff)` and select on `ctx.Done()`; double `250ms → 500ms → 1s → 2s → 4s → 5s`, cap at 5 seconds, and reset to 250 milliseconds immediately after a checked proxy is acquired.

8. **Synchronize canonical documentation with HEAD and the new behavior.** Make focused edits only to these existing sections:
   - `docs/queue.md` (“Consumer Pattern” and “Graceful shutdown under Kubernetes Deployment”): document mandatory deferred publisher confirms/unroutable returns, channel-before-connection `Close` with joined errors, and the dual-context shutdown in which claiming stops, unclaimed deliveries are nacked/requeued, and in-flight handlers finish.
   - `docs/events.md` (“CLI”): document confirmed-count/fail-fast `publishAll` behavior and queue closure after publish commands; describe `proxy discover` callback publication and partial-provider failure semantics.
   - `docs/proxy.md` (“How It Works”, “Diagnostic Logging”, “Container Accessors”, discovery/rate-limit sections): replace goroutine runtime IDs with `census-consume-<hostname>-p<pid>-w<workerID>`, remove `goroutine_id`, update `container.Load.ProxyHub()` with owner passed to worker execution, explain owner-locked `NewProxy` replacement/cooldown behavior, document random unlocked discovery selection/swap and the Lodestone prohibition, and state that discovery streams responses/events without increasing the 1 GiB pod limit.
   - `docs/container.md` (“Usage Tips” and proxy accessors): change `ProxyHub(owner)` to `ProxyHub()` and describe command-owned owner IDs.
   - `docs/census.md` (“CensusService”): record that `Summary` fans out three database queries and add `WorldDetail(ctx, worldName)` / `WorldDetailStats` with seven concurrent database calls.
   - `docs/ui.md` dashboard route entry: record the responsive dashboard grid, race doughnut, expansion card, and circular bottom-centered race legend.
   - `docs/lodestone.md` and `docs/tomestone.md` rate-limit sections: distinguish shared process-wide request buckets from per-owner-locked-proxy Lodestone buckets, specify that tokens are charged per HTTP attempt, and document the shared Tomestone bucket plus independent per-proxy cooldowns.

## Critical files & anchors

- `domain/census/worker/worker.go` — `proxyWorkerLoop`, `waitForProviders`, and `isProxyError`; this is the Lodestone-only lock/swap and delivery-retry boundary.
- `cmd/cli/proxy.go` — `publishDiscoveredProxies`; this is where provider callbacks become individually confirmed RabbitMQ events.
- `container/infrastructure.go` — provider, Lodestone, Tomestone, HTTP client, and `ProxyHub()` construction; keep discovery rotation isolated from census API wiring.
- `infrastructure/httpclient/client.go` — current `io.ReadAll(resp.Body)` at the outbound boundary; streaming must close bodies without buffering.
- `cmd/http/ui/templates/dashboard.html` — `racePieChart` legend labels; apply the exact Chart.js configuration from step 2.

## Verification

Run from the repository root in strict red/green order, recording the expected initial failures before each production change:

1. `go test -run TestDashboardHandler_RaceChartLayout ./cmd/http/ui` — passes only when the dashboard HTML contains circular point-style markers and no fixed `pointStyleWidth`.
2. `go test -run 'TestProxyHub_(RandomActive|SwapActive)' ./domain/proxy && go test -run RandomActive ./infrastructure/postgres/repository` — active-only selection, exclusion, nil result, supported protocols, and non-mutated locks pass against fake and temporary PostgreSQL.
3. `go test ./infrastructure/httpclient ./infrastructure/proxyscrape ./infrastructure/geonode ./infrastructure/pubproxy ./infrastructure/textproxy ./cmd/cli` — streaming bodies close on every success/error path, rotation uses distinct proxies and direct bootstrap, and immediate publisher-confirm ordering passes.
4. `go test -race ./infrastructure/provider ./infrastructure/lodestone ./infrastructure/tomestone ./domain/census/worker` — longest cooldown, per-attempt token charging, shared aggregate Tomestone rate, swap/retry, pool exhaustion recovery, and lock-order tests pass without races.
5. `make build` and run `./bin/ffxiv-census server --start --port 8080`; open `/ui/dashboard` in Chromium at desktop width and at 390 px, confirm all nine legend markers are visually circular, the doughnut remains circular, and labels remain readable/wrapped without clipping.
6. In an environment with cluster credentials after deploying the built image, inspect the named historical job if retained:
   - `kubectl get pod -l job-name=ffxiv-census-cron-proxy-discover-manual-20260821-161526-w9mhp -o yaml`
   - `kubectl logs -l job-name=ffxiv-census-cron-proxy-discover-manual-20260821-161526-w9mhp --previous`
   - `kubectl describe pod -l job-name=ffxiv-census-cron-proxy-discover-manual-20260821-161526-w9mhp`
   Confirm the prior `OOMKilled`/exit 137 evidence without changing its 1 GiB limit. Trigger one manual job from the deployed `proxy-discover` CronJob and verify provider fetches publish progressively, proxy IDs change after a 403/429/5xx or transport failure, the job completes without OOM/restart, and memory remains bounded rather than growing with record count.
7. Compare timestamped logs from `ffxiv-census-worker-proxy-id-sweep`, `ffxiv-census-worker-proxy-character-census`, `ffxiv-census-worker-proxy-achievement-census`, and `ffxiv-census-worker-census-consumer`: proxy transport failure must show old proxy failure → checked/locked replacement → same-delivery retry; 429 must show provider wait with no proxy swap; ordinary Lodestone requests must be spaced at least one second per HTTP attempt; aggregate Tomestone calls from proxy goroutines must remain at the configured five requests/second. Confirm no rapid rate-limit requeue loop and no worker slot permanently disappears after temporary pool exhaustion.

## Assumptions & contingencies

- Random unlocked proxies are for public proxy-list discovery only. Lodestone traffic continues to use `NewProxy` and owner locks; no implementation may route Lodestone through `RandomActive` or `SwapActive`.
- A missing active proxy pool is a bootstrap condition: discovery uses the existing direct HTTP client until an active proxy exists. Once a proxy attempt has failed, inability to find a distinct replacement returns that request's error rather than silently retrying the same endpoint directly.
- HTTP 403, 429, 5xx, and transport errors rotate discovery proxies but do not mark them inactive, because a provider-specific block does not prove the proxy is dead. Lodestone 429s pause the provider and never rotate/disable the proxy.
- If the named manual pod and previous logs have expired, execute the same read-only checks against the newly triggered manual job and use its pod termination status plus timestamped consumer logs as the deployment baseline; do not compensate by increasing pod requests or limits.
