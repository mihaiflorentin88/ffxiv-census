# Lodestone Census — Design Spec

Date: 2026-08-16
Status: Approved (design review complete)

## 1. Purpose

Build a population census for FINAL FANTASY XIV characters using The Lodestone (https://na.finalfantasyxiv.com/lodestone/) as the data source. The service:

- Discovers and ingests all characters (profile, race, jobs/levels, free company, milestone achievements).
- Runs ingest via a SQLite-backed queue with dedicated consumers per event type.
- Exposes census data through a versioned REST API (documented with Swagger) for other clients.
- Renders public census dashboards as server-side HTML + HTMX pages.
- Stores everything (queue + data) in a single SQLite database.

## 2. Key Decisions (approved during brainstorming)

| Decision | Choice |
|---|---|
| Lodestone access | No official API exists; use `xivapi/godestone` Go scraper behind a port interface |
| Character discovery | Full ID-space sweep once, then incremental (new IDs + re-checks) |
| Active player definition | Any achievement earned in the last 30 days (configurable) |
| Queue | SQLite tables (jobs with status/run_at/attempts), `UPDATE ... RETURNING` claim pattern |
| Consumers | One CLI command + domain handler per event type; consumers may publish events for other consumers |
| Migrations | `pressly/goose/v3` with embedded SQL, executed automatically at runtime on boot |
| SQLite driver | `modernc.org/sqlite` (pure Go, keeps `CGO_ENABLED=0` cross-compile targets working) |
| Web UI | Server-rendered Go templates + HTMX + vendored Chart.js; no Node toolchain |
| Old MySQL code | Removed and replaced (clean break — nothing depends on it yet) |

## 3. Architecture

Follows the existing hexagonal (ports & adapters) layout. Only `container/` imports concrete infrastructure; all other layers depend on `port/contract` interfaces.

### 3.1 New ports (`port/contract/`)

- `LodestoneClient` — `FetchCharacter(ctx, id)`, `FetchAchievements(ctx, id)`, `FetchFreeCompany(ctx, id)`. Implemented by `infrastructure/lodestone` (wraps godestone + rate limiter + retry).
- `SQLiteDriver` — replaces `MySQLDriver`; same shape (Acquire/Close/Execute/FetchOne/FetchMany) adapted for SQLite (`?` placeholders).
- `Queue` — `Publish(ctx, jobs...)`, `Claim(ctx, type, n)`, `Complete(ctx, id, nextJobs)`, `Retry(ctx, id)`, `Fail(ctx, id)`, `Depth(ctx)` per status.
- `CharacterRepository`, `FreeCompanyRepository`, `AchievementRepository`, `CensusRunRepository` — persistence contracts.
- Removed: `MySQLDriver`, `MigrationRunner` (MySQL), `FixtureGenerator/Loader`, `ExampleRepository` and their contracts.

### 3.2 Domain (`domain/census/`)

- `handler/` — one handler per event type. Each implements `Handle(ctx, payload) ([]QueueJob, error)`: handlers *return* the events they want published; the worker persists job completion + downstream publishes in one transaction.
  - `idsweep.Handler` — probes a chunk of the ID space; inserts discovered characters as `unverified`; returns `character-census` jobs.
  - `character.Handler` — fetches profile + ClassJobs; upserts; resolves FC ref; returns `achievement-census` (same char) and `fc-census` (if FC unseen/stale).
  - `achievement.Handler` — fetches achievements; filters through milestone registry; upserts milestones; records latest achievement id + date.
  - `fc.Handler` — fetches FC + member list; upserts; returns `character-census` for stale members.
- `CensusService` — census logic: character upserts from Lodestone DTOs, milestone filtering, activity classification, aggregate queries (per race/world/DC/region, new-since-date, expansion-completed).
- Milestone registry: achievement IDs for expansion MSQ completions (ARR→Dawntrail), job level-cap milestones, "My Little Chocobo". Seeded via migration; loading code reads from DB so new milestones can be added without code changes.

### 3.3 Container

Lazy accessors, same pattern as existing code: `SQLiteDriver()`, `Queue()`, `LodestoneClient()`, repositories, handlers map, `CensusService()`. Graceful degradation: missing `[sqlite]`/`[lodestone]` config sections → nil adapter + warn log (callers nil-check).

## 4. Event System

### 4.1 Events

| Event | Published by | Handler does | Publishes next |
|---|---|---|---|
| `id-sweep` | `publish` CLI (bootstrap/cron) | probe ID chunk; insert discovered chars as `unverified` | `character-census` per discovered ID (chunked) |
| `character-census` | `id-sweep`, `publish --recheck`, `fc-census` | fetch profile + jobs; upsert; resolve FC ref | `achievement-census`; `fc-census` if FC unseen/stale |
| `achievement-census` | `character-census` | fetch achievements; milestone filter; latest achievement | — |
| `fc-census` | `character-census`, `publish` | fetch FC + members; upsert | `character-census` for stale members (>30d since last census) |

### 4.2 Loop safety

- `UNIQUE(type, payload_hash)` on the queue — duplicate pending/claimed jobs are a no-op.
- Staleness windows gate re-enqueueing (`fc-census` only if FC `last_seen_at` > 7d; member re-census only if `last_census_at` > 30d). Chains converge.
- All handlers idempotent — re-running a job re-upserts.

### 4.3 Job lifecycle

`pending` → `claimed` → `done` | back to `pending` (retry, `attempts++`, exponential `run_at` backoff) | `failed` (after `max_attempts`, default 5). Claiming uses `BEGIN IMMEDIATE` + `UPDATE ... WHERE type = ? AND status = 'pending' AND run_at <= now RETURNING *`, so multiple pods of the same consumer are safe.

## 5. CLI (cobra, `cmd/cli/`)

```
ffxiv-census consume <event>    [--concurrency N]   # one consumer per event; also "all"
ffxiv-census publish id-sweep --max-id N --chunk-size 500
ffxiv-census publish character-census --recheck --older-than 720h
ffxiv-census publish fc-census --stale-than 168h
ffxiv-census server --start                          # existing; now also serves API + UI
ffxiv-census migrate --direction up|down             # reworked to goose (manual ops)
```

- `consume` = long-running worker: worker pool (default concurrency 4) + token-bucket rate limiter (default ~1 req/s/worker — Lodestone is unofficial; politeness avoids IP bans). Graceful SIGTERM: finish in-flight jobs, release claims.
- `publish` = one-shot cronjob entrypoint (k8s CronJob later; user owns k8s).

## 6. Data Model (SQLite, goose migrations)

```
characters(id INTEGER PK, name, world, datacenter, region, race, tribe, gender,
           grand_company, fc_id NULL, fc_name NULL, achievements_private BOOL,
           latest_achievement_id NULL, latest_achievement_date NULL,
           first_seen_at, last_census_at, deleted_at NULL)
character_jobs(character_id, class_job_id, name, level, exp,
               PK(character_id, class_job_id))
milestone_achievements(achievement_id PK, kind, expansion NULL, detail)
character_milestones(character_id, achievement_id, achieved_at,
                     PK(character_id, achievement_id))
free_companies(id TEXT PK, name, world, datacenter, member_count, formed_at, last_seen_at)
census_runs(id PK, started_at, finished_at, characters_seen, new_characters)
queue_jobs(id PK, type, payload JSON, payload_hash, status, run_at, attempts,
           max_attempts, claimed_at, created_at)
```

- `region` derived from datacenter via static DC→region mapping (NA/EU/JP/OCE).
- "Not in an FC" = `fc_id IS NULL`.
- Only milestone achievements + the single latest achievement per character are persisted (keeps DB small).
- Character IDs are global across regions — one sweep covers NA/EU/JP/OCE.

## 7. Migrations (goose at runtime)

- `pressly/goose/v3` with `go:embed`-ed SQL in `infrastructure/sqlite/migration/query/` (goose naming `YYYYMMDDHHMMSS_name.sql`).
- Every binary self-migrates at boot: the container's `SQLiteDriver` accessor runs `goose.UpContext` on first acquire (server, consumers, publishers).
- Seed migration inserts the milestone registry.
- `migrate` CLI command reworked to goose up/down for manual operations.

## 8. REST API + Swagger

Versioned under `/api/v1`; existing handler + DTO patterns per `docs/data-contracts.md`:

- `GET /api/v1/census/latest` — latest run summary (totals, active count, active ratio)
- `GET /api/v1/census/characters?limit&offset` — browse characters
- `GET /api/v1/census/characters/{id}` — character detail (jobs, milestones, FC)
- `GET /api/v1/stats/breakdown?by=race|world|datacenter|region` — group + active counts
- `GET /api/v1/stats/new-characters?since=YYYY-MM-DD[&until]` — new characters per day
- `GET /api/v1/stats/expansion?name=...` — finished-expansion counts
- `GET /api/v1/queue` — queue depth per status (ops visibility)

Pagination envelope: `{items, total, limit, offset}`. Swagger spec extended (existing embedded `/docs/` setup) documenting every endpoint + DTO schemas.

## 9. Web UI (server-rendered + HTMX)

Extends `cmd/http/ui/` (embedded templates, no Node toolchain):

- `/ui/dashboard` — headline stats (total, active, new this month), new-characters/day trend chart, region → DC → world drill-down tables (HTMX partial swaps)
- `/ui/races`, `/ui/worlds` — population bars with active ratio
- `/ui/expansions` — expansion completion funnel
- `/ui/characters/{id}` — character profile (jobs grid, milestones, FC, latest achievement)
- Chart.js vendored locally (no CDN). Dark FFXIV-flavored theme. Built per `frontend-design` + `web-design-guidelines` skills.

## 10. Testing (TDD — strict)

- Every port gets a fake in `mock/` (Lodestone fake with canned godestone DTOs; in-memory queue; in-memory repos) — two adapters per port.
- SQLite repositories tested against temp-file real DBs (real SQL, no mocks).
- Table-driven tests for milestone filtering, activity classification, DC→region mapping.
- HTTP handlers via `httptest`; worker pool under `go test -race`.
- Red-green-refactor: no production code without a failing test first.

## 11. Documentation (required deliverable)

All technical behavior documented in `docs/*.md`, explaining how everything works and its role:

- `docs/architecture.md` — updated: event system, consumers, queue
- `docs/census.md` (new) — census concepts: discovery, activity definition, milestones, census runs
- `docs/events.md` (new) — event taxonomy, payloads, chaining rules, loop safety
- `docs/queue.md` (new) — SQLite queue design, job lifecycle, claiming, retries/backoff, multi-pod safety
- `docs/sqlite.md` (new) — schema, migrations, runtime migration behavior (replaces `docs/mysql.md`, removed)
- `docs/lodestone.md` (new) — scraper integration, rate limiting, failure handling
- `docs/http-api.md` (new) — REST endpoints, DTOs, pagination envelope
- `docs/ui.md` — updated for the new dashboard pages
- `docs/logging-and-middleware.md`, `docs/container.md` — updated where behavior changes

## 12. Configuration (config.toml additions)

```
[sqlite]   # path, busy_timeout, pool settings
[lodestone] # rate limit (req/s), timeout, max retries, user agent
[queue]     # claim batch size, max attempts, backoff base
[census]    # activity window days, recheck interval, fc staleness, member staleness
```

Embedded config.toml + env overrides (existing Viper mechanism, e.g. `SQLITE_PATH`, `LODESTONE_RATE_LIMIT`).

## 13. Out of Scope

- Kubernetes manifests (user handles later)
- Mounts/minions collection, gear tracking, linkshells/PvP teams (godestone supports them; future work)
- Auth on public API (existing `[auth]` config remains unused)
- Redis (interface kept, no adapter)
