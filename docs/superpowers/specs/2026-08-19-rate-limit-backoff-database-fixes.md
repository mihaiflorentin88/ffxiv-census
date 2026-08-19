# Rate Limiting, Backoff & Database Migration Fixes — Design Spec

**Date:** 2026-08-19
**Status:** Approved
**Scope:** Three production fixes for the ffxiv-census ingest pipeline

## Problem Statement

Three issues were identified during codebase review:

1. **Zero backoff on Lodestone retries.** The `backoffBase` field in `lodestone.Client` is never initialized — it defaults to `0`. The exponential backoff formula `backoffBase * 2^attempt` evaluates to zero for every retry, meaning retries fire immediately (gated only by the 1 req/s token bucket).

2. **Lodestone-only events stall on rate limits.** `achievement-census` and `fc-census` handlers don't receive a `rateLimiter`. When Lodestone is rate-limited (429), the handler calls Lodestone, gets an error, returns it — the worker retries the queue job with exponential backoff (5s→10s→20s→...). Meanwhile, `isEventTypeAvailable()` blocks claiming these jobs when Lodestone is paused, causing them to pile up idle.

3. **Production uses shared `postgres` database.** `k8s/values.yaml` sets `externalPostgres.database: postgres`, causing all production data to land in the shared `postgres` database on the CNPG cluster instead of a dedicated `ffxiv_census` database.

## Design Decisions

### Fix 1: backoffBase Default (500ms)

**Decision:** Set `backoffBase: 500 * time.Millisecond` in `newClient()`.

**Rationale:** 500ms base with exponential backoff produces 500ms → 1s → 2s gaps. Combined with the existing 1 req/s token bucket (the primary defense), this adds breathing room between retries without slowing the happy path. The token bucket still gates every attempt at 1 req/s — the backoff only *increases* the gap between retries when errors occur.

**Retry timing with default `max_retries = 3`:**

| Attempt | Delay | Cumulative |
|---------|-------|------------|
| 0 → 1 | 500ms | 500ms |
| 1 → 2 | 1s | 1.5s |
| 2 → 3 | 2s | 3.5s |

**No risk to IP bans:** Adding 500ms base backoff only increases the gap between retries, never decreases it. Jitter (±10%) further spreads requests.

### Fix 2: Handler-Level WaitUntilAvailable

**Decision:** Add `rateLimiter` field to `AchievementCensus` and `FreeCompanyCensus` handlers. Before making Lodestone calls, check `IsAvailable(contract.ProviderLodestone)` and if unavailable, call `WaitUntilAvailable(ctx, contract.ProviderLodestone)` to block until the cooldown expires, then retry inline.

**Why handler-level, not worker-level:** The worker's `isEventTypeAvailable()` prevents *new* claims when a provider is down. But jobs already claimed before the rate limit was detected need handler-level protection. `WaitUntilAvailable` handles this case.

**Variadic constructor pattern:** Both constructors use `rateLimiter ...contract.ProviderRateLimiter` for backward compatibility — existing test calls without the argument continue to work.

**No infinite loop risk:** `WaitUntilAvailable` blocks exactly once per rate-limit cooldown. If the subsequent fetch fails again (new 429), the handler returns an error and the queue's exponential backoff kicks in. The handler does NOT re-enter `WaitUntilAvailable` in a loop.

**Concurrency impact:** One worker goroutine blocks during the wait. With `concurrency=4` and 1 goroutine per event type, the other 3 continue processing. Kubernetes' 180s termination grace period covers even extended Lodestone cooldowns.

### Fix 3: Database Migration

**Decision:** Change `externalPostgres.database` from `postgres` to `ffxiv_census` in `k8s/values.yaml`. Dump census tables from the `postgres` database, restore to `ffxiv_census`, verify row counts match, then redeploy.

**Why not just change the DSN:** The k8s templates construct `POSTGRES_DSN` from component variables. In Kubernetes, `env` entries take precedence over `envFrom` entries when names collide — so changing `externalPostgres.database` is sufficient and necessary.

**Safety:** Old data is NOT deleted — the `postgres` database retains all rows as a backup.

## Trade-offs

- **500ms vs configurable backoffBase:** Chosen 500ms as a sensible default. Making it configurable via `LodestoneConfig` is possible but adds complexity for a value that rarely needs tuning. The token bucket is the primary rate control; backoff is secondary.
- **Handler blocking vs immediate error return:** Blocking one goroutine is acceptable given concurrency=4. The alternative (returning error immediately) causes 30s idle + queue retry + 5s backoff = ~35s wasted. Inline waiting is more efficient.
- **Single WaitUntilAvailable vs loop:** A loop would be more resilient but risks unbounded blocking. Single wait + error return on re-failure is the safer choice.
