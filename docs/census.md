# Census Domain Model

This document describes the census data model and the persistence layer that stores it. The census ingests FINAL FANTASY XIV character data scraped from The Lodestone and stores it in SQLite (the same single datastore that backs the queue — see `docs/sqlite.md` and `docs/queue.md`).

## Tables

All timestamps are stored as TEXT in UTC `"2006-01-02T15:04:05.000Z"` (millisecond precision), the same convention as `queue_jobs`.

### `characters`

One row per Lodestone character. `id` is the Lodestone character ID (externally assigned, not auto-incremented).

| Column | Type | Notes |
|---|---|---|
| `id` | INTEGER PK | Lodestone character ID |
| `name` | TEXT NOT NULL | empty `''` until a full census has run |
| `world`, `datacenter`, `region` | TEXT NOT NULL | world / datacenter names; `region` derived from datacenter (NA/EU/JP/OCE) |
| `race`, `tribe`, `grand_company` | TEXT (nullable) | absent until fully censused |
| `gender` | INTEGER | 0 = none, 1 = male, 2 = female |
| `fc_id`, `fc_name` | TEXT (nullable) | NULL = not in a free company |
| `achievements_private` | INTEGER | 1 when the character hides achievements |
| `latest_achievement_id`, `latest_achievement_at` | nullable | most recent earned achievement and when |
| `first_seen_at` | TEXT NOT NULL | first discovery time |
| `last_census_at` | TEXT (nullable) | NULL until a full census has run |
| `deleted_at` | TEXT (nullable) | set when Lodestone returns 404 (character gone) |

**Discovery:** the `id-sweep` handler ingests a discovered character fully (profile + jobs) in one `UpsertCharacter` call, since godestone's `FetchCharacter` already returns the complete profile. `last_census_at` is set by the upsert; `latest_achievement_at` stays NULL until the achievement census runs. `ListStale` treats NULL `last_census_at` as stale.

### `character_jobs`

One row per (character, class/job) pair: `character_id`, `class_job_id`, `name`, `level`, `exp_level`. Primary key is `(character_id, class_job_id)`; the character repository replaces the whole set on each upsert.

### `milestone_achievements`

The registry of achievements the census tracks (data-driven — add a row to track a new milestone). `achievement_id` is the game achievement ID (the same `uint32` value godestone returns in `AchievementInfo.ID`). `kind` is one of `expansion_msq`, `job_level`, `chocobo`. `expansion` is non-null only for `expansion_msq` milestones. `detail` is a human-readable description.

### `character_milestones`

A character's earned milestones: `(character_id, achievement_id)` primary key plus `achieved_at`.

### `free_companies`

One row per free company. `id` is the Lodestone FC ID string (19 digits), not a numeric character ID.

### `census_runs`

Operational tracking of census sweeps: `started_at`, `finished_at`, `characters_seen`, `new_characters`.

## Milestone achievement IDs

Achievement IDs are the **game** achievement IDs (small sequential integers), verified against the XIVAPI Achievement sheet (`/api/sheet/Achievement/{id}`) and ffxivcollect's achievement data — NOT the hex slugs used in Lodestone `playguide/db/achievement/...` URLs. godestone's `AchievementInfo.ID` is populated from the character achievement-list HTML and is this same game ID.

The canonical registry lives in `domain/census/milestone.go` (`MilestoneSet`) and is synced to the `milestone_achievements` table via `CensusService.SyncMilestones` (idempotent `INSERT OR IGNORE`). The registry is additive — append new achievements to `MilestoneSet` and re-sync; no migration is needed.

Verified entries:

| Kind | ID | Expansion | Detail |
|---|---|---|---|
| chocobo | 590 | — | My Little Chocobo |
| expansion_msq | 1139 | Heavensward | Looking Up |
| expansion_msq | 1794 | Stormblood | The Measure of His Reach |
| expansion_msq | 2298 | Shadowbringers | Shadowbringers |
| expansion_msq | 2958 | Endwalker | That Its Chorus Might Ring for All |
| expansion_msq | 3496 | Dawntrail | In the Glow of a New Dawn |

A Realm Reborn's MSQ-completion ID and job level-cap achievements are pending verification and will be appended later.

## Repositories

Four contracts in `port/contract`, each with a SQLite implementation in `infrastructure/sqlite/repository/` and an in-memory fake in `mock/repository/`:

- **`CharacterRepository`** — `Upsert` (character + jobs atomically), `Get`, `GetJobs`, `MarkDeleted`, `UpdateAchievementSummary`, `ListStale`. The write/read surface the ingest handlers need.
- **`FreeCompanyRepository`** — `Upsert`, `Get`.
- **`AchievementRepository`** — `SyncMilestones` (idempotent registry upsert), `ListMilestones`, `UpsertCharacterMilestones`, `ListCharacterMilestones`.
- **`CensusRunRepository`** — `Start`, `Finish`.

Repositories are resolved via the service locator (`container.Load.CharacterRepository()`, etc.), which builds them from the shared `SQLiteDriver`.

## CensusService

`domain/census/service.go` is the domain brain: it converts Lodestone DTOs into persisted records and computes milestone/activity facts. Constructed via `container.Load.CensusService()` with the four repositories; the ingest handlers (next phase) call it.

- `SyncMilestones(ctx)` — seeds `MilestoneSet` into the DB (idempotent).
- `UpsertCharacter(ctx, *godestone.Character)` — converts a character + jobs into records and persists them atomically. `region` is derived from the datacenter via `RegionForDatacenter` (table below). nil race/tribe/grand-company are tolerated.
- `ProcessAchievements(ctx, charID, earned, all)` — filters earned achievements against the registry, persists only matching milestones, and updates the character's `achievements_private` flag and latest achievement (any achievement, not just milestones).
- `IsActive(latestAt)` — true when the latest achievement is within the 30-day activity window.

**DC→region mapping** (`domain/census/region.go`):

| Region | Datacenters |
|---|---|
| NA | Aether, Primal, Crystal, Dynamis |
| EU | Chaos, Light |
| JP | Elemental, Gaia, Mana, Meteor |
| OCE | Materia |

## Not yet implemented (later phases)

- **FC member-list re-census** — `fc-census` upserts FC basic info; chaining `character-census` for stale members is deferred until `FetchFreeCompanyMembers` is exposed by the LodestoneClient contract (see `docs/events.md`).
- **Aggregate/stats queries** — population breakdowns (per race/world/DC/region, new-since-date, expansion-completed counts) will be added with the REST API phase.
