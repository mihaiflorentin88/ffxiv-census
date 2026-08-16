# Census Data Model + Repositories — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist the census domain in SQLite: schema for characters, jobs, milestones, free companies, and census runs, plus the repository contracts, SQLite implementations, and mocks that the domain service and ingest handlers (next phase) will consume.

**Architecture:** Hexagonal, following the repo's existing patterns. Records (DTOs) live alongside their interfaces in `port/contract`. SQLite implementations live in `infrastructure/sqlite/repository/` and satisfy `contract` interfaces via the `SQLiteDriver` (transactions use `driver.Acquire` + `BeginTx`, matching `infrastructure/queue`). Every contract gets a hand-written fake in `mock/`.

**Tech Stack:** Go 1.25, modernc.org/sqlite (via existing `SQLiteDriver`), goose migrations. Spec: `docs/superpowers/specs/2026-08-16-lodestone-census-design.md`.

**Timestamps:** store TEXT in UTC format `"2006-01-02T15:04:05.000Z"` — identical to the existing `queue_jobs` convention (`infrastructure/queue/queue.go:16`). A shared helper file centralizes format/parse.

**Verification commands:** `go test ./...`, `go build ./...`, `make lint`. Run from repo root. `golangci-lint` lives at `~/go/bin` (add `PATH="$HOME/go/bin:$PATH"` if `make lint` can't find it).

**Commit convention:** one commit per task (small, incremental, push-capable). Message style matches repo history (`feat(...)`, `chore:`, `test(...)`, `docs:`).

---

## File Map

```
port/contract/census.go                  # records: CharacterRecord, ClassJobRecord, MilestoneAchievement, CharacterMilestone, FreeCompanyRecord, CensusRun
port/contract/character_repository.go    # CharacterRepository interface
port/contract/free_company_repository.go # FreeCompanyRepository interface
port/contract/achievement_repository.go  # AchievementRepository interface
port/contract/census_run_repository.go   # CensusRunRepository interface

infrastructure/sqlite/repository/time.go         # shared timeLayout + format/parse helpers
infrastructure/sqlite/repository/character.go    # CharacterRepository impl
infrastructure/sqlite/repository/free_company.go # FreeCompanyRepository impl
infrastructure/sqlite/repository/achievement.go  # AchievementRepository impl
infrastructure/sqlite/repository/census_run.go   # CensusRunRepository impl
infrastructure/sqlite/repository/*_test.go       # temp-file DB tests (real SQL)

mock/repository/character.go     # in-memory fake
mock/repository/free_company.go
mock/repository/achievement.go
mock/repository/census_run.go

infrastructure/sqlite/migration/query/00003_create_census_tables.sql

container/infrastructure.go      # add 4 repository accessors
container/census_repository_test.go

docs/census.md
```

---

### Task 1: Census schema migration

**Files:**
- Create: `infrastructure/sqlite/migration/query/00003_create_census_tables.sql`

- [ ] **Step 1: Write the migration**

```sql
-- Census domain tables.
-- characters.id is the Lodestone character ID (externally assigned, no AUTOINCREMENT).
-- Timestamps are TEXT in UTC "2006-01-02T15:04:05.000Z" (same convention as queue_jobs).
-- A character discovered by the id-sweep but not yet fully fetched has name = '' and
-- last_census_at = NULL ("unverified"); a full census sets both.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE characters (
    id                    INTEGER PRIMARY KEY,
    name                  TEXT    NOT NULL DEFAULT '',
    world                 TEXT    NOT NULL DEFAULT '',
    datacenter            TEXT    NOT NULL DEFAULT '',
    region                TEXT    NOT NULL DEFAULT '',
    race                  TEXT,
    tribe                 TEXT,
    gender                INTEGER NOT NULL DEFAULT 0,
    grand_company         TEXT,
    fc_id                 TEXT,
    fc_name               TEXT,
    achievements_private  INTEGER NOT NULL DEFAULT 0,
    latest_achievement_id INTEGER,
    latest_achievement_at TEXT,
    first_seen_at         TEXT    NOT NULL,
    last_census_at        TEXT,
    deleted_at            TEXT
);

CREATE INDEX idx_characters_region     ON characters (region);
CREATE INDEX idx_characters_world      ON characters (world);
CREATE INDEX idx_characters_datacenter ON characters (datacenter);
CREATE INDEX idx_characters_race       ON characters (race);
CREATE INDEX idx_characters_fc         ON characters (fc_id);
CREATE INDEX idx_characters_last_census ON characters (last_census_at);

CREATE TABLE character_jobs (
    character_id INTEGER NOT NULL,
    class_job_id INTEGER NOT NULL,
    name         TEXT    NOT NULL,
    level        INTEGER NOT NULL,
    exp_level    INTEGER NOT NULL,
    PRIMARY KEY (character_id, class_job_id)
);

CREATE TABLE milestone_achievements (
    achievement_id INTEGER PRIMARY KEY,
    kind           TEXT    NOT NULL,
    expansion      TEXT,
    detail         TEXT    NOT NULL
);

CREATE TABLE character_milestones (
    character_id   INTEGER NOT NULL,
    achievement_id INTEGER NOT NULL,
    achieved_at    TEXT    NOT NULL,
    PRIMARY KEY (character_id, achievement_id)
);

CREATE TABLE free_companies (
    id           TEXT    PRIMARY KEY,
    name         TEXT    NOT NULL,
    world        TEXT    NOT NULL DEFAULT '',
    datacenter   TEXT    NOT NULL DEFAULT '',
    member_count INTEGER NOT NULL DEFAULT 0,
    formed_at    TEXT,
    last_seen_at TEXT    NOT NULL
);

CREATE TABLE census_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at      TEXT    NOT NULL,
    finished_at     TEXT,
    characters_seen INTEGER NOT NULL DEFAULT 0,
    new_characters  INTEGER NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS census_runs;
DROP TABLE IF EXISTS free_companies;
DROP TABLE IF EXISTS character_milestones;
DROP TABLE IF EXISTS milestone_achievements;
DROP TABLE IF EXISTS character_jobs;
DROP TABLE IF EXISTS characters;
-- +goose StatementEnd
```

- [ ] **Step 2: Verify migration applies and rolls back**

```bash
rm -f data/ffxiv-census.db
./bin/ffxiv-census migrate --direction up
sqlite3 data/ffxiv-census.db ".tables"
./bin/ffxiv-census migrate --direction down
```

Expected: `up` lists the 6 census tables (+ `goose_db_version`, `queue_jobs`); `down` exits 0 and drops them. (If `sqlite3` CLI is unavailable, verify with `go test` in Task 2 instead.)

- [ ] **Step 3: Commit**

```bash
git add infrastructure/sqlite/migration/query/00003_create_census_tables.sql
git commit -m "feat(schema): census tables (characters, jobs, milestones, fcs, runs)"
```

---

### Task 2: Domain records (`port/contract/census.go`)

**Files:**
- Create: `port/contract/census.go`
- Test: `port/contract/census_test.go` (compile-time/JSON-safety checks)

- [ ] **Step 1: Write the records**

```go
package contract

import "time"

// CharacterRecord is the persisted census snapshot of a character.
// ID is the Lodestone character ID. FreeCompanyID/Name are nil when the
// character is not in an FC. LatestAchievementID/At are nil until an
// achievement census has run. DeletedAt is non-nil once a 404 confirms the
// character no longer exists.
type CharacterRecord struct {
	ID                   uint32
	Name                 string
	World                string
	Datacenter           string
	Region               string
	Race                 string
	Tribe                string
	Gender               uint8
	GrandCompany         string
	FreeCompanyID        *string
	FreeCompanyName      *string
	AchievementsPrivate  bool
	LatestAchievementID  *uint32
	LatestAchievementAt  *time.Time
	FirstSeenAt          time.Time
	LastCensusAt         *time.Time
	DeletedAt            *time.Time
}

// ClassJobRecord is one job/class level snapshot for a character.
type ClassJobRecord struct {
	CharacterID uint32
	ClassJobID  uint8
	Name        string
	Level       uint8
	ExpLevel    uint32
}

// MilestoneKind classifies a tracked achievement. Values are validated on
// write (see AchievementRepository.SyncMilestones).
const (
	MilestoneKindExpansion string = "expansion_msq"
	MilestoneKindJobLevel  string = "job_level"
	MilestoneKindChocobo   string = "chocobo"
)

// MilestoneAchievement is a registry entry describing an achievement we track.
// Expansion is non-nil for expansion_msq milestones (e.g. "Heavensward").
type MilestoneAchievement struct {
	AchievementID uint32
	Kind          string
	Expansion     *string
	Detail        string
}

// CharacterMilestone is an achievement a character has earned that matches a
// registered milestone.
type CharacterMilestone struct {
	CharacterID   uint32
	AchievementID uint32
	AchievedAt    time.Time
}

// FreeCompanyRecord is the persisted snapshot of a free company. ID is the
// Lodestone FC ID string (19 digits), not a numeric character ID.
type FreeCompanyRecord struct {
	ID          string
	Name        string
	World       string
	Datacenter   string
	MemberCount uint32
	FormedAt    *time.Time
	LastSeenAt  time.Time
}

// CensusRun is one census sweep for operational tracking.
type CensusRun struct {
	ID             int64
	StartedAt      time.Time
	FinishedAt     *time.Time
	CharactersSeen int
	NewCharacters  int
}
```

- [ ] **Step 2: Write a light compile/consistency test**

`port/contract/census_test.go`:

```go
package contract

import "testing"

func TestMilestoneKindConstants(t *testing.T) {
	if MilestoneKindExpansion != "expansion_msq" {
		t.Errorf("expansion kind = %q", MilestoneKindExpansion)
	}
	if MilestoneKindJobLevel != "job_level" {
		t.Errorf("job level kind = %q", MilestoneKindJobLevel)
	}
	if MilestoneKindChocobo != "chocobo" {
		t.Errorf("chocobo kind = %q", MilestoneKindChocobo)
	}
}
```

- [ ] **Step 3: Run tests to verify they pass**

```bash
go test ./port/contract/ -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add port/contract/census.go port/contract/census_test.go
git commit -m "feat(contract): census domain records"
```

---

### Task 3: CharacterRepository

**Files:**
- Create: `port/contract/character_repository.go`
- Create: `infrastructure/sqlite/repository/time.go`
- Create: `infrastructure/sqlite/repository/character.go`
- Create: `mock/repository/character.go`
- Test: `infrastructure/sqlite/repository/character_test.go`

- [ ] **Step 1: Write the contract**

`port/contract/character_repository.go`:

```go
package contract

import (
	"context"
	"time"
)

// CharacterRepository persists character snapshots and their job levels.
type CharacterRepository interface {
	// Upsert replaces the character row and its jobs in one transaction.
	// first_seen_at is preserved on conflict; deleted_at is cleared.
	Upsert(ctx context.Context, rec CharacterRecord, jobs []ClassJobRecord) error
	// Get returns the character, or nil (no error) if not found.
	Get(ctx context.Context, id uint32) (*CharacterRecord, error)
	// GetJobs returns the character's job levels (empty if none).
	GetJobs(ctx context.Context, id uint32) ([]ClassJobRecord, error)
	// MarkDeleted records that the character no longer exists on Lodestone.
	MarkDeleted(ctx context.Context, id uint32, at time.Time) error
	// UpdateAchievementSummary sets achievements_private, latest_achievement_id,
	// and latest_achievement_at for a character (after an achievement census).
	UpdateAchievementSummary(ctx context.Context, id uint32, private bool, latestID *uint32, latestAt *time.Time) error
	// ListStale returns up to limit characters whose last_census_at is before
	// cutoff (NULL last_census_at counts as stale), ordered oldest-first.
	ListStale(ctx context.Context, cutoff time.Time, limit int) ([]CharacterRecord, error)
}
```

- [ ] **Step 2: Write the shared time helper**

`infrastructure/sqlite/repository/time.go`:

```go
package repository

import "time"

// timeLayout matches the queue_jobs TEXT timestamp convention and SQLite's
// strftime('%Y-%m-%dT%H:%M:%fZ','now').
const timeLayout = "2006-01-02T15:04:05.000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }
```

- [ ] **Step 3: Write the failing tests**

`infrastructure/sqlite/repository/character_test.go`:

```go
package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite"
	sqlitemigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/migration"
)

func newTestRepo(t *testing.T) (contract.CharacterRepository, func()) {
	t.Helper()
	cfg := &config.SQLiteConfig{
		Path:         filepath.Join(t.TempDir(), "test.db"),
		MaxOpenConns: 2,
		MaxIdleConns: 2,
		BusyTimeout:  "2s",
		JournalMode:  "WAL",
	}
	driver, err := sqlite.NewDriver(cfg, sqlitemigration.FS())
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	return NewCharacterRepository(driver), func() { _ = driver.Close() }
}

func strPtr(s string) *string { return &s }

func TestCharacterRepository_UpsertAndGet(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	fc := "9234567890123456789"
	rec := contract.CharacterRecord{
		ID:          12345678,
		Name:        "Tataru Taru",
		World:       "Ultros",
		Datacenter:  "Primal",
		Region:      "NA",
		Race:        "Lalafell",
		Tribe:       "Dunesfolk",
		Gender:      2,
		GrandCompany: "Maelstrom",
		FreeCompanyID: &fc,
		FirstSeenAt: now,
	}
	jobs := []contract.ClassJobRecord{
		{CharacterID: 12345678, ClassJobID: 1, Name: "Gladiator", Level: 90, ExpLevel: 0},
		{CharacterID: 12345678, ClassJobID: 19, Name: "Paladin", Level: 90, ExpLevel: 12345},
	}

	if err := repo.Upsert(context.Background(), rec, jobs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.Get(context.Background(), 12345678)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected character, got nil")
	}
	if got.Name != "Tataru Taru" || got.Region != "NA" || got.FreeCompanyID == nil || *got.FreeCompanyID != fc {
		t.Errorf("got %+v", got)
	}

	gotJobs, err := repo.GetJobs(context.Background(), 12345678)
	if err != nil {
		t.Fatalf("GetJobs: %v", err)
	}
	if len(gotJobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(gotJobs))
	}
}

func TestCharacterRepository_GetNotFound(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	got, err := repo.Get(context.Background(), 99999999)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing character, got %+v", got)
	}
}

func TestCharacterRepository_MarkDeleted(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	rec := contract.CharacterRecord{ID: 111, Name: "X", FirstSeenAt: now}
	if err := repo.Upsert(context.Background(), rec, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	deletedAt := now.Add(time.Hour)
	if err := repo.MarkDeleted(context.Background(), 111, deletedAt); err != nil {
		t.Fatalf("MarkDeleted: %v", err)
	}
	got, _ := repo.Get(context.Background(), 111)
	if got.DeletedAt == nil || !got.DeletedAt.Equal(deletedAt) {
		t.Errorf("deleted_at = %v, want %v", got.DeletedAt, deletedAt)
	}
}

func TestCharacterRepository_UpdateAchievementSummary(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	rec := contract.CharacterRecord{ID: 222, Name: "Y", FirstSeenAt: now}
	if err := repo.Upsert(context.Background(), rec, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	latest := uint32(590)
	latestAt := now.Add(time.Hour)
	if err := repo.UpdateAchievementSummary(context.Background(), 222, true, &latest, &latestAt); err != nil {
		t.Fatalf("UpdateAchievementSummary: %v", err)
	}
	got, _ := repo.Get(context.Background(), 222)
	if !got.AchievementsPrivate {
		t.Error("achievements_private = false, want true")
	}
	if got.LatestAchievementID == nil || *got.LatestAchievementID != 590 {
		t.Errorf("latest_achievement_id = %v, want 590", got.LatestAchievementID)
	}
}

func TestCharacterRepository_ListStale(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	old := now.Add(-48 * time.Hour)
	// stale: last_census_at far in the past
	if err := repo.Upsert(context.Background(),
		contract.CharacterRecord{ID: 301, Name: "A", FirstSeenAt: old, LastCensusAt: &old}, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// fresh: last_census_at recent
	fresh := now.Add(-time.Hour)
	if err := repo.Upsert(context.Background(),
		contract.CharacterRecord{ID: 302, Name: "B", FirstSeenAt: fresh, LastCensusAt: &fresh}, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	cutoff := now.Add(-24 * time.Hour)
	stale, err := repo.ListStale(context.Background(), cutoff, 10)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(stale) != 1 || stale[0].ID != 301 {
		t.Errorf("stale = %+v, want only id 301", stale)
	}
}
```

- [ ] **Step 4: Run tests to verify they fail**

```bash
go test ./infrastructure/sqlite/repository/ -run TestCharacterRepository -v
```

Expected: FAIL — package/`NewCharacterRepository` undefined.

- [ ] **Step 5: Implement the repository**

`infrastructure/sqlite/repository/character.go`:

```go
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// CharacterRepository is a SQLite implementation of contract.CharacterRepository.
type CharacterRepository struct {
	driver contract.SQLiteDriver
}

func NewCharacterRepository(driver contract.SQLiteDriver) contract.CharacterRepository {
	return &CharacterRepository{driver: driver}
}

// Upsert replaces the character row and its jobs in one transaction.
func (r *CharacterRepository) Upsert(ctx context.Context, rec contract.CharacterRecord, jobs []contract.ClassJobRecord) error {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("character upsert begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO characters (
			id, name, world, datacenter, region, race, tribe, gender, grand_company,
			fc_id, fc_name, achievements_private, latest_achievement_id, latest_achievement_at,
			first_seen_at, last_census_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			world = excluded.world,
			datacenter = excluded.datacenter,
			region = excluded.region,
			race = excluded.race,
			tribe = excluded.tribe,
			gender = excluded.gender,
			grand_company = excluded.grand_company,
			fc_id = excluded.fc_id,
			fc_name = excluded.fc_name,
			achievements_private = excluded.achievements_private,
			latest_achievement_id = excluded.latest_achievement_id,
			latest_achievement_at = excluded.latest_achievement_at,
			last_census_at = excluded.last_census_at,
			deleted_at = NULL`,
		rec.ID, rec.Name, rec.World, rec.Datacenter, rec.Region, rec.Race, rec.Tribe,
		rec.Gender, rec.GrandCompany, nullableString(rec.FreeCompanyID), nullableString(rec.FreeCompanyName),
		boolInt(rec.AchievementsPrivate), nullableUint32(rec.LatestAchievementID), nullableTime(rec.LatestAchievementAt),
		formatTime(rec.FirstSeenAt), nullableTime(rec.LastCensusAt), nullableTime(rec.DeletedAt)); err != nil {
		return fmt.Errorf("character upsert: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM character_jobs WHERE character_id = ?`, rec.ID); err != nil {
		return fmt.Errorf("character upsert delete jobs: %w", err)
	}
	for _, j := range jobs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO character_jobs (character_id, class_job_id, name, level, exp_level)
			 VALUES (?, ?, ?, ?, ?)`,
			j.CharacterID, j.ClassJobID, j.Name, j.Level, j.ExpLevel); err != nil {
			return fmt.Errorf("character upsert insert job: %w", err)
		}
	}
	return tx.Commit()
}

// Get returns the character or nil when absent.
func (r *CharacterRepository) Get(ctx context.Context, id uint32) (*contract.CharacterRecord, error) {
	row, err := r.driver.FetchOne(ctx,
		`SELECT id, name, world, datacenter, region, race, tribe, gender, grand_company,
		        fc_id, fc_name, achievements_private, latest_achievement_id, latest_achievement_at,
		        first_seen_at, last_census_at, deleted_at
		   FROM characters WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	rec, err := scanCharacter(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rec, err
}

// GetJobs returns the character's job levels.
func (r *CharacterRepository) GetJobs(ctx context.Context, id uint32) ([]contract.ClassJobRecord, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT character_id, class_job_id, name, level, exp_level
		   FROM character_jobs WHERE character_id = ? ORDER BY class_job_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []contract.ClassJobRecord
	for rows.Next() {
		var j contract.ClassJobRecord
		if err := rows.Scan(&j.CharacterID, &j.ClassJobID, &j.Name, &j.Level, &j.ExpLevel); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (r *CharacterRepository) MarkDeleted(ctx context.Context, id uint32, at time.Time) error {
	_, err := r.driver.Execute(ctx,
		`UPDATE characters SET deleted_at = ? WHERE id = ?`, formatTime(at), id)
	if err != nil {
		return fmt.Errorf("mark deleted: %w", err)
	}
	return nil
}

func (r *CharacterRepository) UpdateAchievementSummary(ctx context.Context, id uint32, private bool, latestID *uint32, latestAt *time.Time) error {
	_, err := r.driver.Execute(ctx,
		`UPDATE characters
		    SET achievements_private = ?, latest_achievement_id = ?, latest_achievement_at = ?
		  WHERE id = ?`,
		boolInt(private), nullableUint32(latestID), nullableTime(latestAt), id)
	if err != nil {
		return fmt.Errorf("update achievement summary: %w", err)
	}
	return nil
}

func (r *CharacterRepository) ListStale(ctx context.Context, cutoff time.Time, limit int) ([]contract.CharacterRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.driver.FetchMany(ctx,
		`SELECT id, name, world, datacenter, region, race, tribe, gender, grand_company,
		        fc_id, fc_name, achievements_private, latest_achievement_id, latest_achievement_at,
		        first_seen_at, last_census_at, deleted_at
		   FROM characters
		  WHERE deleted_at IS NULL
		    AND (last_census_at IS NULL OR last_census_at < ?)
		  ORDER BY last_census_at ASC
		  LIMIT ?`, formatTime(cutoff), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.CharacterRecord
	for rows.Next() {
		rec, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// scanCharacter scans one character row into a CharacterRecord.
func scanCharacter(row interface{ Scan(...any) error }) (*contract.CharacterRecord, error) {
	var rec contract.CharacterRecord
	var gender uint8
	var achievementsPrivate int
	var fcID, fcName, latestID, firstSeen, latestAt, lastCensus, deletedAt sql.NullString
	if err := row.Scan(&rec.ID, &rec.Name, &rec.World, &rec.Datacenter, &rec.Region,
		&rec.Race, &rec.Tribe, &gender, &rec.GrandCompany,
		&fcID, &fcName, &achievementsPrivate, &latestID,
		&firstSeen, &latestAt, &lastCensus, &deletedAt); err != nil {
		return nil, err
	}
	rec.Gender = gender
	rec.AchievementsPrivate = achievementsPrivate != 0
	rec.FreeCompanyID = sqlStringPtr(fcID)
	rec.FreeCompanyName = sqlStringPtr(fcName)
	rec.LatestAchievementID = sqlUint32Ptr(latestID)
	rec.LatestAchievementAt = sqlTimePtr(latestAt)
	rec.FirstSeenAt, _ = parseTime(firstSeen.String)
	rec.LastCensusAt = sqlTimePtr(lastCensus)
	rec.DeletedAt = sqlTimePtr(deletedAt)
	return &rec, nil
}
```

Note: `latest_achievement_id` is INTEGER, scanned into `sql.NullString` above would type-mismatch. Correct it in the impl: scan `latest_achievement_id` into `sql.NullInt64`, not `sql.NullString`. The final code must use `sql.NullInt64` for that column. (The implementer applies this correction while writing the file; see the "gotchas" note at the end of this plan.)

- [ ] **Step 6: Write the shared scan helpers** (add to `time.go` or a new `scan.go`)

`infrastructure/sqlite/repository/scan.go`:

```go
package repository

import (
	"database/sql"
	"strconv"
	"time"
)

func nullableString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullableUint32(v *uint32) any {
	if v == nil {
		return nil
	}
	return int64(*v)
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sqlStringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func sqlUint32Ptr(ni sql.NullInt64) *uint32 {
	if !ni.Valid {
		return nil
	}
	v := uint32(ni.Int64)
	return &v
}

func sqlTimePtr(ns sql.NullString) *time.Time {
	if !ns.Valid {
		return nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil
	}
	return &t
}
```

- [ ] **Step 7: Write the in-memory fake**

`mock/repository/character.go`:

```go
package repository

import (
	"context"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// CharacterRepository is an in-memory fake with error-injection and call recording.
type CharacterRepository struct {
	mu           sync.Mutex
	characters   map[uint32]contract.CharacterRecord
	jobs         map[uint32][]contract.ClassJobRecord
	UpsertErr    error
	GetErr       error
	MarkDeletedErr error
	UpdateErr    error
	ListStaleErr error
	UpsertCalls  int
}

func NewFake() *CharacterRepository {
	return &CharacterRepository{
		characters: map[uint32]contract.CharacterRecord{},
		jobs:       map[uint32][]contract.ClassJobRecord{},
	}
}

func (f *CharacterRepository) Upsert(ctx context.Context, rec contract.CharacterRecord, jobs []contract.ClassJobRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.UpsertCalls++
	if f.UpsertErr != nil {
		return f.UpsertErr
	}
	if rec.FirstSeenAt.IsZero() {
		rec.FirstSeenAt = time.Now().UTC()
	}
	now := time.Now().UTC()
	rec.LastCensusAt = &now
	rec.DeletedAt = nil
	f.characters[rec.ID] = rec
	if jobs != nil {
		f.jobs[rec.ID] = append([]contract.ClassJobRecord(nil), jobs...)
	}
	return nil
}

func (f *CharacterRepository) Get(ctx context.Context, id uint32) (*contract.CharacterRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.GetErr != nil {
		return nil, f.GetErr
	}
	rec, ok := f.characters[id]
	if !ok {
		return nil, nil
	}
	cp := rec
	return &cp, nil
}

func (f *CharacterRepository) GetJobs(ctx context.Context, id uint32) ([]contract.ClassJobRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contract.ClassJobRecord(nil), f.jobs[id]...), nil
}

func (f *CharacterRepository) MarkDeleted(ctx context.Context, id uint32, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.MarkDeletedErr != nil {
		return f.MarkDeletedErr
	}
	rec := f.characters[id]
	rec.DeletedAt = &at
	f.characters[id] = rec
	return nil
}

func (f *CharacterRepository) UpdateAchievementSummary(ctx context.Context, id uint32, private bool, latestID *uint32, latestAt *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.UpdateErr != nil {
		return f.UpdateErr
	}
	rec := f.characters[id]
	rec.AchievementsPrivate = private
	rec.LatestAchievementID = latestID
	rec.LatestAchievementAt = latestAt
	f.characters[id] = rec
	return nil
}

func (f *CharacterRepository) ListStale(ctx context.Context, cutoff time.Time, limit int) ([]contract.CharacterRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListStaleErr != nil {
		return nil, f.ListStaleErr
	}
	var out []contract.CharacterRecord
	for _, rec := range f.characters {
		if rec.DeletedAt != nil {
			continue
		}
		if rec.LastCensusAt == nil || rec.LastCensusAt.Before(cutoff) {
			out = append(out, rec)
		}
	}
	return out, nil
}

var _ contract.CharacterRepository = (*CharacterRepository)(nil)
```

- [ ] **Step 8: Run tests to verify they pass**

```bash
go test ./infrastructure/sqlite/repository/ -v
```

Expected: PASS (5 tests).

- [ ] **Step 9: Commit**

```bash
git add port/contract/character_repository.go infrastructure/sqlite/repository/ mock/repository/
git commit -m "feat(repository): character repository (contract, sqlite impl, mock)"
```

---

### Task 4: FreeCompanyRepository

**Files:**
- Create: `port/contract/free_company_repository.go`
- Create: `infrastructure/sqlite/repository/free_company.go`
- Create: `mock/repository/free_company.go`
- Test: `infrastructure/sqlite/repository/free_company_test.go`

- [ ] **Step 1: Write the contract**

```go
package contract

import "context"

// FreeCompanyRepository persists free-company snapshots.
type FreeCompanyRepository interface {
	Upsert(ctx context.Context, rec FreeCompanyRecord) error
	// Get returns the FC or nil (no error) when absent.
	Get(ctx context.Context, id string) (*FreeCompanyRecord, error)
}
```

- [ ] **Step 2: Write the failing tests**

```go
package repository

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestFCRepo(t *testing.T) contract.FreeCompanyRepository {
	t.Helper()
	_, cleanup, driver := newTestDriver(t)
	t.Cleanup(cleanup)
	return NewFreeCompanyRepository(driver)
}

func TestFreeCompanyRepository_UpsertAndGet(t *testing.T) {
	repo := newTestFCRepo(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	formed := now.Add(-24 * 30 * time.Hour)
	rec := contract.FreeCompanyRecord{
		ID: "9234567890123456789", Name: "The Scions", World: "Ultros",
		Datacenter: "Primal", MemberCount: 42, FormedAt: &formed, LastSeenAt: now,
	}
	if err := repo.Upsert(context.Background(), rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := repo.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.Name != "The Scions" || got.MemberCount != 42 {
		t.Errorf("got %+v", got)
	}
}

func TestFreeCompanyRepository_GetNotFound(t *testing.T) {
	repo := newTestFCRepo(t)
	got, err := repo.Get(context.Background(), "0000000000000000000")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}
```

- [ ] **Step 3: Implement** (`free_company.go`)

```go
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type FreeCompanyRepository struct {
	driver contract.SQLiteDriver
}

func NewFreeCompanyRepository(driver contract.SQLiteDriver) contract.FreeCompanyRepository {
	return &FreeCompanyRepository{driver: driver}
}

func (r *FreeCompanyRepository) Upsert(ctx context.Context, rec contract.FreeCompanyRecord) error {
	_, err := r.driver.Execute(ctx,
		`INSERT INTO free_companies (id, name, world, datacenter, member_count, formed_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			world = excluded.world,
			datacenter = excluded.datacenter,
			member_count = excluded.member_count,
			formed_at = excluded.formed_at,
			last_seen_at = excluded.last_seen_at`,
		rec.ID, rec.Name, rec.World, rec.Datacenter, rec.MemberCount,
		nullableTime(rec.FormedAt), formatTime(rec.LastSeenAt))
	if err != nil {
		return fmt.Errorf("free company upsert: %w", err)
	}
	return nil
}

func (r *FreeCompanyRepository) Get(ctx context.Context, id string) (*contract.FreeCompanyRecord, error) {
	row, err := r.driver.FetchOne(ctx,
		`SELECT id, name, world, datacenter, member_count, formed_at, last_seen_at
		   FROM free_companies WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	var rec contract.FreeCompanyRecord
	var formedAt, lastSeen sql.NullString
	if err := row.Scan(&rec.ID, &rec.Name, &rec.World, &rec.Datacenter, &rec.MemberCount,
		&formedAt, &lastSeen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	rec.FormedAt = sqlTimePtr(formedAt)
	if t, err := parseTime(lastSeen.String); err == nil {
		rec.LastSeenAt = t
	}
	return &rec, nil
}
```

- [ ] **Step 4: Write the fake** (`mock/repository/free_company.go`)

```go
package repository

import (
	"context"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type FreeCompanyRepository struct {
	mu       sync.Mutex
	fcs      map[string]contract.FreeCompanyRecord
	UpsertErr error
}

func NewFake() *FreeCompanyRepository {
	return &FreeCompanyRepository{fcs: map[string]contract.FreeCompanyRecord{}}
}

func (f *FreeCompanyRepository) Upsert(ctx context.Context, rec contract.FreeCompanyRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.UpsertErr != nil {
		return f.UpsertErr
	}
	f.fcs[rec.ID] = rec
	return nil
}

func (f *FreeCompanyRepository) Get(ctx context.Context, id string) (*contract.FreeCompanyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.fcs[id]
	if !ok {
		return nil, nil
	}
	cp := rec
	return &cp, nil
}

var _ contract.FreeCompanyRepository = (*FreeCompanyRepository)(nil)
```

- [ ] **Step 5: Run tests + commit**

```bash
go test ./infrastructure/sqlite/repository/ -v
git add port/contract/free_company_repository.go infrastructure/sqlite/repository/free_company.go mock/repository/free_company.go
git commit -m "feat(repository): free company repository (contract, sqlite impl, mock)"
```

Note: the test helper `newTestDriver(t)` (returning `driver, cleanup, driver`) is defined once in a shared `helpers_test.go` (see Task 3 note) — the implementer refactors `newTestRepo` into a reusable `newTestDriver` helper so Tasks 3–6 share it.

---

### Task 5: AchievementRepository

**Files:**
- Create: `port/contract/achievement_repository.go`
- Create: `infrastructure/sqlite/repository/achievement.go`
- Create: `mock/repository/achievement.go`
- Test: `infrastructure/sqlite/repository/achievement_test.go`

- [ ] **Step 1: Write the contract**

```go
package contract

import "context"

// AchievementRepository persists the milestone registry and characters' earned
// milestones.
type AchievementRepository interface {
	// SyncMilestones idempotently upserts the registry (INSERT OR IGNORE).
	SyncMilestones(ctx context.Context, registry []MilestoneAchievement) error
	// ListMilestones returns the full registry.
	ListMilestones(ctx context.Context) ([]MilestoneAchievement, error)
	// UpsertCharacterMilestones replaces a character's earned milestones.
	UpsertCharacterMilestones(ctx context.Context, characterID uint32, milestones []CharacterMilestone) error
	// ListCharacterMilestones returns a character's earned milestones.
	ListCharacterMilestones(ctx context.Context, characterID uint32) ([]CharacterMilestone, error)
}
```

- [ ] **Step 2: Write the failing tests**

```go
package repository

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestAchievementRepo(t *testing.T) contract.AchievementRepository {
	t.Helper()
	_, cleanup, driver := newTestDriver(t)
	t.Cleanup(cleanup)
	return NewAchievementRepository(driver)
}

func expStr(s string) *string { return &s }

func TestAchievementRepository_SyncAndList(t *testing.T) {
	repo := newTestAchievementRepo(t)
	registry := []contract.MilestoneAchievement{
		{AchievementID: 590, Kind: contract.MilestoneKindChocobo, Detail: "My Little Chocobo"},
		{AchievementID: 739, Kind: contract.MilestoneKindExpansion, Expansion: expStr("Heavensward"), Detail: "Heavensward"},
	}
	if err := repo.SyncMilestones(context.Background(), registry); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	// idempotent: syncing again must not error or duplicate
	if err := repo.SyncMilestones(context.Background(), registry); err != nil {
		t.Fatalf("SyncMilestones (2nd): %v", err)
	}
	got, err := repo.ListMilestones(context.Background())
	if err != nil {
		t.Fatalf("ListMilestones: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("milestones = %d, want 2", len(got))
	}
}

func TestAchievementRepository_CharacterMilestones(t *testing.T) {
	repo := newTestAchievementRepo(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	ms := []contract.CharacterMilestone{
		{CharacterID: 42, AchievementID: 590, AchievedAt: now},
		{CharacterID: 42, AchievementID: 739, AchievedAt: now.Add(-time.Hour)},
	}
	if err := repo.UpsertCharacterMilestones(context.Background(), 42, ms); err != nil {
		t.Fatalf("UpsertCharacterMilestones: %v", err)
	}
	got, err := repo.ListCharacterMilestones(context.Background(), 42)
	if err != nil {
		t.Fatalf("ListCharacterMilestones: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("milestones = %d, want 2", len(got))
	}
}
```

- [ ] **Step 3: Implement** (`achievement.go`)

```go
package repository

import (
	"context"
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type AchievementRepository struct {
	driver contract.SQLiteDriver
}

func NewAchievementRepository(driver contract.SQLiteDriver) contract.AchievementRepository {
	return &AchievementRepository{driver: driver}
}

func (r *AchievementRepository) SyncMilestones(ctx context.Context, registry []contract.MilestoneAchievement) error {
	for _, m := range registry {
		_, err := r.driver.Execute(ctx,
			`INSERT OR IGNORE INTO milestone_achievements (achievement_id, kind, expansion, detail)
			 VALUES (?, ?, ?, ?)`,
			m.AchievementID, m.Kind, nullableString(m.Expansion), m.Detail)
		if err != nil {
			return fmt.Errorf("sync milestone %d: %w", m.AchievementID, err)
		}
	}
	return nil
}

func (r *AchievementRepository) ListMilestones(ctx context.Context) ([]contract.MilestoneAchievement, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT achievement_id, kind, expansion, detail FROM milestone_achievements ORDER BY achievement_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.MilestoneAchievement
	for rows.Next() {
		var m contract.MilestoneAchievement
		var expansion *string
		if err := rows.Scan(&m.AchievementID, &m.Kind, &expansion, &m.Detail); err != nil {
			return nil, err
		}
		m.Expansion = expansion
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *AchievementRepository) UpsertCharacterMilestones(ctx context.Context, characterID uint32, milestones []contract.CharacterMilestone) error {
	// Replace-all is done in a transaction so a partial failure doesn't corrupt state.
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("milestones begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM character_milestones WHERE character_id = ?`, characterID); err != nil {
		return err
	}
	for _, m := range milestones {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO character_milestones (character_id, achievement_id, achieved_at) VALUES (?, ?, ?)`,
			m.CharacterID, m.AchievementID, formatTime(m.AchievedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *AchievementRepository) ListCharacterMilestones(ctx context.Context, characterID uint32) ([]contract.CharacterMilestone, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT character_id, achievement_id, achieved_at FROM character_milestones
		  WHERE character_id = ? ORDER BY achieved_at DESC`, characterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.CharacterMilestone
	for rows.Next() {
		var m contract.CharacterMilestone
		var achievedAt string
		if err := rows.Scan(&m.CharacterID, &m.AchievementID, &achievedAt); err != nil {
			return nil, err
		}
		if t, err := parseTime(achievedAt); err == nil {
			m.AchievedAt = t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Write the fake** (`mock/repository/achievement.go`)

```go
package repository

import (
	"context"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type AchievementRepository struct {
	mu        sync.Mutex
	registry  map[uint32]contract.MilestoneAchievement
	milestones map[uint32][]contract.CharacterMilestone
	SyncErr   error
	UpsertErr error
}

func NewFake() *AchievementRepository {
	return &AchievementRepository{
		registry:   map[uint32]contract.MilestoneAchievement{},
		milestones: map[uint32][]contract.CharacterMilestone{},
	}
}

func (f *AchievementRepository) SyncMilestones(ctx context.Context, registry []contract.MilestoneAchievement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SyncErr != nil {
		return f.SyncErr
	}
	for _, m := range registry {
		f.registry[m.AchievementID] = m
	}
	return nil
}

func (f *AchievementRepository) ListMilestones(ctx context.Context) ([]contract.MilestoneAchievement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]contract.MilestoneAchievement, 0, len(f.registry))
	for _, m := range f.registry {
		out = append(out, m)
	}
	return out, nil
}

func (f *AchievementRepository) UpsertCharacterMilestones(ctx context.Context, characterID uint32, milestones []contract.CharacterMilestone) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.UpsertErr != nil {
		return f.UpsertErr
	}
	f.milestones[characterID] = append([]contract.CharacterMilestone(nil), milestones...)
	return nil
}

func (f *AchievementRepository) ListCharacterMilestones(ctx context.Context, characterID uint32) ([]contract.CharacterMilestone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contract.CharacterMilestone(nil), f.milestones[characterID]...), nil
}

var _ contract.AchievementRepository = (*AchievementRepository)(nil)
```

- [ ] **Step 5: Run tests + commit**

```bash
go test ./infrastructure/sqlite/repository/ -v
git add port/contract/achievement_repository.go infrastructure/sqlite/repository/achievement.go mock/repository/achievement.go
git commit -m "feat(repository): achievement repository (contract, sqlite impl, mock)"
```

---

### Task 6: CensusRunRepository

**Files:**
- Create: `port/contract/census_run_repository.go`
- Create: `infrastructure/sqlite/repository/census_run.go`
- Create: `mock/repository/census_run.go`
- Test: `infrastructure/sqlite/repository/census_run_test.go`

- [ ] **Step 1: Write the contract**

```go
package contract

import "context"

// CensusRunRepository records census sweeps for operational tracking.
type CensusRunRepository interface {
	// Start creates a run and returns its ID.
	Start(ctx context.Context) (int64, error)
	// Finish records completion with per-run counters.
	Finish(ctx context.Context, id int64, charactersSeen, newCharacters int) error
}
```

- [ ] **Step 2: Write the failing tests**

```go
package repository

import (
	"context"
	"testing"
)

func newTestRunRepo(t *testing.T) contract.CensusRunRepository {
	t.Helper()
	_, cleanup, driver := newTestDriver(t)
	t.Cleanup(cleanup)
	return NewCensusRunRepository(driver)
}

func TestCensusRunRepository_StartAndFinish(t *testing.T) {
	repo := newTestRunRepo(t)
	id, err := repo.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id <= 0 {
		t.Fatalf("id = %d, want > 0", id)
	}
	if err := repo.Finish(context.Background(), id, 1000, 50); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}
```

- [ ] **Step 3: Implement** (`census_run.go`)

```go
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type CensusRunRepository struct {
	driver contract.SQLiteDriver
}

func NewCensusRunRepository(driver contract.SQLiteDriver) contract.CensusRunRepository {
	return &CensusRunRepository{driver: driver}
}

func (r *CensusRunRepository) Start(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(timeLayout)
	res, err := r.driver.Execute(ctx,
		`INSERT INTO census_runs (started_at) VALUES (?)`, now)
	if err != nil {
		return 0, fmt.Errorf("census run start: %w", err)
	}
	return res.LastInsertId()
}

func (r *CensusRunRepository) Finish(ctx context.Context, id int64, charactersSeen, newCharacters int) error {
	now := time.Now().UTC().Format(timeLayout)
	_, err := r.driver.Execute(ctx,
		`UPDATE census_runs SET finished_at = ?, characters_seen = ?, new_characters = ?
		  WHERE id = ?`, now, charactersSeen, newCharacters, id)
	if err != nil {
		return fmt.Errorf("census run finish: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Write the fake** (`mock/repository/census_run.go`)

```go
package repository

import (
	"context"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type CensusRunRepository struct {
	mu       sync.Mutex
	nextID   int64
	started  []int64
	finished map[int64][2]int // id -> {charactersSeen, newCharacters}
}

func NewFake() *CensusRunRepository {
	return &CensusRunRepository{nextID: 1, finished: map[int64][2]int{}}
}

func (f *CensusRunRepository) Start(ctx context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID
	f.nextID++
	f.started = append(f.started, id)
	return id, nil
}

func (f *CensusRunRepository) Finish(ctx context.Context, id int64, charactersSeen, newCharacters int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished[id] = [2]int{charactersSeen, newCharacters}
	return nil
}

var _ contract.CensusRunRepository = (*CensusRunRepository)(nil)
```

- [ ] **Step 5: Run tests + commit**

```bash
go test ./infrastructure/sqlite/repository/ -v
git add port/contract/census_run_repository.go infrastructure/sqlite/repository/census_run.go mock/repository/census_run.go
git commit -m "feat(repository): census run repository (contract, sqlite impl, mock)"
```

---

### Task 7: Container wiring

**Files:**
- Modify: `container/infrastructure.go`
- Test: `container/census_repository_test.go`

- [ ] **Step 1: Write the failing test**

`container/census_repository_test.go`:

```go
package container

import (
	"path/filepath"
	"testing"
)

func TestServiceContainer_CensusRepositories(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "census.db"))
	Load = NewServiceContainer()

	if Load.CharacterRepository() == nil {
		t.Fatal("CharacterRepository nil")
	}
	if Load.FreeCompanyRepository() == nil {
		t.Fatal("FreeCompanyRepository nil")
	}
	if Load.AchievementRepository() == nil {
		t.Fatal("AchievementRepository nil")
	}
	if Load.CensusRunRepository() == nil {
		t.Fatal("CensusRunRepository nil")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./container/ -run TestServiceContainer_CensusRepositories
```

Expected: FAIL — `Load.CharacterRepository` undefined.

- [ ] **Step 3: Implement**

In `container/infrastructure.go`, add fields to `InfrastructureContainer`:

```go
	characterRepository    contract.CharacterRepository
	freeCompanyRepository  contract.FreeCompanyRepository
	achievementRepository  contract.AchievementRepository
	censusRunRepository    contract.CensusRunRepository
```

Add the import `"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/repository"` and four accessors following the existing lazy pattern (each checks the SQLite driver, logs a warning and returns nil when unavailable, else constructs via `repository.NewX(driver)` and caches):

```go
func (s *ServiceContainer) CharacterRepository() contract.CharacterRepository {
	if s.infrastructure.characterRepository != nil {
		return s.infrastructure.characterRepository
	}
	driver := s.SQLite()
	if driver == nil {
		logging.Warn("container.character_repository", "sqlite driver unavailable")
		return nil
	}
	s.infrastructure.characterRepository = repository.NewCharacterRepository(driver)
	return s.infrastructure.characterRepository
}
```

(Repeat for `FreeCompanyRepository`, `AchievementRepository`, `CensusRunRepository`, each with its own accessor and log event name.)

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./container/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add container/
git commit -m "feat(container): census repository accessors"
```

---

### Task 8: Documentation

**Files:**
- Create: `docs/census.md`
- Modify: `README.md` (add link)

- [ ] **Step 1: Write `docs/census.md`**

Cover: the census domain model and its tables (characters, character_jobs, milestone_achievements, character_milestones, free_companies, census_runs); the "unverified" stub convention (name='', last_census_at NULL); timestamps format; the milestone registry concept and `MilestoneKind` values; repository contracts and what each persists; the DC→region derivation (deferred to the service phase); how the ingest handlers (next phase) will use these repositories. Note that milestone achievement IDs are verified against XIVAPI (game achievement IDs, matching godestone's `AchievementInfo.ID`).

- [ ] **Step 2: Link from README**

Add `docs/census.md` to the "Key documentation" list.

- [ ] **Step 3: Commit**

```bash
git add docs/census.md README.md
git commit -m "docs: census domain model and repositories"
```

---

### Task 9: Final verification

- [ ] **Step 1: Full suite with race detector**

```bash
go test ./... -race
```

Expected: all PASS.

- [ ] **Step 2: Lint**

```bash
PATH="$HOME/go/bin:$PATH" make lint
```

Expected: clean.

- [ ] **Step 3: Build**

```bash
make build
```

Expected: `bin/ffxiv-census` produced.

- [ ] **Step 4: Commit any fixes**

```bash
git add -A && git commit -m "chore: census domain phase verification"
```

---

## Implementation Gotchas

1. **`latest_achievement_id` is INTEGER, not TEXT.** In `scanCharacter` (Task 3), scan that column into `sql.NullInt64` (not `sql.NullString`). The `sqlUint32Ptr` helper already expects `sql.NullInt64`. The column order in every `SELECT` must be: `..., latest_achievement_id, latest_achievement_at, first_seen_at, last_census_at, deleted_at` — and `latest_achievement_at`, `first_seen_at`, `last_census_at`, `deleted_at` are TEXT scanned into `sql.NullString`.

2. **Shared test helper.** Tasks 3–6 each reference a `newTestDriver(t) (contract.SQLiteDriver, func(), contract.SQLiteDriver)`-style helper. The implementer should create `infrastructure/sqlite/repository/helpers_test.go` once (extracting the `newTestRepo` body into a `newTestDriver(t) (driver, cleanup)` returning the driver) and have `newTestRepo`/`newTestFCRepo`/`newTestAchievementRepo`/`newTestRunRepo` delegate to it. The exact signature in the plan is illustrative; make it consistent across all test files.

3. **`database/sql` in the port contract.** `CharacterRepository` (and others) depend on `contract.SQLiteDriver`, which exposes `*sql.DB`/`*sql.Row`/`*sql.Rows` — so the repository impl imports `database/sql`. This matches the existing `queue`/`mysql` pattern and is accepted for this infrastructure-level port.

4. **NULL vs empty.** `characters.name`, `world`, `datacenter`, `region` default to `''` (NOT NULL) to support discovery stubs; `race`/`tribe`/`grand_company`/`fc_*` are nullable. `ListStale` treats NULL `last_census_at` as stale.

5. **`strconv` import** in `scan.go` is not needed by the helpers as written (nullableUint32 returns `int64` directly); drop unused imports if golangci-lint flags them.
