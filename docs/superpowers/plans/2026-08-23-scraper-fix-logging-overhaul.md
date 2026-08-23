# Scraper HTML Fix + Column Removal + Logging Overhaul

## Context

The Lodestone HTML scraper (`infrastructure/lodestone/client.go`) stores raw HTML in database columns because `extractTextBetween` returns content between markers without stripping inner HTML tags. Lodestone recently changed their HTML structure to wrap fields like character names, worlds, and FC names in `<a>` and `<i>` tags. This contaminated 1,425 character rows (all entries from 2026-08-23 ~09:48 UTC onward). Yesterday's data is clean.

Additionally, the user wants `avatar_url` and `portrait_url` columns removed from the database, and logging across the entire app improved to use descriptive, human-readable messages with correct log levels and rich identifying context (IDs, names, durations) on every log line.

## Approach

### Step 1: Fix HTML tag stripping in Lodestone scraper

**File: `infrastructure/lodestone/client.go`**

Add a `stripTags` helper function that:
1. Removes all HTML tags via regex `<[^>]*>` → empty string
2. Decodes common HTML entities: `&#39;` → `'`, `&amp;` → `&`, `&lt;` → `<`, `&gt;` → `>`, `&quot;` → `"`, `&#34;` → `"`, `&nbsp;` → space
3. Collapses multiple whitespace into single spaces and trims

Apply `stripTags` to the return values of:
- `extractTextBetween` (line ~496) — affects Name, World, Bio, GrandCompany, FreeCompanyName, and parseClassJobs job names
- `extractAllTextBetween` (line ~511) — affects Race/Tribe extraction

This is the minimal fix: one helper applied at the two extraction points. All callers benefit automatically. No changes needed to `extractAttr`/`extractAlt`/`extractHref` (they read attribute values, not inner text).

Add unit tests for `stripTags` covering: plain text passthrough, single tag removal, nested tags, HTML entities, mixed content, whitespace normalization.

Add unit tests for `extractTextBetween` and `extractAllTextBetween` with HTML-containing input to verify tags are stripped.

### Step 2: Remove `avatar_url` and `portrait_url` columns

**New migration: `infrastructure/postgres/migration/query/00013_drop_avatar_portrait.sql`**
```sql
-- +goose Up
ALTER TABLE characters DROP COLUMN IF EXISTS avatar_url;
ALTER TABLE characters DROP COLUMN IF EXISTS portrait_url;

-- +goose Down
ALTER TABLE characters ADD COLUMN avatar_url TEXT;
ALTER TABLE characters ADD COLUMN portrait_url TEXT;
```

**File: `port/contract/census.go`** — Remove `AvatarURL` and `PortraitURL` fields from `CharacterRecord` struct.

**File: `port/contract/lodestone.go`** — Remove `AvatarURL` and `PortraitURL` fields from `CharacterProfile` struct.

**File: `port/contract/tomestone.go`** — Remove `AvatarURL` and `PortraitURL` fields from `TomestoneCharacter` struct.

**File: `infrastructure/postgres/repository/character.go`**:
- Remove `avatar_url, portrait_url` from `characterColumns` constant (line 23)
- Remove from INSERT/UPSERT query (lines 30-31, 43-44)
- Remove from scanCharacter scan vars and assignments (lines 540, 553-554)

**File: `domain/census/service.go`**:
- `profileToRecord` (line ~285): Remove `AvatarURL` and `PortraitURL` assignments
- `toTomestoneCharacterRecord` (line ~253): Remove `AvatarURL` and `PortraitURL` assignments

**File: `infrastructure/lodestone/client.go`**:
- `parseCharacterProfile` (line ~448-451): Remove AvatarURL and PortraitURL extraction lines

**File: `infrastructure/tomestone/client.go`**:
- `toContractCharacter` (line ~450-454): Remove `avatar` and `portrait` variable assignments and usage

No UI template changes needed — confirmed `cmd/http/app/ui/` has zero avatar/portrait references.

No mock changes needed — confirmed `mock/repository/` has zero avatar/portrait references.

### Step 3: Logging overhaul — descriptive messages with identifying context

Transform terse dot-notation event names into descriptive sentences. Every log MUST include structured attributes that identify exactly what the log refers to: character IDs, names, worlds, achievement IDs/names, durations, event types, proxy addresses, queue names. Use correct log levels per the existing policy (Info=lifecycle/completions, Warn=transient retries, Error=terminal, Debug=per-item detail).

**Rule**: Every log call MUST carry at least one identifying attribute (ID, name, or address). Error/warn logs MUST carry the `error` attribute. Duration-bearing operations MUST carry `duration`.

#### `domain/census/handler/character.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `handler.character_census` | Debug | `Processing character census` | `character_id` |
| `handler.character_census.fetched` (lodestone) | Debug | `Fetched character from Lodestone` | `character_id`, `name`, `world`, `datacenter`, `duration` |
| `handler.character_census.stored` (lodestone) | Debug | `Stored character in database` | `character_id`, `name`, `world` |
| `handler.character_census.done` (lodestone) | Debug | `Character census complete` | `character_id`, `name`, `world`, `chained_jobs` |
| `handler.character_census.fetched` (tomestone) | Debug | `Fetched character from Tomestone` | `character_id`, `name`, `world`, `datacenter`, `duration` |
| `handler.character_census.stored` (tomestone) | Debug | `Stored character in database` | `character_id`, `name`, `world` |
| `handler.character_census.done` (tomestone) | Debug | `Character census complete` | `character_id`, `name`, `world`, `chained_jobs` |
| `handler.character_census.deleted` | Debug | `Character marked as deleted` | `character_id` |
| `handler.character_census.fetch_error` | Warn | `Failed to fetch character` | `character_id`, `source`, `error` |
| `handler.character_census.store_error` | Error | `Failed to store character` | `character_id`, `name`, `world`, `source`, `error` |
| `handler.character_census.tomestone_miss_retrying_lodestone` | Warn | `Character not found on Tomestone, retrying with Lodestone` | `character_id` |

#### `domain/census/handler/achievement.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `handler.achievement_census` | Debug | `Processing achievement census` | `character_id` |
| `handler.achievement_census.waiting_for_lodestone` | Info | `Waiting for Lodestone rate limiter` | `character_id` |
| `handler.achievement_census.milestone_query_failed` | Warn | `Failed to query known milestones` | `character_id`, `error` |
| `handler.achievement_census.skipped` | Debug | `Skipping achievement census, all milestones already known` | `character_id`, `known_milestones` |
| `handler.achievement_census.fetch_error` | Warn | `Failed to fetch achievements from Lodestone` | `character_id`, `error`, `duration` |
| `handler.achievement_census.private` | Debug | `Achievements are private` | `character_id` |
| `handler.achievement_census.process_error` | Error | `Failed to process milestone results` | `character_id`, `error` |
| `handler.achievement_census.complete` | Debug | `Achievement census complete` | `character_id`, `milestones`, `requests`, `private`, `duration` |

#### `domain/census/handler/idsweep.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `handler.id_sweep.start` | Debug | `Scanning ID range` | `from`, `to`, `count` |
| `handler.id_sweep.discovered` | Debug | `Discovered new character` | `character_id`, `name`, `world`, `source` |
| `handler.id_sweep.probe` | Debug | `Character not found` | `character_id`, `source` |
| `handler.id_sweep.done` | Debug | `ID range scan complete` | `from`, `to`, `discovered` |
| `handler.id_sweep.fetch_error` | Warn | `Failed to fetch character` | `character_id`, `source`, `error` |
| `handler.id_sweep.store_error` | Error | `Failed to store character` | `character_id`, `name`, `world`, `source`, `error` |
| `handler.id_sweep.tomestone_miss_retrying_lodestone` | Warn | `Character not found on Tomestone, retrying with Lodestone` | `character_id` |

#### `domain/census/worker/worker.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `worker.start` | Info | `Worker started` | `event_types`, `concurrency` |
| `worker.missing_handler` | Error | `No handler registered for event type` | `event_type` |
| `worker.job_start` | Debug | `Processing job` | `event_type` |
| `worker.job_done` | Info | `Job completed successfully` | `event_type`, `duration` |
| `worker.job_retry` | Warn | `Job failed, retrying` | `event_type`, `error`, `attempt` |
| `worker.publish_error` | Error | `Failed to publish follow-up job` | `event_type`, `error` |
| `worker.waiting_for_provider` | Info | `Waiting for provider to become available` | `provider` |
| `worker.proxy_acquired` | Info | `Acquired proxy` | `proxy_address` |
| `worker.proxy_start` | Info | `Proxy worker started` | `event_types`, `concurrency`, `owner` |
| `worker.proxy_stop` | Info | `Proxy worker stopped` | `owner` |
| `worker.proxy_waiting` | Info | `No proxy available, waiting` | `worker_id` |
| `worker.proxy_waiting_backoff` | Info | `Proxy acquisition backing off` | `worker_id`, `backoff` |
| `worker.proxy_loop_error` | Error | `Proxy worker loop error` | `worker_id`, `error` |
| `worker.proxy_mark_failed_error` | Warn | `Failed to mark proxy as failed` | `proxy_address`, `error` |
| `worker.proxy_release_error` | Warn | `Failed to release proxy` | `proxy_address`, `error` |
| `worker.proxy_cleanup_release_error` | Warn | `Failed to release proxy during cleanup` | `proxy_address`, `error` |

#### `infrastructure/lodestone/client.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `lodestone.fetch_character.attempt` | Debug | `Fetching character from Lodestone` | `character_id`, `proxy` |
| `lodestone.fetch_character.success` | Debug | `Fetched character from Lodestone` | `character_id`, `name`, `world`, `duration` |
| `lodestone.fetch_character.not_found` | Debug | `Character not found on Lodestone` | `character_id`, `status` |
| `lodestone.fetch_character.error` | Warn | `Failed to fetch character from Lodestone` | `character_id`, `error` |
| `lodestone.fetch_achievements.start` | Debug | `Fetching achievements from Lodestone` | `character_id`, `milestones_to_check` |
| `lodestone.fetch_achievements.private` | Debug | `Achievements are private` | `character_id` |
| `lodestone.fetch_achievements.complete` | Debug | `Fetched achievements from Lodestone` | `character_id`, `milestones_found`, `requests_made`, `total_duration` |
| `lodestone.check_achievement.attempt` | Debug | `Checking achievement` | `character_id`, `achievement_id` |
| `lodestone.check_achievement.earned` | Debug | `Achievement earned` | `character_id`, `achievement_id`, `achievement_name`, `duration` |
| `lodestone.check_achievement.not_earned` | Debug | `Achievement not earned` | `character_id`, `achievement_id`, `achievement_name`, `duration` |
| `lodestone.check_achievement.stopping` | Debug | `Stopping achievement discovery, milestone not earned` | `character_id`, `missing_id`, `missing_name`, `milestones_found` |
| `lodestone.check_privacy.private` | Debug | `Achievement list is private` | `character_id` |
| `lodestone.check_privacy.public` | Debug | `Achievement list is public` | `character_id` |
| `lodestone.request_retry` | Warn | `Retrying Lodestone request` | `url`, `attempt`, `max_retries`, `error` |
| `lodestone.rate_limited` | Warn | `Rate limited by Lodestone, backing off` | `character_id`, `retry_after` |

#### `infrastructure/tomestone/client.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `tomestone.request` | Debug | `Fetching character from Tomestone` | `character_id` |
| `tomestone.empty_character` | Debug | `Empty character response from Tomestone` | `character_id` |
| `tomestone.not_found` | Debug | `Character not found on Tomestone` | `character_id` |
| `tomestone.unauthenticated` | Error | `Tomestone API authentication failed` | `status` |
| `tomestone.rate_limited` | Warn | `Rate limited by Tomestone, backing off` | `character_id`, `retry_after` |
| `tomestone.error` | Error | `Tomestone API error` | `character_id`, `status`, `error` |

#### `infrastructure/tomestone/rate_controller.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `tomestone.rate_limit` | Info | `Tomestone rate limit hit, reducing request rate` | `new_rate`, `consecutive_429s` |
| `tomestone.rate_recovery` | Info | `Tomestone rate recovered` | `new_rate` |

#### `infrastructure/rabbitmq/queue.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `rabbitmq.failed.permanent_discard` | Warn | `Discarding permanently failed message` | `event_type`, `reason`, `attempts` |
| `rabbitmq.failed.republish_error` | Error | `Failed to republish failed message` | `event_type`, `error` |
| `rabbitmq.failed.republished` | Info | `Republished failed message for retry` | `event_type`, `attempt` |
| `rabbitmq.permanent_failure` | Warn | `Message permanently failed after max retries` | `event_type`, `attempts` |
| `rabbitmq.failed_publish_error` | Error | `Failed to publish message to dead letter queue` | `event_type`, `error` |
| `rabbitmq.retry` | Warn | `Retrying message publish` | `event_type`, `attempt`, `error` |
| `rabbitmq.retry_publish_error` | Error | `Failed to republish message after retry` | `event_type`, `error` |

#### `domain/proxy/service.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `proxy.process_new.start` | Info | `Processing new proxy` | `proxy_address` |
| `proxy.process_new.exists_failed` | Error | `Failed to check if proxy exists` | `proxy_address`, `error` |
| `proxy.process_new.skipped_exists` | Info | `Proxy already exists, skipping` | `proxy_address` |
| `proxy.process_new.insert_failed` | Error | `Failed to insert new proxy` | `proxy_address`, `error` |
| `proxy.process_new.inserted` | Info | `New proxy added` | `proxy_address` |
| `proxy.process_scan.start` | Info | `Starting proxy scan` | `proxy_count` |
| `proxy.check_failed` | Info | `Proxy check failed` | `proxy_address`, `error` |
| `proxy.check_passed` | Info | `Proxy check passed` | `proxy_address`, `duration` |

#### `domain/proxy/worker/scan.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `scan_worker.pool_allocation` | Info | `Allocated scan worker pool` | `pool_size` |
| `scan_worker.panic` | Error | `Scan worker panicked` | `proxy_address`, `error` |
| `scan_worker.scan_error` | Warn | `Proxy scan failed` | `proxy_address`, `error` |

#### `domain/proxy/worker/worker.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `worker.start` | Info | `Proxy worker started` | `event_type`, `concurrency` |
| `worker.missing_handler` | Error | `No handler registered for event type` | `event_type` |
| `worker.job_start` | Info | `Processing proxy job` | `event_type` |
| `worker.job_done` | Info | `Proxy job completed` | `event_type`, `duration` |
| `worker.job_retry` | Warn | `Proxy job failed, retrying` | `event_type`, `error`, `attempt` |
| `worker.publish_error` | Error | `Failed to publish follow-up proxy job` | `event_type`, `error` |

#### `cmd/cli/consume.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `queue.close_error` | Error | `Failed to close queue connection` | `error` |
| `consume.failed.start` | Info | `Starting failed message consumer` | `event_types`, `concurrency` |

#### `cmd/cli/publish.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `publish.enqueued` | Info | `Published jobs to queue` | `event_type`, `count` |
| `publish.id_sweep` | Info | `Published ID sweep jobs` | `from`, `to`, `count` |
| `publish.id_sweep_gaps.none_found` | Info | `No ID gaps found to fill` | — |
| `publish.id_sweep_daemon.started` | Info | `ID sweep daemon started` | `count`, `chunk_size` |
| `publish.id_sweep_daemon.stopped` | Info | `ID sweep daemon stopped` | — |
| `publish.id_sweep_daemon.initial_sweep_error` | Warn | `ID sweep daemon initial sweep failed` | `error` |
| `publish.id_sweep_daemon.publish_error` | Warn | `ID sweep daemon publish failed` | `error` |
| `publish.character_census` | Info | `Published character census jobs` | `count`, `limit` |
| `publish.achievement_census` | Info | `Published achievement census jobs` | `count`, `limit` |

#### `cmd/cli/proxy.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `proxy.discover.start` | Info | `Starting proxy discovery` | `limit` |
| `proxy.discover.complete` | Info | `Proxy discovery complete` | `discovered`, `duration` |
| `proxy.discover.providers` | Info | `Configured proxy providers` | `providers` |
| `proxy.discover.fetching` | Info | `Fetching proxies from provider` | `provider`, `url` |
| `proxy.discover.limit_reached` | Info | `Proxy discovery limit reached` | `limit` |
| `proxy.discover.provider_done` | Info | `Provider fetch complete` | `provider`, `found` |
| `proxy.discover.lookup_failed` | Error | `Failed to lookup proxies` | `provider`, `error` |
| `proxy.discover.publish_failed` | Error | `Failed to publish proxy job` | `proxy_address`, `error` |
| `proxy.discover.provider_failed` | Error | `Provider fetch failed` | `provider`, `error` |
| `proxy.scan.start` | Info | `Starting proxy scan` | `proxy_count` |

#### `cmd/cli/migrate.go`

| Current | Level | New message | Required attributes |
|---|---|---|---|
| `migrate.queue.no_jobs` | Info | `No queue jobs to migrate` | — |
| `migrate.queue.found` | Info | `Found queue jobs to migrate` | `total`, `by_type` |
| `migrate.queue.dry_run_complete` | Info | `Queue migration dry run complete` | `total` |
| `migrate.queue.published` | Info | `Queue migration published` | `total` |
| `migrate.queue.cleanup` | Info | `Queue migration cleanup complete` | — |
| `migrate.queue.publish_error` | Warn | `Failed to publish migrated job` | `error` |

#### Stdlib log replacements

**File: `cmd/http/server.go`** — Replace `log.Println`/`log.Printf` (pprof messages) with `logging.Info("http.pprof", ...)` / `logging.Error("http.pprof", ...)`. Attributes: `address`, `error`.

**File: `cmd/http/middleware/max_requests.go`** — Replace `log.Printf` (shutdown signals) with `logging.Info`/`logging.Error`. Attributes: `pid`, `signal`, `error`.

**File: `infrastructure/metrics/client.go`** — Replace `log.Printf` with `logging.Error("metrics.send_failed", ...)`. Attributes: `error`.

**File: `infrastructure/textproxy/client.go`** — Already uses `logging.Warn`; update message text to include provider name and URL. Attributes: `provider`, `url`, `error`.

**File: `container/infrastructure.go`** and **`container/domain.go`** — Update message text to be descriptive. Attributes: `component`, `error`.

**File: `cmd/http/app/ui/*.go`** — Update error log messages to include page/action context. Attributes: `page`, `error`.

### Step 4: Update logging tests

**File: `domain/census/handler/logging_test.go`** — Update expected log message strings to match new descriptive messages. The test structure (buffer logger, attribute checking) stays the same.

### Step 5: Save plan and update documentation

**First action**: Write this plan to `docs/superpowers/plans/2026-08-23-scraper-fix-logging-overhaul.md` before making any code changes.

**After implementation**: Update the following documentation to reflect the changes:
- `docs/logging-and-middleware.md` — Update the log event tables with the new descriptive message names and attribute lists. Update the level policy section if any levels changed.
- `docs/lodestone.md` — Document the `stripTags` HTML sanitization and the removal of avatar/portrait fields.
- `docs/postgres.md` — Document the `00013_drop_avatar_portrait` migration.
- `docs/architecture.md` — If it references avatar/portrait fields, remove those references.

### Step 6: Refresh contaminated database entries

After the fix is deployed, re-queue the 1,425 contaminated character IDs for re-processing. The user confirmed they can run `id-sweep` or `character-census` to fix these. The contaminated entries all have `last_census_at >= '2026-08-23 09:48:03+00'`. A SQL query can extract the IDs for re-publishing.

## Critical files & anchors

1. **`infrastructure/lodestone/client.go`** — `extractTextBetween` (line ~496), `extractAllTextBetween` (line ~511), `parseCharacterProfile` (line ~427): root cause of HTML contamination + avatar/portrait removal
2. **`infrastructure/postgres/repository/character.go`** — `characterColumns` (line 23), `Upsert` (line 28), `scanCharacter` (line 535): column removal
3. **`port/contract/census.go`** — `CharacterRecord` struct (line 10): field removal
4. **`domain/census/handler/character.go`** — `Handle` method: logging overhaul primary target
5. **`domain/census/handler/achievement.go`** — `Handle` method: logging overhaul primary target

## Verification

1. **HTML stripping**: Run `go test ./infrastructure/lodestone/...` — new tests for `stripTags`, `extractTextBetween`, `extractAllTextBetween` with HTML input must pass
2. **Column removal**: Run `go test ./infrastructure/postgres/repository/...` — existing character tests must pass with updated schema
3. **Full test suite**: `make test` — all tests pass
4. **Build**: `make build` — binary compiles
5. **Lint**: `make lint` — no lint errors
6. **Manual DB check**: After deploying, query a re-processed character and verify `world`, `race`, `tribe`, `grand_company`, `fc_name` contain clean text without HTML tags
7. **Logging**: Run the app with `LOGGING_LEVEL=debug` and verify log messages are descriptive sentences with structured attributes
8. **Documentation**: Verify `docs/logging-and-middleware.md`, `docs/lodestone.md`, `docs/postgres.md` are updated to reflect all changes

## Assumptions & contingencies

- **DB connection**: The `.env` `POSTGRES_DSN` uses user `census` but the actual DB user is `admin`. The migration will run at app boot via goose. If the app's DB user lacks ALTER TABLE permissions, the migration must be run manually.
- **Contaminated data scope**: All 1,425 rows with `last_census_at >= '2026-08-23 09:48:03+00'` are contaminated. If the app re-processes these via the normal publish/consume cycle, no manual intervention is needed beyond triggering the re-publish.
