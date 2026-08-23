# Census Domain Model

This document describes the census data model and the persistence layer that stores it. The census ingests FINAL FANTASY XIV character data scraped from The Lodestone and stores it in PostgreSQL.

## Tables

All timestamps are stored as TEXT in UTC `"2006-01-02T15:04:05.000Z"` (millisecond precision).

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

**Discovery:** the `id-sweep` handler ingests a discovered character fully (profile + jobs) in one `UpsertCharacter` call, since godestone's `FetchCharacter` already returns the complete profile. `last_census_at` is set by the upsert; `latest_achievement_at` stays NULL until the achievement census runs.

**`ListStale` behaviour:** returns up to `limit` characters ordered by `last_census_at ASC NULLS FIRST, id ASC`. A zero `cutoff` disables the age predicate — all non-deleted characters are eligible, ordered oldest `last_census_at` first (NULL first). A positive `cutoff` filters to rows whose `last_census_at` is before the cutoff (NULL `last_census_at` counts as stale in both modes).

### `character_jobs`

One row per (character, class/job) pair: `character_id`, `class_job_id`, `name`, `level`, `exp_level`. Primary key is `(character_id, class_job_id)`; the character repository replaces the whole set on each upsert. The `class_job_id` values come from either the Tomestone REST API (direct) or the Lodestone name→ID lookup table (indirect — see `lodestoneJobIDs` in `infrastructure/lodestone/client.go`).
### `character_gear`

One row per equipped gear slot: `(character_id, slot)` primary key, `item_id`, `name`, `item_level`, `dye`, `materia` (JSON array of materia names/tiers), `updated_at`. Stores equipped gear pieces scraped from character profiles.

### `milestone_achievements`

The registry of achievements the census tracks (data-driven — add a row to track a new milestone). `achievement_id` is the game achievement ID (the same `uint32` value godestone returns in `AchievementInfo.ID`). `kind` is one of `expansion_msq`, `job_level`, `chocobo`. `expansion` is non-null only for `expansion_msq` milestones. `detail` is a human-readable description.

### `character_milestones`

A character's earned milestones: `(character_id, achievement_id)` primary key plus `achieved_at`.

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

Three contracts in `port/contract`, each with a PostgreSQL implementation in `infrastructure/postgres/repository/` and an in-memory fake in `mock/repository/`:

- **`CharacterRepository`** — `Upsert` (character + jobs atomically), `Get`, `GetJobs`, `UpsertGear`, `GetGear`, `FindIDGaps`, `MarkDeleted`, `UpdateAchievementSummary`, `SetAchievementsPrivate`, `ListStale`, `List`, `Count`, `CountActive`, `Breakdown`, `NewPerDay`, `MaxID`. The complete persistence and query contract for character data.
- **`AchievementRepository`** — `SyncMilestones` (idempotent registry upsert), `ListMilestones`, `UpsertCharacterMilestones` (batch multi-row INSERT, single round-trip), `ListCharacterMilestones`, `CountExpansions`, `CountExpansionsFiltered`, `NewCharactersPerDay`, `CountChocoboMilestones`.
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

`domain/census/service.go` is the domain brain: it converts Lodestone DTOs into persisted records and computes milestone/activity facts. Constructed via `container.Load.CensusService()` with the three repositories; the ingest handlers call it. The service caches the milestone registry in memory (5-minute TTL) to avoid re-querying the DB on every `ProcessAchievements` call; `SyncMilestones` invalidates the cache.

- `SyncMilestones(ctx)` — seeds configured expansion milestones and chocobo achievement into the DB (idempotent). Invalidates the in-memory milestone cache so the next `ProcessAchievements` picks up the fresh registry.
- `UpsertCharacter(ctx, *godestone.Character)` — converts a Lodestone character + jobs into records and persists them atomically. `region` is derived from the datacenter via `RegionForDatacenter` (table below). nil race/tribe/grand-company are tolerated.
- `UpsertTomestoneCharacter(ctx, *contract.TomestoneCharacter)` — converts a Tomestone character + jobs into records and persists them atomically.
- `ProcessAchievements(ctx, charID, earned, all)` — filters earned achievements against the registry, persists only matching milestones, and updates the character's `achievements_private` flag and latest achievement (any achievement, not just milestones). Uses the in-memory milestone cache (5-minute TTL) to avoid re-querying the DB on every call.
- `MaxCharacterID(ctx)` — returns the highest known character ID in the repository (excluding deleted characters), used for auto-discovery sweeps.
- `MilestoneIDs(ctx)` — returns the set of tracked milestone achievement IDs from the cached registry. Useful for handler-level pre-filtering.
- `IsActive(latestAt)` — true when the latest achievement is within the activity window (default 30 days, configurable via `SetActivityWindow` / `[census] activity_window_days`).
- `SetActivityWindow(d)` — overrides the activity window; a no-op for `d <= 0`.
- `Summary(ctx)` — total, active, and max-level character counts (`total, active, maxLevelCount, err`), where active means the latest achievement is within the activity window and max-level means having at least one job at or above `max_level`. Fans out three database queries concurrently (`Count`, `CountActive`, `Count` with `MinLevel`) and joins results with deterministic error precedence (total → active → max-level).
- `ListCharacters(ctx, filter, limit, offset)` — one page of characters matching `filter` plus the matching count (the HTTP pagination/filtering source).
- `CharacterDetail(ctx, id)` — character plus jobs and milestones, with the free company when the character is in one; `nil` when the id is unknown.
- `WorldDetail(ctx, worldName)` — full census stats for a specific world, returned as `WorldDetailStats` (total population, active players, new characters in last 30 days, race breakdown, MSQ completions, 30-day new-character timeline, and a sample character). Fans out seven database queries concurrently and joins results with deterministic error precedence.
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

