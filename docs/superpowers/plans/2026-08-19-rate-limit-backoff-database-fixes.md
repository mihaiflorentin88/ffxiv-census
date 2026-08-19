# Rate Limiting, Backoff & Database Migration Fixes — Implementation Plan

**Date:** 2026-08-19
**Spec:** `docs/superpowers/specs/2026-08-19-rate-limit-backoff-database-fixes.md`

## Phase 1: Code Changes

### Fix 1 — backoffBase Default
**File:** `infrastructure/lodestone/lodestone.go`
- Add `backoffBase: 500 * time.Millisecond` to the `Client` struct literal in `newClient()`.

### Fix 2 — Rate Limiting for achievement-census & fc-census

#### 2a: Add `rateLimiter` field to both handlers
**Files:** `domain/census/handler/achievement.go`, `domain/census/handler/free_company.go`
- Add `rateLimiter contract.ProviderRateLimiter` field to both structs.
- Change constructors to accept variadic `rateLimiter ...contract.ProviderRateLimiter`.

#### 2b: Add `WaitUntilAvailable` in Handle methods
**Files:** `domain/census/handler/achievement.go`, `domain/census/handler/free_company.go`
- Before Lodestone calls, check `h.rateLimiter != nil && !h.rateLimiter.IsAvailable(contract.ProviderLodestone)`.
- If unavailable, call `h.rateLimiter.WaitUntilAvailable(ctx, contract.ProviderLodestone)`.
- Log the wait event for observability.

#### 2c: Update container registration
**File:** `container/domain.go`
- Pass `s.ProviderRateLimiter()` to `NewAchievementCensus` and `NewFreeCompanyCensus`.

#### 2d: Add new tests
**Files:** `domain/census/handler/achievement_test.go`, `domain/census/handler/free_company_test.go`
- `TestAchievementCensus_WaitsForRateLimitedLodestone` — pause Lodestone for 100ms, verify handler waits and succeeds.
- `TestFreeCompanyCensus_WaitsForRateLimitedLodestone` — same pattern.

### Fix 3 — Database Migration (k8s values only)
**File:** `k8s/values.yaml`
- Change `externalPostgres.database: postgres` to `externalPostgres.database: ffxiv_census`.

## Phase 2: Test Gate
1. `make test` — all tests pass
2. `make lint` — zero lint errors
3. `make fmt` — verify formatting
4. `go test -race ./...` — race detector clean

## Phase 3: Database Migration
1. Verify `ffxiv_census` database exists on CNPG cluster
2. Dump census tables from `postgres` database
3. Restore to `ffxiv_census` database
4. Verify row counts match for all tables
5. Spot-check data integrity (random character IDs, foreign key consistency)
6. Verify `goose_db_version` shows all migrations applied

## Phase 4: Documentation Updates
- `docs/lodestone.md` — document backoffBase default and retry timing
- `docs/events.md` — document WaitUntilAvailable behavior for Lodestone-only events
- `docs/external-postgres.md` — update database name from `postgres` to `ffxiv_census`

## Phase 5: Commit, Push & Release
- `git add` all changed files
- `git commit` with descriptive message
- `git push origin`
- Tag, build, and deploy per release workflow
