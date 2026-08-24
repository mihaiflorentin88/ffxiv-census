# UI Statistics Read Model and 90M-Character Query Scaling — Design Specification

## Status

Proposed. Implementation must not start until the user explicitly says `proceed`.

## Problem statement

The UI currently calculates analytics directly from the transactional `characters`,
`character_jobs`, and `character_milestones` tables during every page request. This
already fails in production with approximately 204,000 characters and 2.44 million
job rows, and it cannot scale to the target of 80–90 million characters.

Production measurements on 2026-08-24 showed:

| Surface | Observed result |
|---|---:|
| `/ui/dashboard` | No response body after 25–30 seconds |
| `/ui/races` | 3.23 seconds |
| `/ui/worlds` | 0.35 seconds |
| `/ui/expansions` | 6.58 seconds |
| `/ui/methodology` | 0.08 seconds |

The dashboard's `SummaryCounts` query is the immediate failure. PostgreSQL gives it
an estimated cost of approximately 2.2 billion because the max-level expression
materializes qualifying `character_jobs` rows and rechecks the subplan while scanning
characters. During one production probe, three copies of this query remained active
for more than 15 seconds after clients had timed out.

The broader issue is architectural: combining several scans into fewer SQL statements
reduces round trips, but it does not reduce the amount of raw data scanned. A request
path that scans 90 million rows is not a scalable request path.

## Goals

1. Restore `/ui/dashboard` and remove the pathological max-level query plan.
2. Make analytics UI request cost independent of raw character-table cardinality.
3. Serve all aggregate UI pages from one consistent, timestamped data snapshot.
4. Limit each web process to at most one small snapshot read per cache refresh period,
   not one or more raw aggregate queries per HTTP request.
5. Preserve all existing dashboard, race, world, expansion, and world-detail numbers,
   filters, sort orders, and HTMX behavior.
6. Make statistics freshness, refresh failures, and UI cache behavior observable.
7. Provide a safe first-release bootstrap and rollback path.
8. Validate request-path behavior with automated tests and production measurements.
9. Preserve HTMX progressive enhancement and make response caching compatible with a
   future reverse proxy without requiring Varnish for acceptable performance.

## Non-goals

- Redesigning the visual identity or charts.
- Re-enabling personal character pages that are intentionally disabled.
- Replacing PostgreSQL, introducing Redis, or adding a frontend build pipeline.
- Deploying Varnish or another reverse-proxy cache in this change.
- Claiming that every export or arbitrary character-search query is constant-time at
  90 million rows. This work scales the aggregate UI and matching aggregate REST APIs.
- Maintaining second-by-second analytics. UI statistics become intentionally
  eventually consistent and display their generation time.

## Existing query inventory

| Route | Current DB calls | Important raw work |
|---|---:|---|
| `/ui/dashboard` | 4 | summary scan plus jobs subplan, two character grouping scans inside `UNION ALL`, milestone aggregation, 30-day milestone aggregation |
| `/ui/partials/world-breakdown` | 1 | global world grouping scan |
| `/ui/races` | 2 | one race scan plus three more character grouping scans inside demographic `UNION ALL` |
| `/ui/worlds` | 1 | global world grouping scan |
| `/ui/expansions` | 4 | three concurrent summary queries plus expansion milestone aggregation |
| `/ui/worlds/{world}` | 7 | total, active, new, races, expansions, timeline, and metadata |
| `/ui/methodology` | 0 | configuration only |

Concurrency hides some latency when the database is idle, but under load it multiplies
CPU, disk, connection-pool, and cancellation pressure.

## Chosen architecture

### 1. Persist one versioned statistics snapshot

Add a PostgreSQL table containing a single current snapshot row. The payload is JSONB
for atomic replacement and operational inspectability; Go reads and writes it through
a typed port, so JSONB does not leak into the domain or UI.

```sql
CREATE TABLE ui_stats_snapshots (
    snapshot_key          TEXT PRIMARY KEY,
    schema_version        INTEGER NOT NULL,
    generated_at          TIMESTAMPTZ NOT NULL,
    activity_since        TIMESTAMPTZ NOT NULL,
    max_level             INTEGER NOT NULL,
    source_character_count BIGINT NOT NULL,
    refresh_duration_ms   BIGINT NOT NULL,
    payload               JSONB NOT NULL,
    CHECK (snapshot_key = 'current'),
    CHECK (schema_version = 1)
);
```

The refresh operation builds a complete typed value first and then performs one
`INSERT ... ON CONFLICT DO UPDATE`. Readers therefore see either the previous complete
snapshot or the new complete snapshot—never a partially refreshed result.

### 2. Keep the request path bounded

Each web process maintains an immutable in-memory copy of the current snapshot.
Requests read that copy without touching PostgreSQL. On cache expiry, a singleflight
loader performs one primary-key lookup; concurrent requests continue using the stale
snapshot while that load is in progress. A load error never evicts a valid snapshot.

If no snapshot has ever been loaded, analytics routes fail quickly with HTTP 503,
`Retry-After`, and a clear unavailable state. They must never fall back to raw aggregate
queries, because that would recreate the outage precisely when refresh is unhealthy.

Expected steady-state DB-query behavior:

```text
HTTP requests ──► immutable in-process snapshot ──► render
                         │
                         └── once per TTL per pod ──► SELECT ... WHERE snapshot_key='current'
```

### 3. Refresh outside HTTP requests

Add a `refresh ui-stats` CLI command and a Kubernetes CronJob. It runs on a controlled
schedule, acquires a PostgreSQL advisory lock, computes the next snapshot, and swaps it
atomically. Overlapping refreshes exit successfully after reporting that another
refresh owns the lock.

The initial schedule was every six hours to establish predictable database load.
Production measurements at roughly 208,000 characters showed a 6.9-second refresh,
so the deployed schedule is now hourly at minute 17. Continue to shorten the configurable
schedule only when production refresh-duration and database-load measurements support it.

The refresh uses a read-only, repeatable-read transaction so character and milestone
sections describe one database snapshot. A configurable statement timeout bounds the
work. Failure preserves the prior snapshot.

### 4. Scan raw data only during controlled refresh

Character totals and demographic groups are produced by one `GROUPING SETS` query,
not separate scans for each page. Milestone completion and Chocobo timeline groups are
produced by one second query. The resulting row count is determined by the finite set
of FFXIV regions, datacenters, worlds, races, tribes, genders, expansions, and days—not
by the number of characters.

The snapshot contains global, region, datacenter, and world scopes needed to reproduce
all current UI filters and drill-downs in memory.

### 5. Remove the max-level jobs join from analytics

Add `characters.max_job_level SMALLINT NOT NULL DEFAULT 0`, backfill it once from
`character_jobs`, and keep it current in `CharacterRepository.Upsert`. This converts
max-level statistics from a multi-million/billion-row jobs operation to a predicate on
the character row already being aggregated.

```go
func maxJobLevel(jobs []contract.ClassJobRecord) uint32 {
	var max uint32
	for _, job := range jobs {
		if uint32(job.Level) > max {
			max = uint32(job.Level)
		}
	}
	return max
}
```

The existing direct `SummaryCounts` method is also rewritten to use
`max_job_level >= $2`, fixing any non-snapshot caller and eliminating the observed
pathological plan.

### 6. Reuse the snapshot for aggregate REST APIs

The public summary, breakdown, new-character, and expansion aggregate endpoints must
read from the same snapshot service. Otherwise API traffic could continue issuing the
same large scans the UI work is intended to remove. Date ranges or dimensions that the
snapshot does not contain return a documented validation error rather than silently
falling back to a raw full-table scan.

### 7. Preserve HTMX and expose standards-based cache semantics

HTMX remains the interaction layer for world drill-down and future partial updates.
Caching must distinguish full-page and partial representations and must never mix
filter/query variants.

- ETags include snapshot generation time, route, normalized query parameters, and
  representation type.
- Responses include `Vary: HX-Request, Accept-Encoding` where a route can vary by HTMX
  request headers.
- The explicit world-drilldown partial uses its normalized region in the ETag/cache key.
- A matching `If-None-Match` returns 304 without rendering the template.
- Cache headers use standard HTTP semantics, so a future Varnish deployment can honor
  them without application-specific invalidation hooks.
- The application remains fast without a reverse proxy because warm requests read only
  immutable in-process data.

## Snapshot contract

The domain representation is explicit and versioned:

```go
type UIStatsSnapshot struct {
	SchemaVersion   int
	GeneratedAt     time.Time
	ActivitySince   time.Time
	MaxLevel        uint32
	SourceCharacters int64
	Summary         StatsSummary
	Groups          []ScopedGroupCount
	Expansions      []ScopedExpansionCount
	NewCharacters   []ScopedDailyCount
}

type StatsScope struct {
	Region     string
	Datacenter string
	World      string
}

type ScopedGroupCount struct {
	Scope     StatsScope
	Dimension string // race, tribe, gender, race_gender, world
	Key       string
	Total     int64
	Active    int64
}
```

The service validates `SchemaVersion`, UTC timestamps, non-negative counts, unique
group keys, and `active <= total` before publishing or accepting a loaded snapshot.

## Freshness and user experience

- Analytics pages display `Statistics updated <timestamp>`.
- Responses include an ETag derived from the snapshot generation timestamp.
- Responses use conservative browser caching, for example:
  `Cache-Control: public, max-age=60, stale-while-revalidate=300`.
- HTMX partials retain their current targets, swap behavior, and progressive fallback.
- A stale but valid snapshot remains available when refresh fails.
- If snapshot age exceeds a configurable warning threshold, the UI shows a subtle
  freshness warning without removing the data.
- If no snapshot exists, the page responds quickly with 503 instead of hanging.

## Observability

Add metrics with bounded labels:

| Metric | Type | Meaning |
|---|---|---|
| `ui_stats_snapshot_age_seconds` | gauge | Age of the snapshot currently served |
| `ui_stats_refresh_duration_seconds` | histogram | End-to-end refresh duration |
| `ui_stats_refresh_total{result}` | counter | success, error, or lock-skipped refreshes |
| `ui_stats_cache_total{result}` | counter | hit, reload, stale-served, or error |
| `ui_stats_payload_bytes` | gauge | Serialized snapshot size |

Refresh logs include generation time, source character count, payload size, duration,
and result. They must not log the full payload.

## Failure behavior

| Failure | Behavior |
|---|---|
| Refresh query timeout | Transaction rolls back; previous row remains current |
| Concurrent CronJobs | Advisory-lock loser reports `skipped` and exits zero |
| Snapshot decode/version error | Existing in-memory valid snapshot remains served; metric/log emitted |
| PostgreSQL unavailable with warm cache | Stale snapshot remains served |
| PostgreSQL unavailable with cold cache | Fast 503 with `Retry-After` |
| Empty snapshot at first release | Initial manual refresh job is mandatory before route acceptance checks |

## Release and rollback

The release sequence must apply the migration, deploy the CLI and snapshot-backed
handlers, run one manual refresh job, wait for success, then probe every route. Because
the dashboard is currently unavailable, a short initial 503 window is preferable to
running unbounded fallback scans.

Rollback keeps the snapshot table and `max_job_level` column in place; the previous
binary ignores them. Do not run the down migration during rollback. This makes rollback
non-destructive and avoids re-running the backfill later.

## Acceptance criteria

1. No aggregate UI handler calls raw `Summary`, `Breakdown`,
   `DemographicBreakdown`, `ExpansionCompletions`, or `NewCharacters` methods.
2. Warm-cache analytics requests execute zero SQL queries.
3. A cache reload executes exactly one indexed snapshot-row query per web process.
4. `/ui/dashboard`, `/ui/races`, `/ui/worlds`, `/ui/expansions`, world drill-down,
   and world detail preserve current values and filtering behavior.
5. The direct max-level summary query no longer references `character_jobs`.
6. Concurrent requests trigger only one snapshot reload and pass `go test -race`.
7. Refresh failure preserves the last good snapshot.
8. Production route time-to-first-byte is under 1 second and p95 is under 500 ms after
   warm-up, subject to network latency.
9. The request-path plan remains a primary-key lookup at 1 million and synthetic
   90-million-row planner statistics.
10. Documentation describes freshness, refresh operations, metrics, failure states,
    and the release/bootstrap procedure.
