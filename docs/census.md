# Census Domain Model

This document describes the census data model and the persistence layer that stores it. The census ingests FINAL FANTASY XIV character data scraped from The Lodestone and stores it in PostgreSQL (the same single datastore that backs the queue — see `docs/external-postgres.md` and `docs/queue.md`).

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
### `character_gear`

One row per equipped gear slot: `(character_id, slot)` primary key, `item_id`, `name`, `item_level`, `dye`, `materia` (JSON array of materia names/tiers), `updated_at`. Stores equipped gear pieces scraped from character profiles.

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

The milestone registry is driven dynamically by `config.toml` (`[census.expansions]`) alongside the foundational Chocobo milestone and is synced to the `milestone_achievements` table via `CensusService.SyncMilestones` (idempotent `INSERT OR IGNORE`).

Default entries:

| Kind | ID | Expansion | Detail |
|---|---|---|---|
| chocobo | 590 | — | My Little Chocobo |
| expansion_msq | 1129 | A Realm Reborn | My Left Arm |
| expansion_msq | 1139 | Heavensward | Looking Up |
| expansion_msq | 1794 | Stormblood | The Measure of His Reach |
| expansion_msq | 2298 | Shadowbringers | Shadowbringers |
| expansion_msq | 2958 | Endwalker | That Its Chorus Might Ring for All |
| expansion_msq | 3496 | Dawntrail | In the Glow of a New Dawn |
## Repositories

Four contracts in `port/contract`, each with a PostgreSQL implementation in `infrastructure/postgres/repository/` and an in-memory fake in `mock/repository/`:

- **`CharacterRepository`** — `Upsert` (character + jobs atomically), `Get`, `GetJobs`, `UpsertGear`, `GetGear`, `FindIDGaps`, `MarkDeleted`, `UpdateAchievementSummary`, `SetAchievementsPrivate`, `ListStale`, `List`, `Count`, `CountActive`, `Breakdown`, `NewPerDay`, `MaxID`. The complete persistence and query contract for character data.
- **`FreeCompanyRepository`** — `Upsert`, `Get`.
- **`AchievementRepository`** — `SyncMilestones` (idempotent registry upsert), `ListMilestones`, `UpsertCharacterMilestones`, `ListCharacterMilestones`, `CountExpansions`, `CountExpansionsFiltered`, `NewCharactersPerDay`, `CountChocoboMilestones`.
- **`CensusRunRepository`** — `Start`, `Finish`.

Repositories are resolved via the service locator (`container.Load.CharacterRepository()`, etc.), which builds them from the shared `DatabaseDriver`.

### CharacterFilter

The `CharacterFilter` struct (`port/contract/character_repository.go`) controls `List`, `Count`, `Stream`, and `Breakdown` queries:

| Field | Type | Effect |
|---|---|---|
| `World`, `Datacenter`, `Region`, `Race`, `GrandCompany`, `FreeCompanyID` | `string` | Exact match (ignored when empty) |
| `Name` | `string` | Case-insensitive substring (`ILIKE`) |
| `ActiveOnly` | `bool` | Adds `deleted_at IS NULL`. Without `Since`, this is redundant with the base query which already excludes deleted characters. |
| `Since` | `*time.Time` | When non-nil, only characters with `latest_achievement_at >= Since` are returned (activity window filter). This is the proper way to filter by "recently active". |
| `MinLevel` | `uint32` | When > 0, only characters with at least one job at or above this level are returned (subquery on `character_jobs`). |
| `SortBy` | `string` | Column to sort by: `"id"`, `"name"`, `"world"`, `"created_at"`, `"updated_at"`, `"achievement_points"` |
| `SortOrder` | `string` | `"asc"` (default) or `"desc"` |

**Active filtering**: To filter by the activity window (e.g. "active in last 30 days"), set `Since` to the window start time. `ActiveOnly` alone does not include the activity window — it only checks `deleted_at IS NULL`.

## CensusService

`domain/census/service.go` is the domain brain: it converts Lodestone DTOs into persisted records and computes milestone/activity facts. Constructed via `container.Load.CensusService()` with the four repositories; the ingest handlers call it.

- `SyncMilestones(ctx)` — seeds configured expansion milestones and chocobo achievement into the DB (idempotent).
- `UpsertCharacter(ctx, *godestone.Character)` — converts a Lodestone character + jobs into records and persists them atomically. `region` is derived from the datacenter via `RegionForDatacenter` (table below). nil race/tribe/grand-company are tolerated.
- `UpsertTomestoneCharacter(ctx, *contract.TomestoneCharacter)` — converts a Tomestone character + jobs into records and persists them atomically.
- `ProcessAchievements(ctx, charID, earned, all)` — filters earned achievements against the registry, persists only matching milestones, and updates the character's `achievements_private` flag and latest achievement (any achievement, not just milestones).
- `MaxCharacterID(ctx)` — returns the highest known character ID in the repository (excluding deleted characters), used for auto-discovery sweeps.
- `IsActive(latestAt)` — true when the latest achievement is within the activity window (default 30 days, configurable via `SetActivityWindow` / `[census] activity_window_days`).
- `SetActivityWindow(d)` — overrides the activity window; a no-op for `d <= 0`.
- `Summary(ctx)` — total, active, and max-level character counts (`total, active, maxLevelCount, err`), where active means the latest achievement is within the activity window and max-level means having at least one job at or above `max_level`.
- `ListCharacters(ctx, filter, limit, offset)` — one page of characters matching `filter` plus the matching count (the HTTP pagination/filtering source).
- `CharacterDetail(ctx, id)` — character plus jobs and milestones, with the free company when the character is in one; `nil` when the id is unknown.
- `Breakdown(ctx, by)` — per-`race`/`world`/`datacenter`/`region` totals and active counts; any other dimension returns `ErrInvalidDimension`.
- `NewCharacters(ctx, since, until)` — characters who earned the Chocobo milestone (achievement 590) per UTC day in `[since, until)`. The Chocobo milestone is the canonical definition for "new character" as it indicates the character has started playing.
- `ExpansionCompletions(ctx)` — distinct characters per expansion that completed that expansion's MSQ.

**DC→region mapping** (`domain/census/region.go`):

| Region | Datacenters |
|---|---|
| NA | Aether, Primal, Crystal, Dynamis |
| EU | Chaos, Light |
| JP | Elemental, Gaia, Mana, Meteor |
| OCE | Materia |

## Not yet implemented (later phases)

- **FC member-list re-census** — `fc-census` upserts FC basic info; chaining `character-census` for stale members is deferred until `FetchFreeCompanyMembers` is exposed by the LodestoneClient contract (see `docs/events.md`).
