# REST API + Aggregate Queries — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Ship the versioned read API (`GET /api/v1/...`) exposing the ingested census data, plus the aggregate/stats SQL queries and `CensusService` methods it needs, and document every endpoint in the embedded Swagger.

**Architecture:** Aggregate reads are added to the existing repository contracts (`CharacterRepository`, `AchievementRepository`), implemented in `infrastructure/sqlite/repository` (real SQL) and `mock/repository` (in-memory fakes) — the repo's "two adapters per port" rule. `CensusService` (`domain/census/service.go`) gains thin stats methods that call the repos and apply the activity window; it stays tech-agnostic. HTTP controllers in `cmd/http/app/census` resolve `container.Load.CensusService()`/`container.Load.Queue()` at registration and map domain results to response DTOs in `port/dto/response`. Routes use Go 1.22+ method patterns on the existing `http.ServeMux`.

**Tech Stack:** Go 1.25.7 (method-pattern `ServeMux` + `r.PathValue`). Existing: `contract.SQLiteDriver` (`FetchOne`/`FetchMany`/`Execute`), `contract.CharacterRepository`/`AchievementRepository`/`FreeCompanyRepository`/`CensusRunRepository`, `census.Service` (UpsertCharacter/ProcessAchievements/IsActive/SyncMilestones), `mock/repository`, `mock/queue`, swaggo `http-swagger` + embedded `resource/swagger/*`.

**Spec:** `docs/superpowers/specs/2026-08-16-lodestone-census-design.md` §8 (REST API), §3.2 (aggregate queries), §12 (config). This plan implements all 7 spec endpoints.

## Global Constraints

- Go 1.25.7; `CGO_ENABLED=0` cross-compile must keep working (modernc.org/sqlite only).
- Hexagonal: domain/HTTP depend only on `port/contract`; no concrete infra imports outside `container/` and `cmd/`.
- Strict TDD: write the failing test, watch it fail, then implement. No production code without a failing test.
- Every port method gets both adapters: `infrastructure/sqlite/repository/*` and `mock/repository/*`.
- Timestamps stored/compared as TEXT in UTC `"2006-01-02T15:04:05.000Z"` (`repository.timeLayout`). DTOs return `time.Time` (RFC3339 JSON).
- Commit convention: one commit per task.

---

### Task 1: `[census]` config — activity window

**Files:**
- Modify: `config/config.go`, `config/config.toml`
- Test: `config/census_test.go`

**Interfaces:**
- Produces: `Config.Census *CensusConfig` with `CensusConfig{ ActivityWindowDays int }`, read by `container/domain.go` in Task 6.

- [ ] **Step 1: Write the failing test** — `config/census_test.go`:

```go
package config

import (
	"path/filepath"
	"testing"
)

func TestCensusConfig_Defaults(t *testing.T) {
	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.Census == nil || cfg.Census.ActivityWindowDays != 30 {
		t.Fatalf("activity_window_days = %v, want 30", cfg.Census)
	}
}

func TestCensusConfig_EnvOverride(t *testing.T) {
	t.Setenv("CENSUS_ACTIVITY_WINDOW_DAYS", "45")
	cfg, _ := NewConfig()
	if cfg.Census.ActivityWindowDays != 45 {
		t.Fatalf("CENSUS_ACTIVITY_WINDOW_DAYS override: got %v, want 45", cfg.Census.ActivityWindowDays)
	}
}
```

(Remove the unused `filepath` import if the test doesn't need it.)

- [ ] **Step 2: Run to verify it fails** — `go test ./config/ -run TestCensusConfig` → FAIL: `cfg.Census` undefined.

- [ ] **Step 3: Implement** — in `config/config.go`, add to `Config` struct: `Census *CensusConfig \`mapstructure:"census"\``; add type:

```go
type CensusConfig struct {
	ActivityWindowDays int `mapstructure:"activity_window_days"`
}
```

In `config/config.toml`, append:

```toml
[census]
activity_window_days = 30
```

- [ ] **Step 4: Verify pass, commit** — `go test ./config/ -v`; `git add config/`; `git commit -m "feat(config): census activity_window_days"`.

---

### Task 2: Stats indexes migration

**Files:**
- Create: `infrastructure/sqlite/migration/query/00004_census_stats_indexes.sql`

- [ ] **Step 1: Add the migration file** (no test — schema-only, verified by the next binary boot + repo tests):

```sql
-- Indexes for the aggregate/stats queries (active filter on
-- latest_achievement_at, new-per-day on first_seen_at). The group-by columns
-- (race/world/datacenter/region) are already indexed by 00003.

-- +goose Up
-- +goose StatementBegin
CREATE INDEX idx_characters_latest_achievement ON characters (latest_achievement_at);
CREATE INDEX idx_characters_first_seen ON characters (first_seen_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_characters_latest_achievement;
DROP INDEX IF EXISTS idx_characters_first_seen;
-- +goose StatementEnd
```

- [ ] **Step 2: Verify + commit** — `go build ./...`; then `git add infrastructure/sqlite/migration/query/00004_census_stats_indexes.sql`; `git commit -m "feat(sqlite): stats indexes for aggregate queries"`. (Indexes are applied lazily on next `SQLite()` acquire; no correctness dependency.)

---

### Task 3: Contract aggregate types

**Files:**
- Modify: `port/contract/census.go`

- [ ] **Step 1: Add the three types** (additive; no test — pure data types exercised by Tasks 4–6):

```go
// GroupCount is one row of a group-by population aggregate (e.g. per-world).
type GroupCount struct {
	Key    string
	Total  int64
	Active int64
}

// DailyCount is one day's count in a time-series aggregate.
type DailyCount struct {
	Day   string // "2006-01-02"
	Count int64
}

// ExpansionCount is the number of characters who completed an expansion MSQ.
type ExpansionCount struct {
	Expansion string
	Count     int64
}
```

- [ ] **Step 2: Commit** — `git add port/contract/census.go`; `git commit -m "feat(contract): aggregate result types"`.

---

### Task 4: CharacterRepository read methods

**Files:**
- Modify: `port/contract/character_repository.go`, `infrastructure/sqlite/repository/character.go`, `mock/repository/character.go`
- Test: `infrastructure/sqlite/repository/character_test.go`

**Interfaces:**
- Produces (contract methods, must exist on both adapters):

```go
List(ctx context.Context, limit, offset int) ([]CharacterRecord, error)
Count(ctx context.Context) (int64, error)
CountActive(ctx context.Context, since time.Time) (int64, error)
Breakdown(ctx context.Context, column string, since time.Time) ([]GroupCount, error)
NewPerDay(ctx context.Context, since, until time.Time) ([]DailyCount, error)
```

- [ ] **Step 1: Write failing tests** in `infrastructure/sqlite/repository/character_test.go` (reuse `newTestCharacterRepo(t)`). Seed two characters via `Upsert` then assert each method:
  - `TestCharacterRepository_ListPagination` — insert ids 1,2,3; `List(ctx, 2, 0)` → 2 rows ordered by id; `List(ctx, 2, 2)` → 1 row.
  - `TestCharacterRepository_Counts` — 3 upserted, 1 `MarkDeleted`; `Count` → 2; `CountActive(ctx, since)` counts only rows with `LatestAchievementAt`/`last_census` … (set `LatestAchievementAt` via `UpdateAchievementSummary(ctx, id, false, &lid, &lat)`); assert active count.
  - `TestCharacterRepository_Breakdown` — characters with known worlds; `Breakdown(ctx, "world", since)` → per-world `{Key, Total, Active}`.
  - `TestCharacterRepository_NewPerDay` — characters with distinct `FirstSeenAt` days; `NewPerDay` → per-day counts ordered ascending.

- [ ] **Step 2: Run to verify they fail** — `go test ./infrastructure/sqlite/repository/ -run 'TestCharacterRepository_(List|Counts|Breakdown|NewPerDay)'` → FAIL: methods undefined.

- [ ] **Step 3: Implement SQL** in `infrastructure/sqlite/repository/character.go` (reuse `characterColumns`, `scanCharacter`, `formatTime`). Column whitelist for `Breakdown`:

```go
var breakdownColumns = map[string]bool{"race": true, "world": true, "datacenter": true, "region": true}
```

```go
func (r *CharacterRepository) List(ctx context.Context, limit, offset int) ([]contract.CharacterRecord, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT `+characterColumns+` FROM characters WHERE deleted_at IS NULL ORDER BY id LIMIT ? OFFSET ?`,
		limit, offset)
	// scan loop identical to ListStale's, appending *rec
}

func (r *CharacterRepository) Count(ctx context.Context) (int64, error) {
	row, err := r.driver.FetchOne(ctx, `SELECT COUNT(*) FROM characters WHERE deleted_at IS NULL`)
	// Scan(&n); return n
}

func (r *CharacterRepository) CountActive(ctx context.Context, since time.Time) (int64, error) {
	row, err := r.driver.FetchOne(ctx,
		`SELECT COUNT(*) FROM characters WHERE deleted_at IS NULL AND latest_achievement_at >= ?`,
		formatTime(since))
	// Scan(&n)
}

func (r *CharacterRepository) Breakdown(ctx context.Context, column string, since time.Time) ([]contract.GroupCount, error) {
	if !breakdownColumns[column] {
		return nil, fmt.Errorf("invalid breakdown column %q", column)
	}
	rows, err := r.driver.FetchMany(ctx,
		`SELECT `+column+`, COUNT(*),
		        SUM(CASE WHEN latest_achievement_at >= ? THEN 1 ELSE 0 END)
		   FROM characters WHERE deleted_at IS NULL
		  GROUP BY `+column+` ORDER BY COUNT(*) DESC`,
		formatTime(since))
	// scan Key(string), Total(int64), Active(int64)
}

func (r *CharacterRepository) NewPerDay(ctx context.Context, since, until time.Time) ([]contract.DailyCount, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT substr(first_seen_at, 1, 10), COUNT(*)
		   FROM characters
		  WHERE deleted_at IS NULL AND first_seen_at >= ? AND first_seen_at < ?
		  GROUP BY substr(first_seen_at, 1, 10) ORDER BY 1`,
		formatTime(since), formatTime(until))
	// scan Day(string), Count(int64)
}
```

(Use `row.Scan(&n)` on the `*sql.Row` returned by `FetchOne`; `List`/`Breakdown`/`NewPerDay` use the `FetchMany` → `rows.Scan` pattern already in `ListStale`/`GetJobs`.)

- [ ] **Step 4: Implement the mock** in `mock/repository/character.go` — mirror the SQL semantics over the existing `characters`/`jobs` maps (derive active from `LatestAchievementAt`; group by the record field; day from `FirstSeenAt.Format("2006-01-02")`). Return slices sorted by key/count as the SQL does.

- [ ] **Step 5: Verify pass, commit** — `go test ./infrastructure/sqlite/repository/ -v`; `go build ./...` (mock must satisfy the interface); `git add port/contract/character_repository.go infrastructure/sqlite/repository/character.go mock/repository/character.go infrastructure/sqlite/repository/character_test.go`; `git commit -m "feat(repo): character list/count/breakdown/new-per-day reads"`.

---

### Task 5: AchievementRepository.CountExpansions

**Files:**
- Modify: `port/contract/achievement_repository.go`, `infrastructure/sqlite/repository/achievement.go`, `mock/repository/achievement.go`
- Test: `infrastructure/sqlite/repository/achievement_test.go`

**Interfaces:**
- Produces: `CountExpansions(ctx context.Context) ([]ExpansionCount, error)` on `contract.AchievementRepository`.

- [ ] **Step 1: Write failing test** — seed `milestone_achievements` (SyncMilestones with two `expansion_msq` entries, e.g. Heavensward id 1139, Stormblood id 1794) and `character_milestones` (UpsertCharacterMilestones for characters 1 and 2); assert `CountExpansions` returns `[{Heavensward, n}, {Stormblood, m}]` with `COUNT(DISTINCT character_id)`.

- [ ] **Step 2: Run to verify fails** — `go test ./infrastructure/sqlite/repository/ -run CountExpansions` → FAIL.

- [ ] **Step 3: Implement SQL** in `infrastructure/sqlite/repository/achievement.go`:

```go
func (r *AchievementRepository) CountExpansions(ctx context.Context) ([]contract.ExpansionCount, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT ma.expansion, COUNT(DISTINCT cm.character_id)
		   FROM character_milestones cm
		   JOIN milestone_achievements ma ON ma.achievement_id = cm.achievement_id
		  WHERE ma.kind = 'expansion_msq' AND ma.expansion IS NOT NULL
		  GROUP BY ma.expansion ORDER BY ma.expansion`)
	// scan Expansion(string), Count(int64)
}
```

- [ ] **Step 4: Implement the mock** in `mock/repository/achievement.go` — join the fake's milestone/achievement maps with the same DISTINCT-characters semantics.

- [ ] **Step 5: Verify pass, commit** — `go test ./infrastructure/sqlite/repository/ -v`; `go build ./...`; commit `feat(repo): expansion completion counts`.

---

### Task 6: CensusService aggregate methods + activity window

**Files:**
- Modify: `domain/census/service.go`
- Test: `domain/census/service_test.go`

**Interfaces:**
- Produces (all methods on `*Service`; existing `NewService(chars, fcs, ach, runs)` signature unchanged):

```go
func (s *Service) SetActivityWindow(d time.Duration)              // no-op when d <= 0
func (s *Service) Summary(ctx context.Context) (total, active int64, err error)
func (s *Service) ListCharacters(ctx context.Context, limit, offset int) ([]contract.CharacterRecord, int64, error)
func (s *Service) CharacterDetail(ctx context.Context, id uint32) (*CharacterDetail, error)
func (s *Service) Breakdown(ctx context.Context, by string) ([]contract.GroupCount, error)
func (s *Service) NewCharacters(ctx context.Context, since, until time.Time) ([]contract.DailyCount, error)
func (s *Service) ExpansionCompletions(ctx context.Context) ([]contract.ExpansionCount, error)

var ErrInvalidDimension = errors.New("invalid breakdown dimension: want race|world|datacenter|region")

type CharacterDetail struct {
	Character   contract.CharacterRecord
	Jobs        []contract.ClassJobRecord
	Milestones  []contract.CharacterMilestone
	FreeCompany *contract.FreeCompanyRecord
}
```

- [ ] **Step 1: Write failing tests** in `domain/census/service_test.go` (reuse `newTestService(t)`; mock fakes now have the read methods from Tasks 4–5). Assert: `Summary` totals; `ListCharacters` pagination + total; `CharacterDetail` (missing id → `nil, nil`; with FC → FreeCompany populated); `Breakdown` invalid `by` → `errors.Is(err, ErrInvalidDimension)`; `NewCharacters`; `ExpansionCompletions`.

- [ ] **Step 2: Run to verify fails** — `go test ./domain/census/ -run 'TestService_(Summary|ListCharacters|CharacterDetail|Breakdown|NewCharacters|ExpansionCompletions)'` → FAIL.

- [ ] **Step 3: Implement** in `domain/census/service.go`:

Add field `activityWindow time.Duration` to `Service`; set it to `defaultActivityWindow` in `NewService`. Add:

```go
func (s *Service) SetActivityWindow(d time.Duration) {
	if d > 0 {
		s.activityWindow = d
	}
}

func (s *Service) activitySince() time.Time {
	return time.Now().UTC().Add(-s.activityWindow)
}
```

Change `IsActive` to use `s.activityWindow` instead of the `defaultActivityWindow` const (keep the const as the default). Implement the six methods by delegating to repos:
- `Summary` → `s.characters.Count(ctx)` + `s.characters.CountActive(ctx, s.activitySince())`.
- `ListCharacters` → `s.characters.List(...)` + `s.characters.Count(...)`.
- `CharacterDetail` → `s.characters.Get` (nil → `nil, nil`), `GetJobs`, `s.achievements.ListCharacterMilestones`, and when `rec.FreeCompanyID != nil` → `s.freeCompanies.Get(*rec.FreeCompanyID)` (missing FC → leave `FreeCompany` nil).
- `Breakdown` → validate `by` against `{race, world, datacenter, region}` → return `ErrInvalidDimension`; else `s.characters.Breakdown(by, s.activitySince())`.
- `NewCharacters` → `s.characters.NewPerDay(since, until)`.
- `ExpansionCompletions` → `s.achievements.CountExpansions(ctx)`.

- [ ] **Step 4: Wire config** in `container/domain.go` `CensusService()` — after `census.NewService(...)`, add (import `time`):

```go
	if c := s.Config().Census; c != nil && c.ActivityWindowDays > 0 {
		svc.SetActivityWindow(time.Duration(c.ActivityWindowDays) * 24 * time.Hour)
	}
```

- [ ] **Step 5: Verify pass, commit** — `go test ./domain/census/ -v`; `go build ./...`; commit `feat(census): aggregate stats methods and configurable activity window`.

---

### Task 7: Response DTOs

**Files:**
- Create: `port/dto/response/census.go`

- [ ] **Step 1: Add DTO structs** (data-only; exercised by Task 8). Field names are the API contract — do not rename:

```go
package response

import "time"

type CensusSummary struct {
	TotalCharacters  int64   `json:"total_characters"`
	ActiveCharacters int64   `json:"active_characters"`
	ActiveRatio      float64 `json:"active_ratio"`
}

type CharacterListItem struct {
	ID                  uint32   `json:"id"`
	Name                string   `json:"name"`
	World               string   `json:"world"`
	Datacenter          string   `json:"datacenter"`
	Region              string   `json:"region"`
	Race                string   `json:"race"`
	Gender              uint8    `json:"gender"`
	FreeCompanyID       *string  `json:"free_company_id,omitempty"`
	FreeCompanyName     *string  `json:"free_company_name,omitempty"`
	AchievementsPrivate bool     `json:"achievements_private"`
	LatestAchievementID *uint32  `json:"latest_achievement_id,omitempty"`
	FirstSeenAt         time.Time `json:"first_seen_at"`
	LastCensusAt        *time.Time `json:"last_census_at,omitempty"`
}

type PaginatedCharacters struct {
	Items  []CharacterListItem `json:"items"`
	Total  int64               `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

type CharacterDetail struct {
	Character   CharacterListItem        `json:"character"`
	Jobs        []CharacterJobDetail     `json:"jobs"`
	Milestones  []CharacterMilestoneDetail `json:"milestones"`
	FreeCompany *FreeCompanyDetail       `json:"free_company,omitempty"`
}

type CharacterJobDetail struct {
	ClassJobID uint8  `json:"class_job_id"`
	Name       string `json:"name"`
	Level      uint8  `json:"level"`
	ExpLevel   uint32 `json:"exp_level"`
}

type CharacterMilestoneDetail struct {
	AchievementID uint32    `json:"achievement_id"`
	AchievedAt    time.Time `json:"achieved_at"`
}

type FreeCompanyDetail struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	World       string     `json:"world"`
	Datacenter  string     `json:"datacenter"`
	MemberCount uint32     `json:"member_count"`
}

type BreakdownGroup struct {
	Key    string `json:"key"`
	Total  int64  `json:"total"`
	Active int64  `json:"active"`
}

type NewCharactersDay struct {
	Day   string `json:"day"`
	Count int64  `json:"count"`
}

type ExpansionStat struct {
	Expansion string `json:"expansion"`
	Count     int64  `json:"count"`
}

type QueueDepthItem struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}
```

- [ ] **Step 2: Commit** — `git add port/dto/response/census.go`; `git commit -m "feat(dto): census API response DTOs"`.

---

### Task 8: HTTP controllers, routes, and wiring

**Files:**
- Create: `cmd/http/app/census/handler/json.go`, `cmd/http/app/census/handler/census.go`, `cmd/http/app/census/handler/queue.go`, `cmd/http/app/census/routes.go`
- Modify: `cmd/http/router.go`
- Test: `cmd/http/app/census/handler/census_test.go`

**Interfaces:**
- Consumes: `*census.Service` (Task 6 methods), `contract.Queue.Depth()` (existing), `port/dto/response` (Task 7).
- Produces: `census.Register(mux *http.ServeMux)`.

- [ ] **Step 1: Write failing httptest** in `census_test.go` — build `svc := census.NewService(mockrepo.NewCharacterFake(), mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())` + `q := mockqueue.NewFake()`; construct controllers; `httptest.NewRequest("GET", "/api/v1/census/latest", nil)` + recorder; assert 200 + `total_characters` key. Repeat for `/api/v1/queue` (assert `status`/`count`). Use a stub service built on fakes (seed via `svc.UpsertCharacter`).

- [ ] **Step 2: Run to verify fails** — `go test ./cmd/http/app/census/...` → FAIL: package undefined.

- [ ] **Step 3: Implement** — `json.go`:

```go
package handler

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
```

`census.go` — `CensusController{ svc *census.Service }`, `NewCensusController(svc *census.Service) *CensusController`. Each method resolves `ctx := r.Context()`; when `c.svc == nil` return `writeError(w, 500, "census service unavailable")`. Methods + param rules:

- `Latest` — `total, active, err := c.svc.Summary(ctx)`; ratio = 0 when total == 0 else `float64(active)/float64(total)`.
- `List` — parse `limit` (default 100, clamp ≤ 500) and `offset` (default 0) via `strconv.Atoi`; invalid/negative → 400.
- `Get` — `r.PathValue("id")` → `strconv.ParseUint(id, 10, 32)` → `uint32`; `CharacterDetail` nil → 404 `{"error":"character not found"}`.
- `Breakdown` — `by := r.URL.Query().Get("by")`; empty → 400; `svc.Breakdown(by)`; `errors.Is(err, census.ErrInvalidDimension)` → 400.
- `NewCharacters` — `since` (required, `time.Parse("2006-01-02", ...)`, UTC), `until` (optional, default `time.Now().UTC()`); parse error → 400.
- `Expansion` — `name := r.URL.Query().Get("name")`; call `svc.ExpansionCompletions`; when `name != ""` filter the slice to `name` (empty result OK, 200).

Each maps domain results to the Task 7 DTOs (e.g. `CharacterRecord` → `CharacterListItem` copying each field; `ClassJobRecord` → `CharacterJobDetail`; `CharacterMilestone` → `CharacterMilestoneDetail`). All DB errors → 500 `{"error": ...}`.

`queue.go` — `QueueController{ q contract.Queue }`, `NewQueueController(q contract.Queue) *QueueController`; `Depth` → `q.Depth(ctx)` → `[]response.QueueDepthItem` sorted by status string; nil queue → 500.

`routes.go`:

```go
package census

func Register(mux *http.ServeMux) {
	svc := container.Load.CensusService()
	q := container.Load.Queue()
	c := handler.NewCensusController(svc)
	qc := handler.NewQueueController(q)
	mux.HandleFunc("GET /api/v1/census/latest", c.Latest)
	mux.HandleFunc("GET /api/v1/census/characters", c.List)
	mux.HandleFunc("GET /api/v1/census/characters/{id}", c.Get)
	mux.HandleFunc("GET /api/v1/stats/breakdown", c.Breakdown)
	mux.HandleFunc("GET /api/v1/stats/new-characters", c.NewCharacters)
	mux.HandleFunc("GET /api/v1/stats/expansion", c.Expansion)
	mux.HandleFunc("GET /api/v1/queue", qc.Depth)
}
```

`cmd/http/router.go` — add `census "github.com/mihaiflorentin88/ffxiv-census/cmd/http/app/census"` import and call `census.Register(mux)` in `RegisterRoutes` (alongside `ui.Register(mux)`).

- [ ] **Step 4: Verify pass, commit** — `go test ./cmd/http/app/census/... -v`; `go build ./...`; `git add cmd/http/app/census cmd/http/router.go`; `git commit -m "feat(http): census REST API controllers and routes"`.

---

### Task 9: Swagger documentation

**Files:**
- Modify: `cmd/http/resource/swagger/swagger.json`, `cmd/http/resource/swagger/swagger.yaml`, `cmd/http/resource/swagger/docs.go`

- [ ] **Step 1: Rewrite `swagger.json`** to document all 7 paths + schemas. Keep `swagger: "2.0"`, `info.title "ffxiv-census API"`, `version "1.0"`. Add `paths` entries with `get`, `produces: ["application/json"]`, `parameters` (query: `limit`/`offset` int; `by` enum `[race,world,datacenter,region]`; `since`/`until` string date; `name` string; path `{id}` integer), and `responses` `200` referencing `definitions` for `CensusSummary`, `PaginatedCharacters`, `CharacterDetail`, `BreakdownGroup`, `NewCharactersDay`, `ExpansionStat`, `QueueDepthItem` (fields mirror the Task 7 JSON tags). Remove the `/example` placeholder path. Keep `/health`.

- [ ] **Step 2: Mirror in `swagger.yaml`** (same content, YAML form) and update `docs.go` `docTemplate` to the same JSON string (it is registered via `swag.Register`).

- [ ] **Step 3: Verify + commit** — `go build ./...` (embed must compile); `git add cmd/http/resource/swagger/`; `git commit -m "docs(swagger): document /api/v1 endpoints"`.

---

### Task 10: Docs

**Files:**
- Create: `docs/http-api.md`
- Modify: `docs/census.md`

- [ ] **Step 1: `docs/http-api.md`** — endpoint table (method, path, query/path params, success shape, error shape), the pagination envelope `{items, total, limit, offset}`, the JSON error convention `{"error": "..."}`, and the activity-window semantics (`[census] activity_window_days`, active = `latest_achievement_at` within window).

- [ ] **Step 2: `docs/census.md`** — remove "Aggregate/stats queries" from the "Not yet implemented (later phases)" list (leave FC member-list re-census), and add the new `CensusService` methods to its method list.

- [ ] **Step 3: Commit** — `git add docs/http-api.md docs/census.md`; `git commit -m "docs: REST API reference"`.

---

## Critical Files & Anchors

- `port/contract/character_repository.go` — add 5 read methods; exact signatures in Task 4.
- `infrastructure/sqlite/repository/character.go` — `characterColumns`, `scanCharacter`, `formatTime`, and the `ListStale`/`GetJobs` row-scan pattern to copy for the new reads.
- `domain/census/service.go` — `defaultActivityWindow` (line ~109), `IsActive` (~175), `NewService` constructor; add `activityWindow` field + Task 6 methods here.
- `container/domain.go` — `CensusService()` accessor; add the `SetActivityWindow` wiring (Task 6 Step 4).
- `cmd/http/router.go` — `RegisterRoutes`; mount `census.Register(mux)` (Task 8).

## Verification

1. Build/typecheck: `go build ./...`.
2. Unit/integration: `go test ./... -race` (SQLite repos test against temp-file DBs; handler tests use mocks + httptest).
3. Lint: `PATH="$HOME/go/bin:$PATH" make lint` (golangci-lint lives in `~/go/bin`, not on PATH).
4. End-to-end smoke (from repo root), network available:

```bash
make build
DB=$(mktemp -d)/census.db
SQLITE_PATH=$DB ./bin/ffxiv-census publish id-sweep --from 36795950 --to 36795952 --chunk-size 3
SQLITE_PATH=$DB timeout 30 ./bin/ffxiv-census consume id-sweep --concurrency 1   # ingests 3 characters
SQLITE_PATH=$DB timeout 30 ./bin/ffxiv-census consume achievement-census --concurrency 1  # sets latest_achievement_at
SQLITE_PATH=$DB ./bin/ffxiv-census server --start --port 18080 &   # then:
curl -s localhost:18080/api/v1/census/latest          # -> {"total_characters":N,"active_characters":M,"active_ratio":R}
curl -s 'localhost:18080/api/v1/census/characters?limit=2&offset=0'  # -> {"items":[...],"total":N,"limit":2,"offset":0}
curl -s localhost:18080/api/v1/census/characters/36795950           # -> detail with jobs[]/milestones[]/free_company
curl -s 'localhost:18080/api/v1/stats/breakdown?by=world'            # -> [{"key":"Louisoix","total":..,"active":..},...]
curl -s 'localhost:18080/api/v1/stats/new-characters?since=2020-01-01'  # -> [{"day":"2026-08-17","count":N}]
curl -s 'localhost:18080/api/v1/stats/expansion'                     # -> [{"expansion":"...","count":N},...]
curl -s localhost:18080/api/v1/queue                                # -> [{"status":"done","count":N},...]
curl -s localhost:18080/docs/swagger.json | jq '.paths | keys'      # -> all 7 /api/v1 paths
```

Expected: every `curl` returns JSON with the keys shown (exact counts vary with the seed); `/api/v1/queue` returns `done`/`pending` counts reflecting the completed consume run. Kill the server with Ctrl-C.

## Assumptions & Contingencies

- **Activity window is `[census] activity_window_days` (default 30).** If the user later wants separate windows for different stats, add a second key then — this phase uses one window for "active" everywhere (spec has a single `[census] activity window days`).
- **`/stats/expansion` filters by optional `name`; no match returns `[]`, not 404.** If a 404 is preferred, change `c.Expansion` to return 404 on a non-empty name with no match — no other code changes.
- **`SetActivityWindow` (not a constructor param)** keeps the existing 4-arg `NewService` and its ~12 test call sites unchanged. If the reviewer prefers a constructor option, that is a mechanical signature change — do not do it unless asked.
- **`Breakdown` column whitelist** is enforced in both the service (`ErrInvalidDimension` → 400) and the SQL repo (defense-in-depth); the repo rejects unknown columns with a plain error.
- **Live smoke needs Lodestone reachability** to seed real characters. If the network is down during execution, seed via `sqlite3` INSERTs into `characters`/`character_jobs`/`milestone_achievements`/`character_milestones` using the schema in `docs/sqlite.md`, then run the same curls; the API behavior does not depend on the seed source.
