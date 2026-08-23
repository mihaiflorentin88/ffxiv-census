# Custom Lodestone Client — Implementation Plan

Date: 2026-08-23
Status: Complete

## Context

The original Lodestone adapter wrapped `godestone` (v2) + `bingode` (embedded game-data provider). While functional, godestone pulled in heavy transitive dependencies (colly, chromedp, etc.) and imposed architectural constraints:

- **No request-level context cancellation** — godestone's colly collectors expose no HTTP timeout or ctx, so in-flight requests can't be cancelled.
- **Rate limiting was per-call, not per-request** — `FetchCharacter` internally issues 2 HTTP requests, so character throughput was `2 × rate_limit`.
- **Memory footprint** — godestone + bingode loaded game data tables into memory even though we only need achievement names for milestone filtering.
- **Dependency weight** — `go.mod` carried ~30 transitive dependencies from godestone/bingode that served no purpose in our scraping use case.

The custom client replaces godestone with direct HTML scraping via the Lodestone achievement detail endpoint, achieving the same data with a fraction of the dependencies and better control over rate limiting, retries, and proxy support.

## What Changed

### 1. Contract types (`port/contract/lodestone.go`)

Lightweight DTOs replacing godestone's heavy structs:

- `CharacterProfile` — 15 fields: ID, Name, World, Datacenter, Region, Race, Tribe, Gender, GrandCompany, FreeCompanyID, FreeCompanyName, AchievementsPrivate, LatestAchievementID, LatestAchievementDate, ClassJobs (slice of `ClassJob`).
- `ClassJob` — Name, Level, Exp.
- `AchievementResult` — 4 fields: CharacterID, Achievements (slice), Private, Error.
- `AchievementSummary` — 5 fields: ID, Name, Description, Icon, Date.

Interface unchanged: `FetchCharacter(ctx, id)`, `FetchAchievements(ctx, id)`.

### 2. Custom client (`infrastructure/lodestone/client.go`)

Replaces `infrastructure/lodestone/lodestone.go` (deleted). New implementation:

- **Direct HTTP scraping** — fetches `/lodestone/character/{id}/achievement` HTML, parses with `golang.org/x/net/html`.
- **Sequential milestone checking** — instead of fetching all achievements, checks milestones one at a time via `/lodestone/character/{id}/achievement/detail/{achievement_id}`. Max 7 requests (one per expansion MSQ), early exit on first match.
- **Rate limiting** — token bucket (`golang.org/x/time/rate`) + `ProviderRateLimiter` 429 pause integration.
- **Proxy support** — HTTP/HTTPS/SOCKS4/SOCKS5 via `infrastructure/httpclient` rotating proxy client.
- **Retries** — exponential backoff (500ms × 2^attempt), max configurable attempts.
- **Privacy check** — detects "This character's achievements are set to private" text.
- **30s request timeout** — hardcoded per-request HTTP timeout (acceptable; godestone had none).

### 3. Service layer (`domain/census/service.go`)

- `UpsertCharacter` — maps `CharacterProfile` fields to database columns (unchanged interface, new field names).
- `ProcessMilestoneResults` — processes `AchievementResult` from sequential checker.
- `profileToRecord` / `profileToJobs` — field mapping updated for new DTO names (e.g., `Race` instead of `Race.Name`).

### 4. Achievement handler (`domain/census/handler/achievement.go`)

- `FetchAchievements` signature updated to accept `CharacterProfile` (needed for privacy check and character context).
- `ProcessMilestoneResults` called with new `AchievementResult` type.
- Skip-when-fresh logic preserved (unchanged behavior).

### 5. Container wiring (`container/infrastructure.go`, `cmd/cli/consume.go`)

- `NewCustomClient` used everywhere instead of godestone-backed constructor.
- `rateLimiter` passed correctly to client constructor.
- `LodestoneClient()` accessor returns custom client.

### 6. Mock (`mock/lodestone/lodestone.go`)

- Interface assertion holds — `LodestoneClient` interface unchanged.
- Signatures match new DTO types.

## Files Modified

| File | Change |
|------|--------|
| `port/contract/lodestone.go` | New lightweight DTOs (CharacterProfile, AchievementResult, AchievementSummary, ClassJob) |
| `infrastructure/lodestone/client.go` | **New file** — custom HTTP scraper with sequential milestone checking |
| `infrastructure/lodestone/lodestone.go` | **Deleted** — old godestone wrapper |
| `infrastructure/lodestone/lodestone_test.go` | **Deleted** — old godestone tests |
| `domain/census/service.go` | Updated field mappings for new DTOs |
| `domain/census/service_test.go` | Updated test fixtures |
| `domain/census/handler/achievement.go` | Updated FetchAchievements signature, ProcessMilestoneResults |
| `domain/census/handler/achievement_test.go` | Updated test fixtures |
| `domain/census/handler/character_test.go` | Updated test fixtures |
| `domain/census/handler/idsweep_test.go` | Updated test fixtures |
| `domain/census/handler/logging_test.go` | Updated test fixtures |
| `domain/census/worker/proxy_worker_test.go` | Updated test fixtures |
| `container/infrastructure.go` | NewCustomClient wiring |
| `cmd/cli/consume.go` | NewCustomClient wiring |
| `mock/lodestone/lodestone.go` | Updated mock types |
| `go.mod` | Removed godestone, bingode, and transitive deps |
| `go.sum` | Cleaned after dependency removal |

## Performance Impact

| Metric | Before (godestone) | After (custom) | Delta |
|--------|-------------------|----------------|-------|
| Dependencies in go.mod | ~30 transitive | 2 new (`golang.org/x/net`, `golang.org/x/time`) | **-28 deps** |
| Binary size | Larger (colly, chromedp) | Smaller | **Reduced** |
| Achievement fetch | All achievements (paginated) | Sequential milestone check (max 7 requests) | **~90% fewer requests** |
| Rate limit granularity | Per-call (2 HTTP reqs per FetchCharacter) | Per-request | **Accurate throttling** |
| Context cancellation | Not supported | Full ctx support | **Graceful shutdown** |
| Proxy support | None (godestone used its own HTTP client) | HTTP/HTTPS/SOCKS4/SOCKS5 | **New capability** |

## Verification Results

```
go build ./...                    ✅ Pass
go test ./... -count=1            ✅ All 25 packages pass
go vet ./...                      ✅ Clean
go mod tidy                       ✅ No changes (already clean)
```

**Zero godestone/bingode references in:**
- `go.mod` — not present
- `go.sum` — not present
- Go source files — not imported

## Minor Observations (non-blocking)

- `character_name` omitted from some client-level log lines — name unavailable at that layer; handler compensates with full context.
- `Gender` field not parsed from HTML — unused downstream, can add later if needed.
- 30s request timeout hardcoded — acceptable for now; can make configurable later.
