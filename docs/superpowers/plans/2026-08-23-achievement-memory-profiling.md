# Achievement Consumer Memory/CPU Profiling & Fix Plan

## Context

The `census-consumer` and `proxy-achievement-census` pods are at max CPU (990m/1000m) and high memory (133Mi/185Mi) while other consumers idle. Root cause analysis from code + production data reveals three compounding issues in the achievement processing hot path.

**Production data:** 7 milestones tracked, 101K active characters, 41K character milestones stored. Each character has ~3000 achievements on Lodestone.

## Root Causes (ordered by impact)

### 1. `FetchAchievements` scrapes ALL ~3000 achievements per character

`godestone.FetchCharacterAchievements(id)` scrapes every Lodestone achievement page (~60 HTTP requests, 50 achievements each). Each `AchievementInfo` allocates a `NamedEntity` struct. For 3000 achievements: ~3000 heap objects + 60 HTTP round-trips + HTML parsing. We only need 7 milestone IDs.

**Impact:** This is the dominant CPU and memory cost. Every achievement-census job pays this cost regardless of whether the character has any milestones.

### 2. `ListMilestones` queries the DB on every `ProcessAchievements` call

`ProcessAchievements` calls `s.achievements.ListMilestones(ctx)` which runs `SELECT ... FROM milestone_achievements` on every invocation. With 101K characters, that's 101K identical queries returning the same 7 rows.

**Impact:** Unnecessary DB load. Not the biggest CPU hog but compounds with #1.

### 3. `UpsertCharacterMilestones` does row-by-row INSERTs

Each milestone is inserted individually in a transaction: `INSERT INTO character_milestones ... ON CONFLICT DO UPDATE`. For characters with all 7 milestones matched, that's 7 round-trips per character.

**Impact:** Moderate — DB-bound, not CPU-bound on the consumer.

## Approach

### Step 1: Cache the milestone registry in `census.Service` ✅

Add a `milestoneCache` field to `Service` with a `sync.RWMutex` and TTL (e.g. 5 minutes). `ListMilestones` returns the cached value when fresh, queries DB when stale. `SyncMilestones` invalidates the cache.

**File:** `domain/census/service.go`
- Add fields: `milestoneCache []contract.MilestoneAchievement`, `milestoneCacheAt time.Time`, `milestoneCacheTTL time.Duration` (default 5min)
- Add method: `cachedMilestones(ctx) ([]contract.MilestoneAchievement, error)` — returns cache if fresh, queries DB and updates cache if stale
- Change `ProcessAchievements` to call `s.cachedMilestones(ctx)` instead of `s.achievements.ListMilestones(ctx)`
- Change `SyncMilestones` to invalidate cache after successful DB sync

### Step 2: Filter achievements in the handler before passing to `ProcessAchievements` ❌ Dropped

**Dropped:** `ProcessAchievements` needs the full `earned` list to compute `latest` (the most recent achievement across ALL achievements, not just milestones). Filtering in the handler would break latest-achievement tracking. The milestone cache (Step 1) already eliminates the DB query overhead.

**File:** `domain/census/service.go`
- `MilestoneIDs(ctx) map[uint32]bool` was still added as a utility for future handler-level filtering if needed.

### Step 3: Batch `UpsertCharacterMilestones` with a single multi-row INSERT ✅

Replace the row-by-row loop with a single `INSERT ... VALUES (...), (...), ... ON CONFLICT DO UPDATE` statement.

**File:** `infrastructure/postgres/repository/achievement.go`
- Rewrite `UpsertCharacterMilestones` to build a single multi-row INSERT with parameterized values
- Handle empty milestones slice (no-op, returns nil immediately)

### Step 4: Add early-return for characters with no milestones ✅

The batch INSERT already handles empty slices as a no-op (returns nil immediately), so the early-return optimization is effectively in place. `ProcessAchievements` still calls `UpdateAchievementSummary` for latest-achievement tracking regardless.

### Step 5: Profile and verify

Run the consumer locally against production DB/RabbitMQ with pprof:
```bash
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
go tool pprof http://localhost:6060/debug/pprof/heap
```

## Critical files & anchors

- `domain/census/service.go` — `ProcessAchievements`, `cachedMilestones`, `MilestoneIDs`, `SyncMilestones`
- `domain/census/handler/achievement.go` — `Handle` method, `FetchAchievements` call
- `infrastructure/postgres/repository/achievement.go` — `UpsertCharacterMilestones` (batch INSERT), `ListMilestones`
- `infrastructure/lodestone/lodestone.go` — `FetchAchievements`
- godestone fork `achievement.go` — `buildAchievementCollector` (scraper)

## Verification

1. Run existing achievement handler tests: `go test -run TestAchievement ./domain/census/handler`
2. Run existing milestone tests: `go test -run TestService_ProcessAchievements ./domain/census`
3. Run existing repository tests: `go test -run TestAchievement ./infrastructure/postgres/repository`
4. New cache tests: `TestService_CachedMilestones_*`, `TestService_SyncMilestones_InvalidatesCache`, `TestService_MilestoneIDs`
5. Deploy to staging, compare pod CPU/memory via `kubectl top pods`

## Assumptions

- The milestone registry changes rarely (only on deploy/config change). 5-minute cache TTL is conservative.
- The godestone fork's scraper cannot be easily modified to filter by ID (it parses HTML pages). Filtering in Go after fetch is the pragmatic approach.
- Batch INSERT is safe because milestone count is bounded (7) and the ON CONFLICT clause handles idempotency.

## Release

Released as `v1.7.0` on 2026-08-23. Includes concurrency bump for achievement-census workers (20→25).
