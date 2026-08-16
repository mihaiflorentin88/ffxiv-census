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

**Discovery stub convention:** the `id-sweep` (a later phase) inserts a stub row with `name = ''` and `last_census_at = NULL` to mark that an ID exists. A full `character-census` later fills in the profile and sets `last_census_at`. `ListStale` treats NULL `last_census_at` as stale.

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

Achievement IDs are the **game** achievement IDs (small sequential integers), verified against the XIVAPI Achievement sheet (`/api/sheet/Achievement/{id}`) — NOT the hex slugs used in Lodestone `playguide/db/achievement/...` URLs. godestone's `AchievementInfo.ID` is populated from the character achievement-list HTML and is this same game ID.

Verified example: `590` = "My Little Chocobo".

## Repositories

Four contracts in `port/contract`, each with a SQLite implementation in `infrastructure/sqlite/repository/` and an in-memory fake in `mock/repository/`:

- **`CharacterRepository`** — `Upsert` (character + jobs atomically), `Get`, `GetJobs`, `MarkDeleted`, `UpdateAchievementSummary`, `ListStale`. The write/read surface the ingest handlers need.
- **`FreeCompanyRepository`** — `Upsert`, `Get`.
- **`AchievementRepository`** — `SyncMilestones` (idempotent registry upsert), `ListMilestones`, `UpsertCharacterMilestones`, `ListCharacterMilestones`.
- **`CensusRunRepository`** — `Start`, `Finish`.

Repositories are resolved via the service locator (`container.Load.CharacterRepository()`, etc.), which builds them from the shared `SQLiteDriver`.

## Not yet implemented (later phases)

- **DC→region derivation** — mapping a datacenter name to its region (NA/EU/JP/OCE) lives in the domain service phase, which converts godestone DTOs into `CharacterRecord`s (including filling `region`).
- **Milestone registry data** — the canonical list of tracked achievement IDs (expansion completions, job level caps, chocobo) is defined in the domain phase and synced via `AchievementRepository.SyncMilestones`.
- **Ingest handlers** — `id-sweep`, `character-census`, `achievement-census`, `fc-census` consume queue jobs and drive these repositories.
- **Aggregate/stats queries** — population breakdowns (per race/world/DC/region, new-since-date, expansion-completed counts) will be added with the REST API phase.
