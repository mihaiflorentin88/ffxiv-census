# UI Statistics Audit — Implementation Plan

## Context

Full audit of every UI page and REST API stat calculation. Four confirmed bugs, several documentation inaccuracies.

## Step 1: Add `Since *time.Time` to CharacterFilter

**File:** `port/contract/character_repository.go`
- Add `Since *time.Time` field to `CharacterFilter` struct

**File:** `infrastructure/postgres/repository/character.go` — `characterFilterWhereWithStart`
- When `f.Since != nil`, add `latest_achievement_at >= $N`

## Step 2: Add `MinLevel` handling to SQL filter

**File:** `infrastructure/postgres/repository/character.go` — `characterFilterWhereWithStart`
- When `f.MinLevel > 0`, add `id IN (SELECT character_id FROM character_jobs WHERE level >= $N)`

## Step 3: Fix `WorldDetail` to use `Since`

**File:** `domain/census/service.go`
- Export `ActivitySince()` method
- `WorldDetail` uses `Since: &since` instead of `ActiveOnly: true`

## Step 4: Fix `CharacterList` handler

**File:** `cmd/http/ui/character.go`
- When `activeOnly` is true, set `Since` on the filter via `c.svc.ActivitySince()`

## Step 5: Fix `CountChocoboMilestones` SQL

**File:** `infrastructure/postgres/repository/achievement.go`
- Change `LEFT JOIN` to `INNER JOIN`, remove `first_seen_at` fallback
- Only count characters with `achievement_id = 590` and `achieved_at >= since`

## Step 6: Fix mock implementations

**File:** `mock/repository/achievement.go`
- `NewCharactersPerDay`: iterate actual milestone data for achievement 590
- `CountChocoboMilestones`: count characters with milestone 590 in window
- Both use `matchesFilter` for character filtering

## Step 7: Fix mock `matchesFilter` for `Since`

**File:** `mock/repository/character.go`
- Add `Since` handling: `if f.Since != nil && (rec.LatestAchievementAt == nil || rec.LatestAchievementAt.Before(*f.Since))`

## Step 8: Update tests

- `infrastructure/postgres/repository/character_test.go`: add `Since` and `MinLevel` filter tests
- `cmd/http/app/census/handler/census_test.go`: update `TestCensusController_NewCharacters` to seed chocobo milestone
- Run `make test` and `make lint`

## Step 9: Update documentation

- `docs/census.md`: CharacterFilter docs, NewCharacters description, ActiveOnly clarification
- `docs/ui.md`: dashboard description, world detail route, active definition
- `docs/http-api.md`: NewCharacters endpoint description
- Swagger docs: NewCharacters description

## Step 10: Deploy

- Commit and push
- Build Docker image, tag v1.0.13, push
- `make k8s-release TAG=v1.0.13`

## Verification

1. `make test` — all tests pass
2. `make lint` — clean
3. Manual: `/ui/worlds/Aegis` active count should be ~24 (not 900)
4. Manual: `/ui/dashboard` max level count should be much less than total
5. Manual: `/ui/dashboard` new characters chart based on chocobo milestone
6. Manual: `/ui/characters?active=true` shows only recently active
