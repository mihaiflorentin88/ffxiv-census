# Adaptive Proxy Scanning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Increase freshly verified proxy availability through fair, replica-scalable scanning, ipify health checks and separate Lodestone usability checks.

**Architecture:** Keep scheduling and outcome policy in `domain/proxy`, interfaces and persistence values in `port/contract`, and network/SQL operations in existing infrastructure adapters. A single dispatcher per replica owns concurrency admission; PostgreSQL owns leases, observation fencing and persisted time. Wire the feature through the existing service locator, not a second dependency-injection framework.

**Tech Stack:** Go, net/http, golang.org/x/net/proxy, PostgreSQL, embedded Goose migrations, Cobra, Viper, existing structured logging and Helm chart.

**Spec:** [2026-09-09-proxy-cohort-scheduling-design.md](../specs/2026-09-09-proxy-cohort-scheduling-design.md), approved before this plan was requested.

## Global Constraints

- Implementation requires a separate user approval. This document does not authorize deployment.
- Main model implements and tests directly, per repository rules. Use `executing-plans` for inline execution; do not dispatch implementation subagents without a changed user instruction.
- Strict red/green TDD. Run each behavioral regression before implementing its production change. Compilation failure establishes missing APIs, but behavioral regressions must also demonstrate the relevant bad behavior where an existing path exists.
- Pure Go; verify with `CGO_ENABLED=0`.
- Check timeout `10s`; verification interval `60s`; freshness TTL `120s`.
- Recovery delays `1m`, `2m`, `4m`, `8m`, then `15m`, capped; extra recovery attention ends `1h` after the last general-health success.
- Reservations `10/45/45` percent of concurrency, borrowable and nonpreemptive.
- Scan lease `30s`; per-proxy repeat floor `1s`; inconclusive retry floor `60s`; direct endpoint control interval `30s`; destination cooldown `60s` or a longer valid Retry-After.
- Background includes every non-successful proxy, including recent recovery candidates. Recovery deadlines never exclude a row from background; common repeat/inconclusive cooldowns do.
- Inconclusive completion advances attempt position, but does not change the latest conclusive health result, its version, failure count or success timestamp.
- Database time controls eligibility, completion timestamps, freshness, cooldown and lease expiry. Callers pass durations, never application-wall-clock deadlines.
- Network work holds no database connection. Claims are bounded by free executor capacity, without a claimed prefetch queue.
- General-health lists/counts exclude stale evidence, not destination-cooled but otherwise healthy proxies. Lodestone cooldown applies only to destination acquisition/use.
- Never backfill ipify success from historical Lodestone timestamps. Existing rows begin in background with unknown completed-attempt time.
- Preserve unrelated user changes in `k8s/values.yaml` and untracked `infrastructure/logging/logger_test.go`. Apply only the approved scanner flag/pool edits to values; stage those hunks explicitly, not the whole file.
- The shared database has `max_connections=100`. Scanner deployment defaults are two maximum open and one idle connection per replica through existing `POSTGRES_MAX_OPEN_CONNS` / `POSTGRES_MAX_IDLE_CONNS`, not a new pool configuration subsystem.
- Work on an isolated branch/worktree when implementation starts, following `using-git-worktrees`. Additive contracts may coexist during the implementation sequence; final integration removes all obsolete APIs and callers. No intermediate commit is a deployable semantic cutover.

## Reconnaissance and file ownership

Verified entry points:

- `port/contract/proxy.go`: current checker returns `(int, error)`; repository includes unversioned `UpdateStatus`, `UpdateScanTime`, `ListForScan`, `ListDeadForScan`, `MarkFailedProxy`, and consumer locks.
- `domain/proxy/service.go`: `ProcessNewProxy` currently performs an unleased check after insert; `ProcessScanProxy` performs an unversioned status update.
- `domain/proxy/worker/scan.go`: split pools, claimed prefetch, credit semaphore and notifier.
- `domain/proxy/hub.go`: handout check; random-active provider discovery paths.
- `domain/proxy/proxy.go`: `CanUse` trusts local status then extends a lock without checking DB health; `MarkFailed` has no captured observation version.
- `domain/census/worker/worker.go`: general-failure detection uses error strings; both first attempt and retry can call `MarkFailed`. Provider 429 handling already avoids immediate proxy rotation. Preserve that protection.
- `infrastructure/proxy/checker.go`, `infrastructure/httpclient/proxy_client.go`, `infrastructure/lodestone/client.go`, `infrastructure/tomestone/client.go`: duplicate proxy transport construction, including context-ignoring SOCKS4 fallback.
- `infrastructure/postgres/repository/proxy.go`: all located proxy SQL reads/writes. `ListActive`, `CountByStatus`, `RandomActive`, `ClaimProxy`, `ExtendLock` need fresh semantics.
- No current `ListActive` / `CountByStatus` references or proxy SQL were found in `cmd/http`. Verified on 2026-09-09: the `ui_stats_snapshots` read model refresh (`infrastructure/postgres/repository/ui_stats.go`) queries only `FROM characters`; it counts no proxies, so no snapshot/UI read-model change is required. Do not invent a proxy UI. Fix repository contracts and verify references again at execution; document intentionally unchanged HTTP/UI if still absent.
- `container/infrastructure.go`: checker paths at the provider-client construction, `ProxyChecker()` accessor and `ProxyHub()` construction; `container/domain.go` constructs `ProxyService`.
- Highest migration currently `00016`; new migration is `00017_proxy_scan_scheduling.sql`. Recheck numbering at execution if the tree changed.
- `infrastructure/postgres/repository/helpers_test.go`: current helper truncates a shared database and skips on startup error. New concurrency tests require isolation and explicit integration mode that fails rather than silently skips.

New production files have narrow responsibilities:

| File | Responsibility |
| --- | --- |
| `port/contract/proxy_scan.go` | Typed check errors, scan values, scan-store and guard ports |
| `domain/proxy/scheduling.go` | Relative retry/update policy |
| `domain/proxy/worker/admission.go` | Allocation and persistent admission credits |
| `infrastructure/httpclient/proxy_transport.go` | Shared cancellation-safe proxy transport |
| `infrastructure/proxy/health.go` | ipify validation and direct control check |
| `infrastructure/proxy/guard.go` | Local endpoint-health state and lifecycle |
| `infrastructure/postgres/repository/proxy_scan.go` | Lease claims, completion and release SQL |
| `infrastructure/postgres/migration/query/00017_proxy_scan_scheduling.sql` | Scheduling state and indexes |

Keep `scan.go` as the worker entry point rather than add a parallel dispatcher framework. Extend existing fake repositories/checkers; add `mock/proxy/guard.go` for the new guard port. Tests live beside their owning code. Preserve existing provider ingestion and destination business semantics.

## Cross-task contract

These are the final names and signatures. Task 1 introduces value types and policy, Task 3 adds store methods and implementations, and Task 6 removes old methods after migrating every caller. No application-supplied `now` argument is permitted on production persistence APIs.

```go
// port/contract/proxy_scan.go; context and time are standard-library imports.
type ProxyCheckKind uint8
const (
    CheckLocal ProxyCheckKind = iota // fail closed on unknown/local errors
    CheckProxy                      // proven proxy dial/negotiation failure
    CheckTarget                     // HTTP/payload/target TLS error
    CheckDeadline                   // attribution depends on endpoint guard
    CheckCancelled
)
type ProxyCheckError struct {
    Kind ProxyCheckKind
    Reason string       // bounded diagnostic label; never parsed for decisions
    RetryAfter time.Duration
    Err error
}
func (e *ProxyCheckError) Error() string
func (e *ProxyCheckError) Unwrap() error

// Retain the existing checker interface, with a documented typed-error contract.
// nil error means success, including complete response validation.
// ProxyChecker.Check(context.Context, string, string, int) (int, error)

type ScanQueue uint8
const (
    ScanVerification ScanQueue = iota
    ScanRecovery
    ScanBackground
)
type ScanOutcome uint8
const (
    ScanInconclusive ScanOutcome = iota
    ScanSuccess
    ScanFailure
)
type ProxyScanPolicy struct {
    VerifyInterval, FreshnessTTL time.Duration
    RecoveryBase, RecoveryCap, RecoveryHorizon time.Duration
    LeaseDuration, MinRepeatInterval, InconclusiveRetry time.Duration
    DeadAfter time.Duration
    FailThreshold int
}
type ScanLease struct {
    Record ProxyRecord
    Token string
    Version int64
}
type ScanUpdate struct {
    Outcome ScanOutcome
    LatencyMS int
    NextDelay time.Duration
    RepeatDelay time.Duration
    RecoveryStep int
    FreshnessTTL time.Duration
    DeadAfter time.Duration
    FailThreshold int
}
type GuardSnapshot struct {
    Healthy bool
    Generation uint64
}
type ProxyEndpointGuard interface {
    Snapshot() GuardSnapshot // one consistent snapshot under one lock
    Run(ctx context.Context) error
}
type ProxyScanStore interface {
    ClaimScans(ctx context.Context, queue ScanQueue, limit int) ([]ScanLease, error)
    CompleteScan(ctx context.Context, lease ScanLease, update ScanUpdate) (bool, error)
    ReleaseScan(ctx context.Context, lease ScanLease) error
}
```

`CompleteScan` returns `(false, nil)` for fenced/discarded observations and a nonnil error for persistence failure. Only `(true, nil)` counts as an accepted completion. A failed database write never fabricates a completed attempt.

Extend `ProxyRecord` with:

```go
GeneralHealthy bool
LastCompletedAt, LastVerifiedAt, NextAttemptAt, ScanNotBefore *time.Time
RecoveryStep int
ObservationVersion int64
ScanToken *string
ScanLeaseUntil *time.Time
ClaimedVersion *int64
DestinationCooldownUntil *time.Time
```

`GeneralHealthy` records the latest conclusive result; it is independent of historical `Status`. Do not infer the result from `LastCompletedAt > LastVerifiedAt`, because an inconclusive attempt also advances completion time. `NextAttemptAt` is the verification/recovery deadline; `ScanNotBefore` is the common minimum-repeat/inconclusive cooldown. Background ignores `NextAttemptAt`.

Final consumer repository changes in `ProxyRepository`:

```go
// Embed ProxyScanStore and retain Exists, InsertIfAbsent, Get, ListActive,
// Count, CountByStatus, ClaimProxy, ReleaseProxy and RandomActive.
RefreshConsumerLock(ctx context.Context, id int64, owner string,
    lockTTL time.Duration) (*ProxyRecord, error)
RecordConsumerFailure(ctx context.Context, rec ProxyRecord, owner string,
    lockTTL time.Duration, update ScanUpdate) (bool, error)
CooldownDestination(ctx context.Context, id int64, owner string,
    lockTTL, delay time.Duration) error
```

`RefreshConsumerLock` replaces `ExtendLock`, validates DB freshness/ownership/cooldown and returns the captured version immediately before consumer work. Consumer failure requires the same version and valid owner lock. Destination cooldown is independent of general-health version, but still owner-fenced. A stale consumer observation must not erase newer accepted health evidence; release its owned lock without altering health when discarding it.

Constructors become:

```go
// Concrete adapter return permits access to both implemented ports.
repository.NewProxyRepository(driver contract.DatabaseDriver,
    policy contract.ProxyScanPolicy) *repository.ProxyRepository
mockrepository.NewFakeProxyRepository(policy contract.ProxyScanPolicy) *mockrepository.FakeProxyRepository
```

The fake exposes `Now func() time.Time` for deterministic tests, defaults to `time.Now`, and synchronizes state with its existing mutex. Production uses database time. Add no network or clock port solely for testing.

---

## Task 1: Domain transitions and relative scheduling values

**Files:** create `port/contract/proxy_scan.go`, `domain/proxy/scheduling.go`, `domain/proxy/scheduling_test.go`; extend `port/contract/proxy.go` with record fields. Do not change the existing `ProxyChecker` signature.

**Consumes:** existing `ProxyRecord` and `(latency, error)` checker result.

**Produces:** the value types above and these domain functions:

```go
func RecoveryDelay(step int, base, cap time.Duration) time.Duration
func ClassifyCheck(err error, start, finish contract.GuardSnapshot) contract.ScanOutcome
func MakeScanUpdate(p contract.ProxyScanPolicy, rec contract.ProxyRecord,
    latency int, err error, start, finish contract.GuardSnapshot) contract.ScanUpdate
```

- [ ] Write a failing test for retry progression, saturation and reset. Use a fixed record and no sleeps:

```go
func TestRecoveryDelaySaturates(t *testing.T) {
    for step, want := range []time.Duration{
        time.Minute, 2*time.Minute, 4*time.Minute, 8*time.Minute,
        15*time.Minute, 15*time.Minute,
    } {
        if got := RecoveryDelay(step, time.Minute, 15*time.Minute); got != want {
            t.Fatalf("step %d: %v, want %v", step, got, want)
        }
    }
    if got := RecoveryDelay(1<<30, time.Minute, 15*time.Minute); got != 15*time.Minute {
        t.Fatalf("large failure history overflowed: %v", got)
    }
}
```

- [ ] Run `go test ./domain/proxy -run TestRecoveryDelaySaturates -count=1`; confirm failure before production changes.
- [ ] Implement saturation without unbounded shifts/loops or integer overflow:

```go
func RecoveryDelay(step int, base, cap time.Duration) time.Duration {
    d := base
    for i := 0; i < step && d < cap; i++ {
        if d > cap/2 { return cap }
        d *= 2
    }
    if d > cap { return cap }
    return d
}
```

- [ ] Add red/green tests for conclusive success, typed proxy error, target 429, unknown errors, parent cancellation, healthy-guard deadline and a guard generation change. Decision code uses `errors.As` / `errors.Is`, never `Reason` or `Error()` text:

```go
func ClassifyCheck(err error, start, finish contract.GuardSnapshot) contract.ScanOutcome {
    if err == nil { return contract.ScanSuccess }
    if errors.Is(err, context.Canceled) { return contract.ScanInconclusive }
    var checkErr *contract.ProxyCheckError
    if !errors.As(err, &checkErr) { return contract.ScanInconclusive }
    stable := start.Healthy && finish.Healthy && start.Generation == finish.Generation
    if stable && (checkErr.Kind == contract.CheckProxy || checkErr.Kind == contract.CheckDeadline) {
        return contract.ScanFailure
    }
    return contract.ScanInconclusive
}
```

Guard-change fencing applies to all failures; successful validated proxy checks may still be accepted. Worker cancellation is converted to an explicit `CheckCancelled` result before this function even if the check deadline also expired.

- [ ] Implement `MakeScanUpdate`: success sets `NextDelay=VerifyInterval`, `RepeatDelay=MinRepeatInterval`, recovery step zero; failure sets next recovery delay and saturated next step, repeat floor only; inconclusive preserves the step and sets both delays to `max(InconclusiveRetry, valid RetryAfter)`. Preserve last success in SQL on non-success. Copy threshold/TTL policy into the relative update; storage computes absolute timestamps from database time.
- [ ] Add behavioral tests proving a failure's recovery backoff does not become the common background cooldown, and an inconclusive result after a success does not request a failure transition. Add a reset test using `MakeScanUpdate` with an old nonzero step.
- [ ] Run `go test ./domain/proxy -count=1` and `go vet ./domain/proxy`.
- [ ] Commit only these source/test files: `feat(proxy): define scan outcomes and recovery policy`.

## Task 2: Cancellation-safe transport and complete ipify checks

**Files:** create `infrastructure/httpclient/proxy_transport.go` and adjacent tests; create `infrastructure/proxy/health.go`, `health_test.go`; update `infrastructure/proxy/checker.go`, `infrastructure/httpclient/proxy_client.go`, `infrastructure/lodestone/client.go`, `infrastructure/tomestone/client.go` and their transport tests. Update `mock/proxy/checker.go` only if a blocking callback is needed; keep its existing fixed-result constructor.

**Consumes:** `ProxyCheckError`, existing checker contract.

**Produces:**

```go
httpclient.NewProxyTransport(proxyURL string, timeout time.Duration) (*http.Transport, error)
proxyinfra.NewHealthChecker(targetURL string, timeout time.Duration,
    logger contract.Logger) *proxyinfra.HealthChecker
// HealthChecker implements ProxyChecker.
func (h *HealthChecker) CheckDirect(ctx context.Context) error
```

The existing `NewChecker` remains the Lodestone checker. Both use the one shared transport builder. `CheckDirect` is explicit control-plane-only functionality, not a fallback from `Check`.

- [ ] Start with failing complete-body and cancellation regressions. Separate target and proxy servers: target is `httptest.NewTLSServer`; HTTP proxy accepts CONNECT and tunnels bytes to that server. Trust only the fixture certificate by setting the constructed test transport's RootCAs in package-internal tests, never `InsecureSkipVerify`. Count CONNECT requests and target requests separately; a failed proxy must leave target request count unchanged. Do not point both roles at one plain HTTP server and call that proof of HTTPS proxying.
- [ ] Add response-validation unit tests with actual bodies, including this executable case in package `proxy`:

```go
func TestValidateIPBodyRejectsTrailingObject(t *testing.T) {
    err := validateIPBody(strings.NewReader(`{"ip":"203.0.113.9"} {}`))
    if err == nil { t.Fatal("accepted two JSON objects") }
}
```

`validateIPBody(io.Reader) error` is an unexported function in `health.go`. Test exact 1024-byte valid JSON plus whitespace as accepted, 1025 bytes as rejected, missing/non-string/invalid IP, JSON null/array, valid IPv6 and extra fields. Use `strings.Repeat(" ", n)` for size boundaries; NUL-filled invalid JSON does not isolate the size check.
- [ ] Run `go test ./infrastructure/proxy -run 'TestValidateIPBody|TestHealth' -count=1` and the new transport cancellation tests; confirm red.
- [ ] Implement validation using a bounded read of 1025 bytes and one object decode with EOF check:

```go
body, err := io.ReadAll(io.LimitReader(r, 1025))
if err != nil { return err }
if len(body) > 1024 { return errors.New("ipify response exceeds 1024 bytes") }
var payload struct { IP string `json:"ip"` }
dec := json.NewDecoder(bytes.NewReader(body))
if err := dec.Decode(&payload); err != nil { return err }
if _, err := netip.ParseAddr(payload.IP); err != nil { return err }
var extra any
if err := dec.Decode(&extra); err != io.EOF {
    return errors.New("ipify response contains trailing data")
}
return nil
```

- [ ] Implement proxied GET with a single overall context timeout and client timeout. Reject redirects, require 200, set `Cache-Control: no-cache`, consume/validate complete body, measure latency after validation and close idle connections after each one-shot check. Build addresses with `net.JoinHostPort`, including IPv6 proxy addresses. Target HTTP/body/JSON/TLS validation errors are `CheckTarget`; a check deadline is `CheckDeadline`; caller cancellation is `CheckCancelled`; configuration/construction errors are `CheckLocal`. Classify only proven proxy dial/negotiation failures as `CheckProxy`.
- [ ] Implement the common transport by cloning standard transport, explicitly clearing inherited `ProxyFromEnvironment`, then assigning only the selected proxy. HTTP(S) proxy TCP dial errors are wrapped at the dial boundary; target TLS errors remain target errors. SOCKS5 uses its `ContextDialer`. SOCKS4 needs a real bounded handshake, not an abandoned goroutine around `Dial`:

```go
conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", proxyAddr)
if err != nil { return nil, err }
stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
// SetDeadline to the bounded context deadline before writing/reading handshake.
// Write SOCKS4 CONNECT: VN=4, CD=1, big-endian port, IPv4 address, empty USERID.
// Resolve hostnames using net.DefaultResolver.LookupNetIP(ctx, "ip4", host).
// Read exactly eight reply bytes, requiring VN=0 and status=90.
// On any failure: stop callback, close conn, return typed negotiation error.
// On success: stop callback; if ctx.Err()!=nil, close and return cancellation.
// Clear handshake deadline before returning; HTTP request context owns the conn.
```

Keep SOCKS4's IPv4 target limitation explicit; do not classify an unsupported IPv6 target as proxy death. DNS lookup is context-bound. No per-attempt abandoned dial goroutines, insecure TLS, direct fallback or pre-probe. Test a server that accepts TCP but never answers the SOCKS handshake, a body stall and TLS stall, and observe connections close after cancellation. Do not depend on port 1 being closed; allocate a listener, record its port, close it and exercise refusal.
- [ ] Migrate all four transport builders to the shared implementation while retaining provider-specific rate limits, headers, retry logic and transport defaults. Remove now-unused SOCKS4 registration imports; remove the module dependency only if no references remain.
- [ ] Keep destination validation separate: require successful complete Lodestone response within its timeout; HTTP 403/429/5xx produce typed target errors with Retry-After parsed as delta seconds or HTTP date. Use the duration remaining at receipt, not the date as a DB timestamp. Invalid/negative hints use the normal cooldown floor. Do not parse response bodies as ipify JSON.
- [ ] Run `go test -short ./infrastructure/proxy ./infrastructure/httpclient ./infrastructure/lodestone ./infrastructure/tomestone -count=1 -race`. Existing live public-proxy tests are excluded with `-short`; local fixture tests must still execute. Run `CGO_ENABLED=0 go build ./...` and report the known unrelated logging-test blocker only if encountered by a test command.
- [ ] Commit explicit changed files: `feat(proxy): validate health responses with bounded proxy transport`.

## Task 3: Database leases, result fencing and fresh reads

**Files:** create migration `00017_proxy_scan_scheduling.sql`, repository `proxy_scan.go`; modify `proxy.go`, `proxy_test.go`, `helpers_test.go`, `mock/repository/proxy.go`, `port/contract/proxy.go`, `port/contract/proxy_scan.go`; add `mock/repository/proxy_scan_test.go`. Update constructor callsites in `container/infrastructure.go` and affected test files to pass the policy. Existing scan APIs are removed in Task 6, after caller migration; they must not be used in new code.

**Consumes:** relative `ScanUpdate`, record fields and `ProxyScanPolicy` from Task 1.

**Produces:** the `ProxyScanStore` port and both real/fake adapters, new consumer persistence methods, freshness-based existing read methods. Fake methods implement the same observable ownership and eligibility semantics.

- [ ] Before production changes, introduce an isolated real-DB test fixture. In explicit `REQUIRE_TEST_POSTGRES=1` mode, connection/migration failures call `t.Fatalf`, not `Skip`. Connect to the dedicated local test server on 5433, create a unique database per test with `CREATE DATABASE` outside a transaction, migrate it, then close connections and drop only that database in cleanup. Use a generated identifier containing only a fixed prefix and hex characters. Do not truncate another test's database or change the user's server on 5432. Retain optional skip behavior for ordinary developers without integration prerequisites.
- [ ] Write red tests for background claim exclusivity and stale completion, including:

```go
func TestCompleteScanRejectsSupersededVersion(t *testing.T) {
    ctx := context.Background()
    driver := newTestDriver(t)
    policy := contract.ProxyScanPolicy{
        VerifyInterval: time.Minute, FreshnessTTL: 2*time.Minute,
        RecoveryBase: time.Minute, RecoveryCap: 15*time.Minute,
        RecoveryHorizon: time.Hour, LeaseDuration: 30*time.Second,
        MinRepeatInterval: time.Second, InconclusiveRetry: time.Minute,
        DeadAfter: 48*time.Hour, FailThreshold: 5,
    }
    repo := repository.NewProxyRepository(driver, policy)
    id, _, err := repo.InsertIfAbsent(ctx, contract.ProxyRecord{
        Protocol: "http", IP: "192.0.2.1", Port: 8080, Source: "test",
    })
    if err != nil { t.Fatal(err) }
    leases, err := repo.ClaimScans(ctx, contract.ScanBackground, 1)
    if err != nil || len(leases) != 1 { t.Fatalf("claim: %v, %v", leases, err) }
    _, err = driver.Execute(ctx,
        `UPDATE proxies SET observation_version=observation_version+1 WHERE id=$1`, id)
    if err != nil { t.Fatal(err) }
    accepted, err := repo.CompleteScan(ctx, leases[0], contract.ScanUpdate{
        Outcome: contract.ScanSuccess, LatencyMS: 42,
        NextDelay: time.Minute, RepeatDelay: time.Second,
        FreshnessTTL: 2*time.Minute,
    })
    if err != nil { t.Fatal(err) }
    if accepted { t.Fatal("superseded scan changed health") }
    active, err := repo.ListActive(ctx, 10)
    if err != nil { t.Fatal(err) }
    if len(active) != 0 { t.Fatal("stale completion advertised a usable proxy") }
}
```

No `LastInsertId`: use `InsertIfAbsent`'s returned ID or SQL `INSERT ... RETURNING id`.
- [ ] Run `REQUIRE_TEST_POSTGRES=1 TEST_POSTGRES_PORT=5433 go test ./infrastructure/postgres/repository -run 'TestClaimScans|TestCompleteScan' -v -count=1`; confirm red.
- [ ] Add the migration, using immutable index predicates only:

```sql
-- +goose Up
ALTER TABLE proxies
 ADD COLUMN general_healthy boolean NOT NULL DEFAULT false,
 ADD COLUMN last_completed_at timestamptz,
 ADD COLUMN last_verified_at timestamptz,
 ADD COLUMN next_attempt_at timestamptz,
 ADD COLUMN scan_not_before timestamptz,
 ADD COLUMN recovery_step integer NOT NULL DEFAULT 0,
 ADD COLUMN observation_version bigint NOT NULL DEFAULT 0,
 ADD COLUMN scan_token text,
 ADD COLUMN scan_lease_until timestamptz,
 ADD COLUMN claimed_version bigint,
 ADD COLUMN destination_cooldown_until timestamptz;
CREATE INDEX idx_proxies_verification_due ON proxies(next_attempt_at, id)
 WHERE general_healthy;
CREATE INDEX idx_proxies_recovery_due ON proxies(next_attempt_at, id)
 WHERE NOT general_healthy AND last_verified_at IS NOT NULL;
CREATE INDEX idx_proxies_background_completed ON proxies(last_completed_at NULLS FIRST, id)
 WHERE NOT general_healthy;
CREATE INDEX idx_proxies_fresh_verified ON proxies(last_verified_at)
 WHERE general_healthy;
```

Write the Goose Down section dropping these indexes and columns in reverse order. Do not run Down against production. Do not put `NOW()` in a partial-index predicate (PostgreSQL rejects nonimmutable index predicates). Preserve existing timestamps/status; do not copy them into `last_verified_at`. Let the existing embedded migration directory include the new file without a second migration mechanism.
- [ ] Extend `proxyColumns`, `proxyColumnsClaimed` and their shared `scanProxy` scanner together with both repository constructors; a column added to one string but not the other produces row-scan mismatch errors only at runtime. Add the new port as embedded `ProxyScanStore` on the final `ProxyRepository`; implement the fake methods before claiming the contract complete. Fake test setup must seed fresh health through claim/completion, not by calling unversioned `UpdateStatus`.
- [ ] Implement claim in one short transaction with DB `statement_timestamp()` and `FOR UPDATE SKIP LOCKED`. Select a queue-specific static SQL predicate, never interpolate caller SQL:

```sql
-- Common predicates, with n obtained inside this statement:
(scan_lease_until IS NULL OR scan_lease_until <= n)
AND (scan_not_before IS NULL OR scan_not_before <= n)
-- Verification:
general_healthy AND (next_attempt_at IS NULL OR next_attempt_at <= n)
-- Recovery:
NOT general_healthy AND last_verified_at > n - $recovery_horizon
AND (next_attempt_at IS NULL OR next_attempt_at <= n)
-- Background:
NOT general_healthy
```

Order verification/recovery by deadline NULLS FIRST then ID, background by completion NULLS FIRST then ID. Lease token is a crypto-random 128-bit hex nonce; pairing a batch token with row ID is sufficient, but tokens must never repeat across batches. Set `claimed_version=observation_version` and lease expiry from DB time plus policy duration. Return records in selected order, not arbitrary UPDATE order. Validate queue/limit; nonpositive limit never means unlimited. No destination cooldown predicate belongs here.
- [ ] Implement fenced completion with SQL `clock_timestamp()` evaluated after acquiring the row lock. Explicitly lock the claimed row in a short transaction, then compute a single DB timestamp for the update; this prevents time spent waiting for a row lock from making an expired lease appear valid. Required predicate:

```sql
id=$id AND scan_token=$token
AND claimed_version=$version AND observation_version=$version
AND scan_lease_until > $db_now
```

Atomic state transition table:

| Outcome | Required writes |
| --- | --- |
| Success | `general_healthy=true`, success/last-alive time = DB now, completed time = DB now, `fail_count=0`, step=0, active status, measured latency, next deadline = now+verify interval, common floor = now+repeat floor, version+1 |
| Failure | `general_healthy=false`, completed time = DB now, preserve last success, increment failures, inactive/dead using existing age/count thresholds evaluated with DB now, next recovery deadline/step from update, common repeat floor only, version+1 |
| Inconclusive | completed time = DB now, preserve healthy flag/status/failure count/version/success time/step, set common cooldown and next retry deadline from update |

All accepted outcomes clear scan lease fields and update `updated_at`. A failed CAS leaves evidence untouched. Release uses ID/token/captured version so an old process cannot clear a newer lease. Failed/cancelled persistence leaves ownership to explicit release or expiry; it does not advance completion time.
- [ ] Implement one identical general-health predicate across `ListActive`, `RandomActive`, `CountByStatus`, `ClaimProxy`, and lock refresh:

```sql
general_healthy AND last_verified_at > n - $freshness_ttl
```

`CountByStatus` returns `active` only for that predicate and maps a historically active but nonfresh record to `stale`; preserve historical inactive/dead categories. Do not persist a ticking live-status column. Only `ClaimProxy` and `RefreshConsumerLock` also require expired/absent destination cooldown. Remove the stale fallback branch entirely. `RandomActive` retains existing provider-discovery semantics and ID exclusions, without Lodestone cooldown filtering; the hub's `RandomActive`, `RandomActiveExcluding` and `SwapActive` all delegate to this one repository method, so the single predicate covers all three discovery paths, and discovery's existing direct bootstrap fallback absorbs the case where no fresh proxy exists yet.
- [ ] Implement consumer failure CAS on captured `ObservationVersion`, owner and still-valid lock, using the same failure transition and DB timestamp as scan failure; invalidate the older scan lease. Consumer failure advances completed-attempt time because it is a real conclusive observation. Destination cooldown atomically sets `GREATEST(existing, DB now+delay)` and releases the owned consumer lock, without touching general evidence or scan version. For a rejected stale failure, release only the caller's current valid owned lock.
- [ ] Add red/green integration tests for: concurrent disjoint claims from two repository instances; recovery/background overlap; lease expiry and newer-token protection; replay; consumer failure fencing scanner success and reverse stale-consumer rejection; inconclusive-after-success still in verification; exact freshness boundary excluded; background ignoring a future recovery deadline but respecting common cooldown; historical active migration excluded; destination cooldown not reducing general active count; shutdown release and owner isolation. Use SQL-relative timestamps or a single transaction for exact boundary tests, not wall-clock sleeps.
- [ ] Run integration tests with required mode and `-race`, plus fake repository tests. Record all non-skipped execution. Commit explicit changed files: `feat(proxy): persist fenced scans and fresh availability`.

## Task 4: Endpoint guard with generation fencing

**Files:** create `infrastructure/proxy/guard.go`, `guard_test.go`, `mock/proxy/guard.go`; add `ProxyEndpointGuard` port from the contract block if not already declared.

**Consumes:** `HealthChecker.CheckDirect`, `GuardSnapshot`.

**Produces:** `NewEndpointGuard(check func(context.Context) error, interval time.Duration, logger contract.Logger) *EndpointGuard`, implementing `Snapshot` and `Run`. The callback is bound to the real health checker in the service locator. Fake implements the same port with controlled transitions.

- [ ] Write failing state-transition tests using a channel-controlled direct checker. Record unhealthy initial state, successful first control → healthy, failed control → unhealthy and generation increment, later success → healthy without resetting generation. Repeated failures need not keep incrementing because the first transition already invalidated in-flight failure attribution.
- [ ] Run `go test ./infrastructure/proxy -run TestEndpointGuard -count=1`; confirm red.
- [ ] Implement one consistent snapshot under a mutex, not independently loaded health/generation fields. Immediate startup control, serial subsequent controls, interval jitter ±10%, no overlap. The control callback uses the complete HTTPS/ipify validation and a ten-second timeout; HTTP 200 without JSON is not a healthy baseline. Cancellation stops controls promptly.

```go
func (g *EndpointGuard) Snapshot() contract.GuardSnapshot {
    g.mu.Lock()
    defer g.mu.Unlock()
    return g.state
}
// After a completed control request, while holding g.mu:
if err != nil && g.state.Healthy { g.state.Generation++ }
g.state.Healthy = err == nil
```

- [ ] Verify with a local callback that blocks until cancelled, and a second callback that counts concurrent calls. The maximum must be one. Use the real direct checker fixture for valid JSON, invalid payload and target error tests; direct success cannot mutate any proxy row because the guard has no repository reference.
- [ ] Run `go test ./infrastructure/proxy -run TestEndpointGuard -race -count=1`. Commit explicit files: `feat(proxy): guard scan classification against endpoint outages`.

## Task 5: Fair admission and bounded scan worker

**Files:** create `domain/proxy/worker/admission.go`, `admission_test.go`; rewrite `domain/proxy/worker/scan.go`, update `scan_test.go`; use existing fake repository/checker and new fake guard. Keep the existing scan notifier only for accepted successful completion.

**Consumes:** `ProxyScanStore`, `ProxyChecker`, `ProxyEndpointGuard`, `ProxyScanPolicy`, `MakeScanUpdate`.

**Produces:**

```go
func NewScanWorker(store contract.ProxyScanStore, checker contract.ProxyChecker,
    guard contract.ProxyEndpointGuard, policy contract.ProxyScanPolicy,
    weights [3]int, logger contract.Logger) *ScanWorker
func (w *ScanWorker) RunScan(ctx context.Context, concurrency int) error
func (w *ScanWorker) SetNotifier(fn func())
// package-private admission functions/types:
func reservationTargets(concurrency int, weights [3]int) [3]int
type admission struct { credits [3]int; borrower int }
func (a *admission) chooseSmall(available [3]bool, weights [3]int) (int, bool)
```

- [ ] Write red tests for integer allocation and persistent small-worker credit. Exact large-worker example follows the approved minimum-one-then-remainder rule: 150 workers → `[16,67,67]`, not a fabricated exact 10/45/45 split.

```go
func TestSmallAdmissionDoesNotStarveVerification(t *testing.T) {
    var a admission
    counts := [3]int{}
    for i := 0; i < 100; i++ {
        q, ok := a.chooseSmall([3]bool{true, true, true}, [3]int{10,45,45})
        if !ok { t.Fatal("no queue selected despite available work") }
        counts[q]++
    }
    if counts != [3]int{10,45,45} { t.Fatalf("admissions: %v", counts) }
}
```

- [ ] Run `go test ./domain/proxy/worker -run 'TestSmallAdmission|TestReservation' -count=1`; confirm red.
- [ ] Implement ≥3-worker allocation without sorting allocations or floats: reserve `[1,1,1]`, compute `remaining=concurrency-3`, add `remaining*weight/100` to each, distribute the at-most-two leftovers by `(remaining*weight)%100`, deterministic index ties. Validate positive concurrency and positive queue weights at configuration boundaries.
- [ ] Implement persistent smooth weighted round-robin for one/two workers. Never recreate credits per admission:

```go
func (a *admission) chooseSmall(available [3]bool, weights [3]int) (int, bool) {
    total, best := 0, -1
    for i := range weights {
        if !available[i] { a.credits[i] = 0; continue }
        total += weights[i]
        a.credits[i] += weights[i]
        if best < 0 || a.credits[i] > a.credits[best] { best = i }
    }
    if best < 0 { return 0, false }
    a.credits[best] -= total
    return best, true
}
```

- [ ] Write failing end-to-end worker tests with channel barriers, not timing guesses: block each admitted check, observe maximum starts equals concurrency; keep recovery and background populated while introducing due verification; free one slot and verify under-reservation work is offered first. Fill only background and require all slots to start background work. At concurrency 1 and 2, sustain all queues and verify each progresses. Use `CompleteScan`-observable results rather than tests that merely assert helper calls.
- [ ] Implement one dispatcher owning free/reserved/in-flight counters, not goroutines independently picking work. At each admission pass: refresh due-queue availability via bounded claims, fill under-reservation queues first, then round-robin borrowers. Claimed rows count against free capacity immediately, and execute immediately. A queue returning empty does not make other queues wait. Retry empty queues at most one second later and on completion notifications; this bounds verification reacquisition while background is continuously busy. On database failure pause claims briefly with context-aware backoff, without reclassifying proxies.
- [ ] Bound every claim RPC by both available slots and a context timeout shorter than the lease. Use a completion channel sized to concurrency and a fixed executor population; no per-result indefinite goroutine accumulation. Do not hold DB connections while checking. The new worker replaces `SplitScanConcurrency`, prefetch buffers, creditPool and both old scan pools; migrate `cmd/cli/proxy.go` constructor/run call in the same compile-complete change, wiring the new dependencies finalized by Task 6.
- [ ] For each execution, capture guard snapshot before I/O and after I/O, compute relative update with `MakeScanUpdate`, then recheck guard immediately before persistence and downgrade failures if its generation changed. The spec's guarantee is local observation fencing, not globally synchronized outage detection; document the small concurrent-update attribution window rather than promise impossible distributed atomicity. If parent context was cancelled, release ownership and do not claim a network failure. On accepted success only, notify.
- [ ] On shutdown: stop claims, cancel checks, wait for bounded network completion; release outstanding owned leases using a fresh bounded cleanup context. If persistence cleanup fails, expiry recovers them. Recovered panics release capacity/lease and log a local error; never mark the proxy dead. Test process-style cancellation and release failure.
- [ ] Run `go test ./domain/proxy/worker -race -count=1 -timeout 300s`, including guard-startup pause, guard transition during a check, persistence failure, discarded completion not notifying, borrowing, minimum concurrency and no double ownership. Commit explicit files: `feat(proxy): schedule fair scans within bounded capacity`.

## Task 6: Configuration, ingestion and service-locator cutover

**Files:** `config/config.go`, `config/config.toml`, `config/config_test.go`; `container/infrastructure.go`, `container/domain.go`; `cmd/cli/proxy.go`; `domain/proxy/service.go`, `service_test.go`, relevant handler tests; `port/contract/proxy.go`; delete obsolete scan APIs/implementations from real/fake repositories and migrate their remaining tests.

**Consumes:** Tasks 1–5. May be executed together with Task 5's constructor cutover so no uncompilable commit is left behind.

**Produces:** one executable scanner path, validated settings, insertion-only discovery ingestion and no legacy status writer bypass.

- [ ] Write red config tests for invalid durations/URLs/weights and incompatible freshness/lease constraints, using `t.Setenv` for real Viper overrides. A default-literal test has no behavioral value; remove the existing timeout string pin rather than re-pin it to `10s`.

```go
func TestNewConfigRejectsExpiredBeforeRecheck(t *testing.T) {
    t.Setenv("PROXY_SCAN_FRESHNESS_TTL", "65s")
    t.Setenv("PROXY_SCAN_VERIFICATION_INTERVAL", "60s")
    t.Setenv("PROXY_TEST_TIMEOUT", "10s")
    if _, err := NewConfig(); err == nil {
        t.Fatal("accepted freshness that expires before the bounded recheck")
    }
}
```

- [ ] Run `go test ./config -run TestNewConfigRejects -count=1`; confirm red before changes.
- [ ] Add duration-typed scanner fields using the existing Viper duration decode support, validate after Unmarshal, and fail on malformed explicit values. Keep unrelated string fields untouched. Add exact configuration:

```toml
[proxy]
test_url = "https://api64.ipify.org?format=json"
test_timeout = "10s"
# Retain existing dead_threshold_days and fail_count_threshold.
# Delete dead_scan_interval_days.

[proxy.scan]
verification_interval = "60s"
freshness_ttl = "120s"
recovery_base = "1m"
recovery_cap = "15m"
recovery_horizon = "1h"
weight_verification = 10
weight_recovery = 45
weight_background = 45
lease_duration = "30s"
min_repeat_interval = "1s"
inconclusive_retry = "60s"
control_interval = "30s"

[proxy.consumer]
# Retain lock_ttl, lodestone_rate_limit and request_timeout.
test_url = "https://na.finalfantasyxiv.com/lodestone/"
test_timeout = "10s"
cooldown = "60s"
```

Environment names are uppercase keys with dots/hyphens replaced by underscores: `PROXY_TEST_URL`, `PROXY_TEST_TIMEOUT`, `PROXY_SCAN_VERIFICATION_INTERVAL`, `PROXY_SCAN_FRESHNESS_TTL`, `PROXY_SCAN_RECOVERY_BASE`, `PROXY_SCAN_RECOVERY_CAP`, `PROXY_SCAN_RECOVERY_HORIZON`, `PROXY_SCAN_WEIGHT_VERIFICATION`, `PROXY_SCAN_WEIGHT_RECOVERY`, `PROXY_SCAN_WEIGHT_BACKGROUND`, `PROXY_SCAN_LEASE_DURATION`, `PROXY_SCAN_MIN_REPEAT_INTERVAL`, `PROXY_SCAN_INCONCLUSIVE_RETRY`, `PROXY_SCAN_CONTROL_INTERVAL`, `PROXY_CONSUMER_TEST_URL`, `PROXY_CONSUMER_TEST_TIMEOUT`, `PROXY_CONSUMER_COOLDOWN`. Add defaults for all keys so Viper enumerates env overrides during Unmarshal.

Validate HTTPS URL with host and no credentials; positive durations and weights; weights sum 100; freshness strictly greater than interval+check deadline; lease strictly greater than check deadline; cap≥base; min-repeat≤inconclusive retry. Do not silently clamp invalid user settings. Do not add an unrelated new database-pool configuration tree.
- [ ] Add failing ingestion regression proving newly inserted proxy has no fresh evidence and no independent health I/O. Change `ProcessNewProxy` to retain dedup/insertion/logging but leave actual scanning to background. This puts all high-volume general checks behind the guard, reservations and lease fence. Delete the unleased `processProxyCheck` / `ProcessScanProxy` path and update queue handler/service comments. RabbitMQ acknowledges insertion, not unleased verification.
- [ ] Wire `ProxyChecker()` to general ipify checker and a separate `DestinationChecker()` to `NewChecker` using consumer settings. `ProxyHub()` and provider-client hub construction use the destination checker for handout only; random provider discovery does not invoke it. `ProxyService` is ingestion-only and no longer needs a checker. Start the endpoint guard from the scan command's cancellable lifecycle, not a lazy accessor spawning unmanaged goroutines.
- [ ] Pass the single parsed policy into repository and worker constructors. Use the same policy for all fresh consumers. Scan deployment's existing Postgres env changes to 2/1 in Task 8; no second pool in one process is necessary. Keep required queue configuration even though scanning bypasses RabbitMQ.
- [ ] Remove `--dead-scan-percentage` registration/read/logging and old help text. Retain `-c` as total per-replica concurrency; invalid nonpositive explicit concurrency fails clearly. Remove `ListForScan`, `ListDeadForScan`, `UpdateScanTime`, `UpdateStatus`, obsolete narrow `Scanner`, split-pool helpers, stale aliases and their obsolete tests. Run LSP references before each exported removal and migrate all matches. Constructor and old setup-call migrations include `domain/census/worker/proxy_worker_test.go`, `domain/proxy/hub_test.go`, `infrastructure/httpclient/rotating_proxy_client_test.go` and repository tests.
- [ ] Run `go test ./config ./domain/proxy/... ./mock/... ./cmd/cli -count=1` and `CGO_ENABLED=0 go build ./...`. Launch `./bin/ffxiv-census proxy scan --help` after `make build`: check actual updated usage and rejection of the removed flag, not a source-string assertion. Commit explicit files: `feat(proxy): wire the guarded scanner and retire windowed scans`.

## Task 7: Fresh handout and typed consumer failure handling

**Files:** `domain/proxy/hub.go`, `proxy.go`, `hub_test.go`, `proxy_test.go`; `domain/census/worker/worker.go`, `proxy_worker_test.go`; `infrastructure/lodestone/client.go`, `client_test.go`; `infrastructure/tomestone/client.go`, `client_test.go`; `mock/repository/proxy.go` if consumer behavior tests expose parity gaps.

**Consumes:** destination checker, typed errors and Task 3 persistence. General-health repository predicates are already fresh.

**Produces:** version-fenced consumer use without converting provider responses/business errors into proxy death.

- [ ] Write failing hub scenarios using real fake-repository transitions: successful general check then destination 429 returns no proxy, preserves `ListActive` availability/count and failure count, but prevents another Lodestone claim until cooldown. A transport refusal removes general availability and rejects an older scanner completion. An expired general success can never be handed out even if destination checker returns success.
- [ ] Write a pure classification regression in census worker:

```go
func TestTargetErrorDoesNotBecomeGeneralFailure(t *testing.T) {
    err := fmt.Errorf("request: %w", &contract.ProxyCheckError{
        Kind: contract.CheckTarget, Reason: "http_503",
        Err: errors.New("Service Unavailable"),
    })
    var e *contract.ProxyCheckError
    if !errors.As(err, &e) { t.Fatal("lost typed cause") }
    if isGeneralProxyFailure(err) { t.Fatal("target 503 killed general health") }
}
```

`isGeneralProxyFailure(error) bool` is private to `domain/census/worker/worker.go`; it returns true only for typed `CheckProxy`. Ambiguous deadline/TLS/read errors are not general failures in this unguarded destination path.
- [ ] Run `go test ./domain/proxy ./domain/census/worker -run 'TestNewProxy|TestTargetError|TestConsumer' -count=1`; confirm red.
- [ ] Update hub to claim fresh/uncooldowned rows, run destination checker, and refresh/validate ownership and freshness again before returning (freshness can expire during the check). Destination success does not renew ipify time. On typed conclusive proxy failure, persist `RecordConsumerFailure` using the version captured before I/O. On destination error, persist cooldown and release ownership. On local error/cancellation, release without failure. Propagate DB failures, never silently hand out the row.
- [ ] Replace `Proxy.CanUse`'s local-time/status shortcut plus `ExtendLock` with `RefreshConsumerLock`, store its returned record/version before the next job. `Proxy.IsActive` must no longer be used as a live-authoritative local status predicate; remove if references show it unused, otherwise replace callers with the DB-checked path. Remove unversioned `MarkFailedProxy`; domain `MarkFailed` uses the captured record and relative failure update.
- [ ] Replace census `isProxyError` string matching with typed provenance on both first-attempt and retry paths, including the direct retry `MarkFailed` call. Shared transport wraps proven proxy-dial errors; provider adapters preserve `%w` so `errors.As` reaches the type. Unrecognized errors remain ordinary job errors. Fix outdated rotation comments to match actual release-before-reacquire code; do not reintroduce hold-and-wait pool deadlocks.
- [ ] Preserve existing provider rate limiting and 429 nonrotation behavior. When a Lodestone target error has Retry-After, apply the destination cooldown and provider pause without immediately using another identity to bypass it. Return normal job retry. Do not add a retry loop to this feature. Preserve existing Tomestone unauthenticated/not-found errors and Lodestone achievement-private 403 semantics: those are business outcomes, not proxy observations. Only actual destination-check rejection or propagated target errors invoke cooldown. Acquisition homepage 403 is distinct from private-achievement 403.
- [ ] Add tests with a scanner success during a long consumer job: stale consumer failure is rejected and cannot overwrite it. Add valid consumer failure during scan: late scan success is rejected. Test general counts during destination cooldown and post-check freshness expiry. Preserve rotating-provider client's existing direct discovery fallback; the no-direct-fallback rule applies to health checks and Lodestone proxy use, not this unrelated discovery policy.
- [ ] Run `go test -short ./domain/proxy/... ./domain/census/worker ./infrastructure/lodestone ./infrastructure/tomestone ./infrastructure/httpclient -race -count=1`. Commit explicit files: `feat(proxy): fence consumer failures and separate destination cooldowns`.

## Task 8: Operational cutover and evidence-producing runtime exercise

**Files:** targeted scanner hunks in `k8s/values.yaml`; `docs/proxy.md`, `docs/architecture.md` and affected existing configuration guides after the runtime smoke proves behavior. Change `k8s/templates/workers.yaml` only if rendering proves necessary; its environment replacement behavior already supports the settings. No new UI or metrics platform.

**Consumes:** the integrated binary and all previous contracts.

**Produces:** verified local scanner behavior, honest scaling measurements and an actionable coordinated release procedure. Does not perform a production rollout.

- [ ] Add structured accepted-completion logging in the worker's existing logger: queue, outcome, bounded reason, duration, accepted/discarded status, recovery step, source, prior health and verification lateness. Log guard transitions, claim latency/errors, consumer successful handout and typed failure. Only accepted results contribute to throughput calculations. Use the existing logger contract; do not import infrastructure metrics into domain.
- [ ] Use existing repository reads plus periodic SQL for experiment health totals, oldest completed background age, unknown-history count and overdue verification count. Distinguish locally logged attempts from accepted persisted observations. This supplies spec observability without adding an independent telemetry subsystem.
- [ ] Render current chart with `helm template` and inspect actual worker args/env before changes. Preserve five replicas, concurrency 150, resources and every required queue env. Remove only `--dead-scan-percentage` and its value; change scanner instance `POSTGRES_MAX_OPEN_CONNS` from 10 to 2 and idle from 5 to 1. Defaults already contain scan parameters; do not add redundant env overrides to test for their existence. Render again and compare the effective deployment. Keep user changes unstaged except these approved hunks.
- [ ] Run targeted suites and build:

```bash
CGO_ENABLED=0 go build ./...
go vet ./domain/proxy/... ./infrastructure/proxy ./infrastructure/httpclient ./config
go test -short ./domain/proxy/... ./domain/census/worker ./infrastructure/proxy ./infrastructure/httpclient ./infrastructure/lodestone ./infrastructure/tomestone ./config ./mock/... -race -count=1 -timeout 300s
REQUIRE_TEST_POSTGRES=1 TEST_POSTGRES_PORT=5433 go test ./infrastructure/postgres/repository -race -count=1 -v -timeout 300s
make build
```

Read the verbose integration output: missing DB or skips do not count as proof. Full-suite logging-test compile failure and missing golangci-lint executable are known unrelated limitations, not reasons to alter the user's file. Report exact command outcomes. Do not repeatedly rerun them merely to reconfirm the already reported state.
- [ ] Launch the actual binary against the isolated database and local endpoint/proxy fixtures. Build a throwaway Go fixture runner in a scratch directory under the module so it can use local packages; it creates a TLS ipify target and HTTP CONNECT, HTTPS CONNECT, SOCKS4 and SOCKS5 endpoints, trusts only its fixture CA, and seeds records via `InsertIfAbsent`. Local CA goes through a scratch `SSL_CERT_FILE` environment entry for the scanner process; production TLS validation remains enabled. No synthetic traffic reaches public ipify or Lodestone.
- [ ] Exercise: valid body activates; target invalid JSON stays inconclusive; refused proxy fails; handshake/body stall ends by ten seconds; guard outage pauses new claims; successful control resumes; stop scanners and observe every fresh list/count/acquisition expires at 120 seconds. Query the DB and exercise hub acquisition with a local destination checker. Verify 429 preserves general health while preventing destination handout. Launch two scanners to verify useful concurrent claims and accepted completions, not just increasing goroutine count.
- [ ] Scaling procedure: reset the same isolated dataset for each run, seed at least 10,000 distinct endpoint records pointing to local controlled proxy fixtures, run 1, 2, then 5 independent scanner processes at identical per-process concurrency and policy. Use a mixed population of delayed success, immediate refusal and ten-second stall; collect per-run accepted completions/second, background progress, verification lateness, DB connections and CPU/network load. Run long enough to include active rechecks and at least two lease periods. Keep workload proportions/delays and measurement window equal. If the host saturates, lower concurrency equally and rerun rather than assert linear scaling. Acceptance is increased useful throughput where the controlled host has spare resources and no queue starvation, not a prescribed fabricated multiplier.
- [ ] Kill one scanner during checks, record affected lease IDs, and prove another process reclaims them after expiry. Expired lease rows may remain until selected; that is normal lazy reclamation, not a leak. Acceptance requires reclaimability and stale-token rejection, not a reaper clearing every expired row. Check pool limits from `pg_stat_activity`, including test-fixture/admin connections separately.
- [ ] After the runtime smoke succeeds, remove scratch runner/data/CA/processes and update existing operational docs. Document general vs destination health, stale status, all configuration names, bounded connection budgeting and actual measurements. Review prose for filler and unsupported guarantees; the unslop skill is unavailable locally, so apply its editing principles directly.
- [ ] Document rollout exactly: stop old proxy writers/consumers and old live readers, migrate with new binary, start new scanners, observe actual fresh availability, then start new consumers/readers. Do not backfill fake success. No blind rolling mix, automatic destructive rollback, image release or production deploy without user authorization. README release procedure governs any later approved release.
- [ ] Re-run reference search for removed symbols and confirm no code/config callers remain; historical specs/plans remain historical documents, not compatibility APIs. Verify `cmd/http` still has no live proxy view, and note it intentionally unchanged. Stage only owned source/docs and scanner configuration hunks, commit with `feat(proxy): finish scanner cutover and operational verification`, and push the implementation branch after approval of execution.

## Plan review and acceptance mapping

| Specification requirement | Owning tasks / evidence |
| --- | --- |
| All locally unknown history stays eligible | 3 migration/background predicates; 8 initial sweep |
| Recovery cadence and no background starvation | 1 relative deadlines; 3 separate common cooldown; 5 persistent fair admission |
| Fresh active counts, lists, random and acquisition | 3 strict DB-time predicate; 7 post-check validation; 8 scanners-stopped smoke |
| 10-second complete validation and no direct fallback | 2 separate TLS target/tunnel fixtures and cancellation tests |
| ipify outage/inconclusive handling | 1 classification, 4 serial direct guard, 5 generation check, 8 pause/resume |
| Destination checks do not renew ipify health | 7 hub and consumer tests |
| Consumer/scanner stale result fencing | 3 both CAS directions; 7 job-time version capture |
| Lease crash recovery and no claimed prefetch | 3 lease SQL; 5 bounded worker; 8 killed-process experiment |
| Replica throughput with DB budget | 8 measured 1/2/5 process runs and existing pool overrides |
| Clean configuration/API cutover | 6 reference-driven removals, 8 rendered chart |
| Pure-Go build and isolated real-SQL tests | 2/3/8 command evidence |
| Coordinated migration and initial revalidation | 3 no success backfill; 8 operational docs, no deployment |

Review performed before publication: production persistence has no caller clock; latest conclusive result has its own boolean rather than timestamp inference; cooldown does not prevent background exploration; stale fallback is removed; static indexes contain no clock calls; SQL fencing checks current observation version and strictly unexpired ownership; small-worker credits persist; no abandoned SOCKS dial goroutines; no speculative UI or pool subsystem. Code snippets name functions and types defined in this plan. Tests/experiments described here are execution requirements, not claims that implementation has already passed.
