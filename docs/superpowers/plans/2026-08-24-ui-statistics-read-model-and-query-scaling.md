# Implementation Plan: UI Statistics Read Model and 90M-Character Query Scaling

> **Implementation gate:** Do not edit production code until the user explicitly says
> `proceed`. Follow strict red/green/refactor TDD for every production change.

## Objective

Replace per-request analytics over large transactional tables with a versioned,
periodically refreshed statistics snapshot. Restore the dashboard immediately, bound
UI database work independently of the target 80–90 million character rows, update all
affected documentation, test the implementation, commit and push it, release with the
README procedure, and verify the production routes and metrics.

The companion design is:
`docs/superpowers/specs/2026-08-24-ui-statistics-read-model-and-query-scaling.md`.

## Baseline evidence to retain in the implementation PR/commit notes

- Production row estimates: 203,784 characters; 2,443,039 character jobs; 114,126
  character milestones.
- Production route timing: dashboard >25 seconds/no body; races 3.23 seconds; worlds
  0.35 seconds; expansions 6.58 seconds; methodology 0.08 seconds.
- Dashboard summary plan estimate: approximately 2.2 billion cost units.
- The slow plan materializes about 334,894 `character_jobs` rows and evaluates that
  subplan from the character aggregate.
- Three copies of the summary query were observed active concurrently for >15 seconds
  after timed-out dashboard requests.

## Task 1 — Add the snapshot domain model and port using TDD

**Files:**

- Create `domain/census/ui_stats.go`
- Create `domain/census/ui_stats_test.go`
- Create `port/contract/ui_stats_repository.go`
- Create `mock/repository/ui_stats.go`
- Update `port/contract/README.md`

### Red

Write table-driven tests for:

- schema version validation;
- UTC timestamp normalization;
- negative counts;
- active count greater than total;
- duplicate scoped keys;
- lookup by global/region/datacenter/world scope;
- deterministic world, race, expansion, and day ordering;
- unknown scope/dimension errors.

Run and observe failure:

```bash
go test -run 'TestUIStats' ./domain/census ./mock/repository
```

### Green

Define a technology-neutral contract. Keep JSON encoding out of the port:

```go
const UIStatsSchemaVersion = 1

type UIStatsRepository interface {
	LoadCurrent(ctx context.Context) (*UIStatsSnapshot, error)
	Refresh(ctx context.Context, opts UIStatsRefreshOptions) (*UIStatsRefreshResult, error)
}

type UIStatsRefreshOptions struct {
	ActivitySince time.Time
	MaxLevel      uint32
	Timeout       time.Duration
}
```

Create typed snapshot data that covers:

- total, active, and max-level summary;
- global region and world totals;
- race, tribe, gender, and race×gender under global, region, datacenter, and world
  scopes;
- expansion completions globally and per world;
- 30-day Chocobo timeline globally and per world;
- generation/freshness metadata.

The fake must record load and refresh calls, return defensive copies, support error
injection, and satisfy the port compile-time assertion.

### Refactor

Keep lookup indexes internal to the domain service and serialized fields simple.
Re-run the focused tests before moving on.

## Task 2 — Add `max_job_level` and repair the pathological summary query

**Files:**

- Create `infrastructure/postgres/migration/query/00015_ui_stats_snapshot.sql`
- Update `infrastructure/postgres/repository/character.go`
- Update `infrastructure/postgres/repository/character_test.go`
- Update `mock/repository/character.go`
- Update `mock/repository/character_test.go`
- Update `port/contract/character_repository.go` comments

### Red

Add PostgreSQL and fake tests proving:

1. an insert stores the highest supplied job level;
2. re-census with lower/different jobs replaces the stored maximum;
3. an empty job set stores zero;
4. `SummaryCounts` returns total, active, and max-level counts correctly;
5. the SQL used by `SummaryCounts` no longer contains `character_jobs`.

Run the focused package test and record the expected failure:

```bash
go test -run 'TestCharacterRepository_(UpsertMaxJobLevel|SummaryCounts)' \
  ./infrastructure/postgres/repository
```

### Green

Create the schema safely:

```sql
ALTER TABLE characters
    ADD COLUMN max_job_level SMALLINT NOT NULL DEFAULT 0;

UPDATE characters c
SET max_job_level = j.max_level
FROM (
    SELECT character_id, MAX(level)::SMALLINT AS max_level
    FROM character_jobs
    GROUP BY character_id
) j
WHERE j.character_id = c.id;

CREATE TABLE ui_stats_snapshots (
    snapshot_key           TEXT PRIMARY KEY,
    schema_version         INTEGER NOT NULL,
    generated_at           TIMESTAMPTZ NOT NULL,
    activity_since         TIMESTAMPTZ NOT NULL,
    max_level              INTEGER NOT NULL,
    source_character_count BIGINT NOT NULL,
    refresh_duration_ms    BIGINT NOT NULL,
    payload                JSONB NOT NULL,
    CHECK (snapshot_key = 'current'),
    CHECK (schema_version = 1)
);
```

The down migration may drop the snapshot table and column, but production rollback
must not execute it.

Compute the maximum once per upsert in Go and include it in both insert and conflict
update clauses:

```go
maxLevel := maxJobLevel(jobs)

// INSERT ... max_job_level ... VALUES (..., $21)
// ON CONFLICT ... DO UPDATE SET
//     max_job_level = excluded.max_job_level
```

Rewrite `SummaryCounts`:

```sql
SELECT COUNT(*) AS total,
       COUNT(*) FILTER (WHERE latest_achievement_at >= $1) AS active,
       COUNT(*) FILTER (WHERE max_job_level >= $2) AS max_level
FROM characters
WHERE deleted_at IS NULL
```

### Refactor and verify

Run `EXPLAIN` in the PostgreSQL integration test and assert the plan text does not
reference `character_jobs`. Do not assert fragile cost numbers or a specific scan type.

## Task 3 — Implement PostgreSQL snapshot load and refresh using TDD

**Files:**

- Create `infrastructure/postgres/repository/ui_stats.go`
- Create `infrastructure/postgres/repository/ui_stats_test.go`
- Update `infrastructure/postgres/repository/helpers_test.go` only if shared seed helpers
  are required

### Red: repository behavior

Write real PostgreSQL tests for:

- no-row result returns the typed not-found error;
- JSON round trip preserves every snapshot field;
- unsupported schema version is rejected;
- replacement is atomic;
- refresh uses non-deleted characters only;
- active cutoff is inclusive;
- max-level uses `characters.max_job_level`;
- every scope/dimension combination has correct counts;
- empty/null demographic values retain existing `Unknown` semantics;
- expansions count distinct eligible characters;
- Chocobo achievement 590 groups by UTC day;
- advisory-lock contention returns a skipped result without error;
- canceled/timed-out refresh leaves the previous snapshot untouched.

Run the new tests and observe failure:

```bash
go test -run 'TestUIStatsRepository' ./infrastructure/postgres/repository
```

### Green: load query

The request-adjacent load is a single primary-key lookup:

```sql
SELECT schema_version, generated_at, activity_since, max_level,
       source_character_count, refresh_duration_ms, payload
FROM ui_stats_snapshots
WHERE snapshot_key = 'current'
```

Validate metadata and payload after decoding. Return a defensive value.

### Green: refresh transaction and lock

Use one acquired connection so the session advisory lock is reliable:

```go
db, err := r.driver.Acquire(ctx)
// db.Conn(ctx), pg_try_advisory_lock(...), defer pg_advisory_unlock(...)
// BeginTx with ReadOnly: true, Isolation: sql.LevelRepeatableRead for reads.
// Build and validate the typed snapshot.
// End the read transaction, then atomically upsert the completed payload.
```

Do not hold a SQL transaction open while JSON marshaling. The read phase is consistent;
the final one-row upsert is independently atomic.

### Green: character aggregate query

Use one scan with grouping sets. The implementation may wrap the result in a CTE to
map `GROUPING()` flags to typed scope/dimension values, but it must not use one UNION
branch per UI chart.

Representative shape:

```sql
SELECT region, datacenter, world, race, tribe, gender,
       GROUPING(region) AS g_region,
       GROUPING(datacenter) AS g_dc,
       GROUPING(world) AS g_world,
       GROUPING(race) AS g_race,
       GROUPING(tribe) AS g_tribe,
       GROUPING(gender) AS g_gender,
       COUNT(*) AS total,
       COUNT(*) FILTER (WHERE latest_achievement_at >= $1) AS active,
       COUNT(*) FILTER (WHERE max_job_level >= $2) AS max_level
FROM characters
WHERE deleted_at IS NULL
GROUP BY GROUPING SETS (
    (), (region), (world),
    (race), (region, race), (datacenter, race), (world, race),
    (tribe), (region, tribe), (datacenter, tribe), (world, tribe),
    (gender), (region, gender), (datacenter, gender), (world, gender),
    (race, gender), (region, race, gender),
    (datacenter, race, gender), (world, race, gender)
)
```

Before implementation, confirm with `EXPLAIN` that PostgreSQL performs one base
character scan. If PostgreSQL chooses an unacceptable plan on the integration fixture,
split this into a small, fixed number of refresh-only queries; never reintroduce scans
to HTTP handlers.

### Green: milestone aggregate query

Query only registered expansion milestones and Chocobo 590, producing global and
per-world results in one controlled refresh query:

```sql
WITH tracked AS (
    SELECT cm.character_id, cm.achievement_id, cm.achieved_at,
           m.expansion, c.world
    FROM character_milestones cm
    JOIN milestone_achievements m ON m.achievement_id = cm.achievement_id
    JOIN characters c ON c.id = cm.character_id
    WHERE c.deleted_at IS NULL
      AND (cm.achievement_id = 590 OR
           ((m.kind = 'expansion_msq' OR m.kind = 'expansion')
             AND m.expansion IS NOT NULL))
)
-- aggregate expansion and UTC-day results from tracked
```

Add or adjust a covering index only after `EXPLAIN` demonstrates it is used. The likely
candidate is:

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_character_milestones_stats
    ON character_milestones (achievement_id, character_id, achieved_at);
```

Because `CREATE INDEX CONCURRENTLY` cannot run inside a normal Goose transaction block,
either use a Goose no-transaction migration supported by the current provider or omit
the new index when the existing `(achievement_id, achieved_at)` index is sufficient.
Do not ship a blocking redundant index by assumption.

### Green: atomic publish

```sql
INSERT INTO ui_stats_snapshots (
    snapshot_key, schema_version, generated_at, activity_since, max_level,
    source_character_count, refresh_duration_ms, payload
) VALUES ('current', $1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (snapshot_key) DO UPDATE SET
    schema_version = excluded.schema_version,
    generated_at = excluded.generated_at,
    activity_since = excluded.activity_since,
    max_level = excluded.max_level,
    source_character_count = excluded.source_character_count,
    refresh_duration_ms = excluded.refresh_duration_ms,
    payload = excluded.payload
```

### Refactor

Keep SQL result decoding helpers private to the adapter. Keep sorting and display
semantics in the domain/UI, not in JSONB queries.

## Task 4 — Add the snapshot service and stale-while-revalidate cache using TDD

**Files:**

- Create `domain/census/ui_stats_service.go`
- Create `domain/census/ui_stats_service_test.go`
- Update `config/config.go`
- Update `config/config.toml`
- Update config tests

### Red

Use the fake repository and a controllable clock to test:

- cold load success;
- cache hit performs no repository call;
- concurrent expired-cache requests perform one reload;
- warm-cache reload error returns the last good snapshot;
- cold-cache load error returns unavailable;
- stale threshold and age calculation;
- returned snapshots cannot mutate the cached copy;
- unsupported schema versions are never cached.

Run with the race detector:

```bash
go test -race -run 'TestUIStatsService' ./domain/census
```

### Green

Add configuration:

```toml
[census.ui_stats]
cache_ttl = "1m"
stale_warning = "12h"
refresh_timeout = "2h"
```

Environment overrides naturally become:

```text
CENSUS_UI_STATS_CACHE_TTL
CENSUS_UI_STATS_STALE_WARNING
CENSUS_UI_STATS_REFRESH_TIMEOUT
```

Use `sync.RWMutex` plus a small internal singleflight mechanism; do not add a dependency
for one key. Expired-cache callers should receive the last good value immediately while
one goroutine reloads. Cold-cache callers may wait for the first bounded load.

## Task 5 — Wire the repository, service, CLI refresh command, and metrics using TDD

**Files:**

- Update `container/infrastructure.go`
- Update `container/domain.go`
- Add/update container tests
- Create `cmd/cli/refresh.go`
- Create `cmd/cli/refresh_test.go`
- Update CLI root command registration
- Update `infrastructure/metrics/collectors.go`
- Update metric tests and dashboard JSON as appropriate

### Red

Test:

- lazy singleton repository/service accessors;
- nil/error behavior consistent with existing container patterns;
- `refresh ui-stats` passes configured activity window, max level, and timeout;
- successful, failed, and lock-skipped exit behavior;
- metrics update for refresh result, duration, snapshot age, payload bytes, and cache
  outcomes.

### Green

CLI contract:

```bash
./bin/ffxiv-census refresh ui-stats
```

Expected logs/output must include only summary metadata:

```text
ui stats refresh complete: generated_at=... characters=... duration=... payload_bytes=...
```

Return non-zero on genuine failure. Return zero for advisory-lock skip so overlapping
CronJobs do not appear failed.

## Task 6 — Convert UI handlers to snapshot-only reads using strict TDD

**Files:**

- Update `cmd/http/ui/controller.go`
- Update `cmd/http/ui/routes.go`
- Update `cmd/http/ui/dashboard.go`
- Update `cmd/http/ui/races.go`
- Update `cmd/http/ui/worlds.go`
- Update `cmd/http/ui/world_detail.go`
- Update `cmd/http/ui/expansions.go`
- Update relevant templates/partials to show generation time and unavailable state
- Update all UI handler tests

### Red

For each analytics route, write tests that seed only the UI-stats fake/snapshot and
prove the rendered values. Add spies or failing raw repositories to prove the handler
does not call any live aggregate method.

Required cases:

- dashboard summary, race chart, region rows, expansion order, and 30-day zero fill;
- region world drill-down;
- races global/region/DC/world cascading filters and all demographic charts;
- worlds global/region/DC filters and ordering;
- expansions totals, retention, and drop-off calculations;
- world detail metadata, races, milestones, and timeline;
- snapshot generation timestamp;
- stale warning;
- cold-cache unavailable response is 503 with `Retry-After`;
- ETag/304 and Cache-Control behavior;
- HTMX full-page/partial representations cannot share an incorrect cache entry;
- `Vary: HX-Request, Accept-Encoding` is set where representation can vary;
- normalized filter/query parameters participate in ETag generation;
- methodology remains database-free.

Run the handler suite and observe failure before implementation:

```bash
go test -run 'Test(Dashboard|Races|Worlds|WorldDetail|Expansions|Methodology)' \
  ./cmd/http/ui
```

### Green

Inject the dedicated snapshot service:

```go
type UIController struct {
	svc     *census.Service       // configuration/non-aggregate behavior only
	stats   *census.UIStatsService
	q       contract.Queue
}
```

Every aggregate handler starts from one immutable snapshot:

```go
snapshot, state, err := c.stats.Current(r.Context())
if err != nil {
	c.renderStatsUnavailable(w, r)
	return
}
```

Remove handler goroutines and mutexes that existed only to parallelize raw queries.
All filtering and view-model construction now operates on small in-memory slices/maps.

Keep HTMX vendored and preserve the current partial routes, targets, swaps, and
progressive-enhancement behavior. Generate ETags from the snapshot timestamp, route,
normalized filters, and representation. These standard headers make a future Varnish
layer possible without adding it now; Varnish is not required to meet this plan's
latency targets.

Do not keep a raw-query fallback. Add a compile/test guard that searches analytics UI
files for forbidden service calls if a direct dependency remains difficult to spy on.

### Template parsing optimization

While touching controller construction, parse embedded templates once and keep
immutable template pointers by page/partial. Add a test proving repeated renders do not
reparse. This removes avoidable CPU work but remains secondary to database optimization.

### Frontend verification

The UI is being preserved, not redesigned. After UI edits, run the required detector
once over the changed targets:

```bash
node /home/mihai/.codex/skills/impeccable/scripts/detect.mjs --json \
  cmd/http/ui/templates cmd/http/ui/assets/styles.css
```

Fix only findings introduced or exposed by this change.

## Task 7 — Convert aggregate REST APIs to the same read model using TDD

**Files:**

- Update `cmd/http/app/census/handler/census.go`
- Update `cmd/http/app/census/handler/census_test.go`
- Update route/controller wiring if needed
- Update Swagger descriptions

### Red

Test that summary, supported breakdowns, default 30-day new-character data, and
expansion completions use the snapshot and do not invoke raw aggregate repositories.
Test unsupported custom ranges explicitly.

### Green

Serve snapshot-compatible queries from the same `UIStatsService`. Preserve existing
response DTO shapes wherever possible. If an endpoint currently promises arbitrary date
ranges that the 30-day snapshot cannot answer, either:

1. retain a separately bounded/indexed range query with a hard maximum span and timeout;
   or
2. return HTTP 400 for unsupported ranges and document the limit.

Choose based on the current published route contract and tests; never silently return a
different range.

## Task 8 — Add the refresh CronJob and safe first-snapshot bootstrap

**Files:**

- Update `k8s/values.yaml`
- Update `k8s/templates/cronjobs.yaml`
- Update `k8s/Makefile` with a non-destructive refresh/bootstrap helper if useful
- Update Helm tests/lint expectations

Add a CronJob entry with low concurrency and predictable resource limits:

```yaml
- name: refresh-ui-stats
  schedule: "17 * * * *"
  concurrencyPolicy: Forbid
  command:
    - /app/ffxiv-census
    - refresh
    - ui-stats
```

Set `successfulJobsHistoryLimit`, `failedJobsHistoryLimit`, and an appropriate
`activeDeadlineSeconds` longer than `refresh_timeout`. The database advisory lock remains
the authoritative cross-release lock.

The original six-hour interval established a production baseline. After deployment,
the refresh completed in 6.9 seconds at roughly 208,000 characters with ample database
headroom, so run it hourly at minute 17. Keep the schedule regression-covered and retain
`concurrencyPolicy: Forbid` as the dataset grows.

Add a documented one-shot bootstrap command using a unique job name:

```bash
kubectl -n default create job \
  --from=cronjob/ffxiv-census-cron-refresh-ui-stats \
  ffxiv-census-ui-stats-bootstrap-<release>
kubectl -n default wait --for=condition=complete \
  job/ffxiv-census-ui-stats-bootstrap-<release> --timeout=2h
```

Resolve the actual rendered CronJob name with `kubectl get cronjob` before executing;
do not assume the illustrative name if Helm naming differs.

## Task 9 — Scale, query-plan, concurrency, and regression verification

### Automated correctness

Run in this order:

```bash
go test ./domain/census ./infrastructure/postgres/repository ./cmd/http/ui
go test -race ./domain/census ./cmd/http/ui
make test
make fmt
make lint
make build
helm lint k8s -f k8s/values.yaml
```

`make fmt` is allowed only after all red/green steps are complete; inspect its diff so
unrelated files are not changed.

### Query-count assertions

Add a counting fake/driver and assert:

| Scenario | Expected SQL |
|---|---:|
| warm cached UI page | 0 |
| first page on cold process | 1 snapshot primary-key read |
| 100 concurrent requests after TTL | 1 reload total |
| snapshot refresh | fixed number of aggregate reads plus 1 atomic write |

### Scale fixture

Provide an opt-in PostgreSQL scale test using `generate_series`. Default CI uses at
least 1 million synthetic characters. A manual production-readiness run accepts an
environment-selected count:

```bash
UI_STATS_SCALE_ROWS=1000000 go test -run TestUIStatsRefreshScale \
  -count=1 -v ./infrastructure/postgres/repository
```

Do not insert 90 million rows into the normal developer/CI database. For 90M readiness:

- use `EXPLAIN (FORMAT JSON)` with production-like statistics for request-path plans;
- run the full 90M refresh benchmark only in an explicitly provisioned staging database;
- record refresh duration, temp-file use, peak CPU/I/O, snapshot payload size, and page
  latency;
- confirm refresh does not starve ingest workers.

### Performance acceptance

- warm handlers: no SQL and sub-50 ms local handler benchmark;
- cold handler: one indexed snapshot lookup;
- production TTFB <1 second for every aggregate UI route;
- production warm p95 <500 ms;
- no aggregate query remains active after its context/statement timeout;
- refresh finishes within 25% of its six-hour interval, otherwise the release is not
  considered 90M-ready and the refresh frequency must not be increased.

## Task 10 — Update documentation in the same implementation commit

**Files:**

- `README.md`
- `docs/ui.md`
- `docs/postgres.md`
- `docs/census.md`
- `docs/http-api.md`
- `docs/metrics.md`
- `docs/container.md`
- `docs/getting-started.md` if local refresh setup needs explanation
- `dashboards/README.md` and relevant Grafana JSON when metrics panels change

Required documentation changes:

1. Remove claims that UI pages calculate real-time data through 4 concurrent raw calls.
2. Document snapshot freshness, generation timestamp, stale behavior, and 503 bootstrap
   behavior.
3. Document `refresh ui-stats`, configuration keys, environment overrides, CronJob,
   advisory lock, and initial bootstrap.
4. Add migration `00015` and `max_job_level` to PostgreSQL schema docs.
5. Document which aggregate REST calls use snapshots and any bounded range limits.
6. Document all new metrics and alert suggestions.
7. Update route query-count inventory to show zero warm-cache queries and one cold-cache
   snapshot read.
8. Add troubleshooting steps for missing, stale, or version-incompatible snapshots.
9. State the tested scale and do not claim a completed 90M full-data benchmark unless it
   was actually run.

## Task 11 — Commit, push, release, and production verification

### Implementation verification record

Completed on 2026-08-24 before release:

- `make test`, `make lint`, `make fmt`, `make build`, and `helm lint` passed.
- `go test -race ./domain/census ./cmd/http/ui` passed.
- `UI_STATS_SCALE_ROWS=1000000 go test -run TestUIStatsRefreshScale -count=1 -v ./infrastructure/postgres/repository` passed; aggregation took 1.49 seconds and produced a 7,168-byte snapshot on the development PostgreSQL instance.
- `EXPLAIN` showed a single `characters` scan for summary counts and a primary-key index scan for the request-path snapshot load; the former `character_jobs` subplan is absent.
- Local warm route TTFB was 0.36 ms for dashboard, 0.37 ms for races, 0.31 ms for worlds, and 1.26 ms for expansions.
- The Impeccable detector reported three pre-existing warnings (methodology side accent, stat-card accent, and width transition); none came from the snapshot/freshness changes, so they were left outside this performance change.

The production 206,741-character initial refresh took 15.98 seconds and produced a 445,614-byte payload. A full 80–90 million-row staging refresh has not been run; the staging acceptance measurements above remain required before claiming refresh-side readiness at that scale. HTTP request cost is already independent of source-table cardinality.

### Pre-release repository checks

```bash
git status --short
git diff --check
git diff --stat
make test
make lint
make build
helm lint k8s -f k8s/values.yaml
```

Commit all code, tests, migrations, plans/specs, docs, dashboards, and project-local
skill artifacts changed by the implementation. Preserve unrelated user changes.

```bash
git add <explicit reviewed paths>
git commit -m "perf(ui): serve analytics from scalable stats snapshots"
git push origin master
```

### Follow the README release procedure exactly

Read remote Git and Docker tags first; never reuse a tag:

```bash
git fetch --tags origin
git tag -l --sort=-v:refname
git ls-remote --tags origin
```

This is backward-compatible performance work with a schema addition, so use the next
patch version unless implementation introduces a breaking API change. Determine the
actual next version at release time.

```bash
git tag -a <next-version> -m "Release <next-version>"
git push origin <next-version>
make docker-build
make docker-tag TAG=<next-version>
make docker-push TAG=<next-version>
make docker-push TAG=latest
make k8s-release TAG=<next-version>
make -C k8s post-deploy-check
```

### Bootstrap and verify the snapshot

1. Confirm migration `00015` is applied.
2. Resolve the actual refresh CronJob name.
3. Start the one-shot bootstrap job and wait for completion.
4. Inspect only summary logs; do not print the JSON payload.
5. Confirm snapshot age/source count metrics.

### Production route verification

Use bounded requests and record status, TTFB, total time, and bytes:

```bash
for route in dashboard races worlds expansions methodology; do
  curl --fail --silent --show-error --max-time 10 \
    -o "/tmp/ffxiv-${route}.html" \
    -w "${route} status=%{http_code} ttfb=%{time_starttransfer} total=%{time_total} bytes=%{size_download}\n" \
    "https://census.ffxivbard.com/ui/${route}"
done
```

Also verify:

- one world detail page;
- one region world drill-down partial;
- the drill-down still swaps correctly through HTMX and works as a direct HTTP request;
- races filters for region, datacenter, and world;
- `Statistics updated` is present and correct;
- ETag returns 304;
- full-page and HTMX partial cache variants do not collide;
- browser console has no errors and charts render;
- no live raw aggregate SQL appears in `pg_stat_activity` during repeated page loads;
- webserver logs contain no context-canceled query or broken-pipe errors;
- refresh metrics and Grafana panels report expected values;
- ingest workers continue processing normally.

### Rollback trigger and procedure

Rollback if pages are incorrect, snapshot refresh corrupts/fails repeatedly, migrations
prevent startup, or latency targets regress. Deploy the previous image tag through Helm.
Do not run `migrate --direction down`; retain the additive table and column. Re-check
health, UI behavior, and worker throughput after rollback.

## Definition of done

- All acceptance criteria in the design specification pass.
- Strict TDD evidence exists for each production behavior change.
- Full tests, race tests, lint, formatting, build, and Helm lint pass.
- Documentation matches the delivered implementation and operational procedure.
- Changes are committed and pushed.
- A unique release tag is created and pushed.
- ARM64 release and `latest` images are pushed.
- Helm rollout and snapshot bootstrap complete.
- Production routes load within the stated targets and serve correct statistics.
- The final handoff reports the release tag, commit, tests, snapshot generation time,
  source row count, route timings, and any scale limit not directly validated.
