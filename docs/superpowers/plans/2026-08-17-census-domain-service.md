# Census Domain Service — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the domain brain of the census — the milestone registry, datacenter→region mapping, and a `CensusService` that converts Lodestone DTOs into persisted records and computes milestone/activity facts. The ingest handlers (next phase) will consume this service.

**Architecture:** Pure domain logic in `domain/census/` (no SQL, no HTTP — depends only on `port/contract` interfaces and godestone DTO types). `CensusService` is constructed with the four repository contracts from the previous phase; handlers will call it. godestone types are accepted at the service boundary (documented design — `contract.LodestoneClient` already returns them) and converted to `contract` records here.

**Tech Stack:** Go 1.25. Existing contracts: `CharacterRepository`, `FreeCompanyRepository`, `AchievementRepository`, `CensusRunRepository`, `CharacterRecord`, `ClassJobRecord`, `MilestoneAchievement`, `CharacterMilestone`. godestone v2 DTO types (`godestone.Character`, `godestone.ClassJob`, `godestone.AchievementInfo`, `godestone.AllAchievementInfo`, `gender.Gender`).

**Commit convention:** one commit per task, pushed to `origin master`.

**Verification:** `go test ./...`, `go build ./...`, `make lint` (golangci-lint at `~/go/bin`).

---

## File Map

```
domain/census/region.go        # DC -> region mapping
domain/census/region_test.go
domain/census/milestone.go     # canonical milestone set (MilestoneSet)
domain/census/milestone_test.go
domain/census/service.go       # CensusService: conversion, upsert, achievements, IsActive
domain/census/service_test.go  # uses mock/repository fakes
container/infrastructure.go    # add CensusService() accessor (moved to domain container)
container/domain.go            # DomainContainer gets censusService field
container/census_service_test.go
docs/census.md                 # update: milestone registry + service now implemented
```

---

### Task 1: Datacenter → region mapping

**Files:**
- Create: `domain/census/region.go`
- Test: `domain/census/region_test.go`

- [ ] **Step 1: Write the failing test**

```go
package census

import "testing"

func TestRegionForDatacenter(t *testing.T) {
	cases := []struct{ dc, want string }{
		{"Aether", "NA"}, {"Primal", "NA"}, {"Crystal", "NA"}, {"Dynamis", "NA"},
		{"Chaos", "EU"}, {"Light", "EU"},
		{"Elemental", "JP"}, {"Gaia", "JP"}, {"Mana", "JP"}, {"Meteor", "JP"},
		{"Materia", "OCE"},
		{"Unknown", ""},
	}
	for _, c := range cases {
		if got := RegionForDatacenter(c.dc); got != c.want {
			t.Errorf("RegionForDatacenter(%q) = %q, want %q", c.dc, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./domain/census/ -run TestRegionForDatacenter
```

Expected: FAIL — `RegionForDatacenter` undefined.

- [ ] **Step 3: Implement**

```go
package census

// datacenterRegion maps each FFXIV logical datacenter to its physical region.
// World counts roll up to datacenter, datacenter rolls up to region.
var datacenterRegion = map[string]string{
	"Aether":    "NA",
	"Primal":    "NA",
	"Crystal":   "NA",
	"Dynamis":   "NA",
	"Chaos":     "EU",
	"Light":     "EU",
	"Elemental": "JP",
	"Gaia":      "JP",
	"Mana":      "JP",
	"Meteor":    "JP",
	"Materia":   "OCE",
}

// RegionForDatacenter returns the region (NA/EU/JP/OCE) for a datacenter name,
// or "" when unknown.
func RegionForDatacenter(dc string) string {
	return datacenterRegion[dc]
}
```

- [ ] **Step 4: Run to verify it passes, then commit**

```bash
go test ./domain/census/ -v
git add domain/census/region.go domain/census/region_test.go
git commit -m "feat(census): datacenter to region mapping"
```

---

### Task 2: Milestone registry

**Files:**
- Create: `domain/census/milestone.go`
- Test: `domain/census/milestone_test.go`

- [ ] **Step 1: Verify achievement IDs against XIVAPI**

The milestone registry uses **game achievement IDs** (uint32, matching `godestone.AchievementInfo.ID`). Verify each ID before committing it:

```bash
# Confirmed working endpoint — returns the achievement name for a game ID.
curl -s "https://v2.xivapi.com/api/sheet/Achievement/590?fields=Name,Description"
# => {"row_id":590,"fields":{"Description":"Obtain a chocobo whistle.","Name":"My Little Chocobo"}}
```

Known-verified: `590` = "My Little Chocobo".

For the expansion MSQ completions and job level-cap achievements, fetch their IDs from the XIVAPI Achievement sheet (each expansion has a self-named achievement, e.g. "Heavensward" / "Stormblood" / "Shadowbringers" / "Endwalker" / "Dawntrail"). If a specific ID cannot be verified from XIVAPI, leave that entry out of `MilestoneSet` rather than committing an unverified number, and note it in the PR/commit message. The registry is additive — new IDs can be appended in a later commit.

- [ ] **Step 2: Write the failing test**

```go
package census

import (
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestMilestoneSet_HasChocobo(t *testing.T) {
	found := false
	for _, m := range MilestoneSet {
		if m.AchievementID == 590 && m.Kind == contract.MilestoneKindChocobo {
			found = true
		}
	}
	if !found {
		t.Fatal("MilestoneSet must contain chocobo achievement 590")
	}
}

func TestMilestoneSet_KindsValid(t *testing.T) {
	for _, m := range MilestoneSet {
		switch m.Kind {
		case contract.MilestoneKindExpansion, contract.MilestoneKindJobLevel, contract.MilestoneKindChocobo:
		default:
			t.Errorf("milestone %d has invalid kind %q", m.AchievementID, m.Kind)
		}
		if m.Kind == contract.MilestoneKindExpansion && m.Expansion == nil {
			t.Errorf("expansion milestone %d missing expansion label", m.AchievementID)
		}
	}
}

func TestMilestoneSet_NoDuplicateIDs(t *testing.T) {
	seen := map[uint32]bool{}
	for _, m := range MilestoneSet {
		if seen[m.AchievementID] {
			t.Errorf("duplicate milestone ID %d", m.AchievementID)
		}
		seen[m.AchievementID] = true
	}
}
```

- [ ] **Step 3: Run to verify it fails**

```bash
go test ./domain/census/ -run TestMilestoneSet
```

Expected: FAIL — `MilestoneSet` undefined.

- [ ] **Step 4: Implement**

```go
package census

import "github.com/mihaiflorentin88/ffxiv-census/port/contract"

func expansionPtr(s string) *string { return &s }

// MilestoneSet is the canonical registry of achievements the census tracks.
// Achievement IDs are game achievement IDs (verified against the XIVAPI
// Achievement sheet), matching godestone's AchievementInfo.ID. Additive only:
// append new milestones here; they are synced to the DB idempotently via
// AchievementRepository.SyncMilestones (INSERT OR IGNORE).
var MilestoneSet = []contract.MilestoneAchievement{
	// Chocobo (verified: XIVAPI sheet 590).
	{AchievementID: 590, Kind: contract.MilestoneKindChocobo, Detail: "My Little Chocobo"},

	// Expansion MSQ completions — add verified IDs here, one per expansion.
	// e.g. {AchievementID: <id>, Kind: contract.MilestoneKindExpansion, Expansion: expansionPtr("Heavensward"), Detail: "Heavensward"},

	// Job level-cap achievements — add verified IDs here.
	// e.g. {AchievementID: <id>, Kind: contract.MilestoneKindJobLevel, Detail: "White Mage to level 100"},
}
```

- [ ] **Step 5: Run to verify it passes, then commit**

```bash
go test ./domain/census/ -v
git add domain/census/milestone.go domain/census/milestone_test.go
git commit -m "feat(census): milestone registry (chocobo 590 verified)"
```

---

### Task 3: CensusService — character conversion + upsert

**Files:**
- Create: `domain/census/service.go`
- Test: `domain/census/service_test.go`

- [ ] **Step 1: Write the failing test (uses mock fakes)**

```go
package census

import (
	"context"
	"testing"
	"time"

	"github.com/xivapi/godestone/v2"
	"github.com/xivapi/godestone/v2/data/gender"
	"github.com/xivapi/godestone/v2/provider/models"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
)

func newTestService(t *testing.T) (*Service, *mockrepo.CharacterRepository) {
	t.Helper()
	chars := mockrepo.NewCharacterFake()
	svc := NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return svc, chars
}

func TestService_UpsertCharacter(t *testing.T) {
	svc, chars := newTestService(t)

	char := &godestone.Character{
		ID:     123,
		Name:   "Tataru Taru",
		World:  "Ultros",
		DC:     "Primal",
		Gender: gender.Female,
		Race:   &models.GenderedEntity{Name: "Lalafell"},
		Tribe:  &models.GenderedEntity{Name: "Dunesfolk"},
		GrandCompanyInfo: &godestone.GrandCompanyInfo{
			GrandCompany: &models.NamedEntity{Name: "Maelstrom"},
		},
		FreeCompanyID:   "9234567890123456789",
		FreeCompanyName: "The Scions",
		ClassJobs: []*godestone.ClassJob{
			{JobID: 19, Name: "Paladin", Level: 90, ExpLevel: 12345},
			{JobID: 25, Name: "White Mage", Level: 90, ExpLevel: 0},
		},
	}

	if err := svc.UpsertCharacter(context.Background(), char); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}
	got, err := chars.Get(context.Background(), 123)
	if err != nil || got == nil {
		t.Fatalf("Get: %v / %+v", err, got)
	}
	if got.Region != "NA" {
		t.Errorf("region = %q, want NA (derived from Primal)", got.Region)
	}
	if got.Race != "Lalafell" || got.GrandCompany != "Maelstrom" {
		t.Errorf("got %+v", got)
	}
	if got.FreeCompanyID == nil || *got.FreeCompanyID != "9234567890123456789" {
		t.Errorf("free company id = %v", got.FreeCompanyID)
	}
	jobs, _ := chars.GetJobs(context.Background(), 123)
	if len(jobs) != 2 {
		t.Errorf("jobs = %d, want 2", len(jobs))
	}
}

func TestService_UpsertCharacter_NilSafe(t *testing.T) {
	svc, _ := newTestService(t)
	// Minimal character with nil race/tribe/grand company must not panic.
	char := &godestone.Character{ID: 1, Name: "X", World: "W", DC: "Primal", Gender: gender.None}
	if err := svc.UpsertCharacter(context.Background(), char); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./domain/census/ -run TestService_UpsertCharacter
```

Expected: FAIL — `NewService`/`Service` undefined.

- [ ] **Step 3: Implement `service.go` (conversion + upsert + SyncMilestones)**

```go
package census

import (
	"context"
	"time"

	"github.com/xivapi/godestone/v2"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Service is the domain brain of the census. It converts Lodestone DTOs into
// persisted records and computes milestone/activity facts. It depends only on
// contracts, never on SQL or HTTP.
type Service struct {
	characters    contract.CharacterRepository
	freeCompanies contract.FreeCompanyRepository
	achievements  contract.AchievementRepository
	censusRuns    contract.CensusRunRepository
}

func NewService(
	characters contract.CharacterRepository,
	freeCompanies contract.FreeCompanyRepository,
	achievements contract.AchievementRepository,
	censusRuns contract.CensusRunRepository,
) *Service {
	return &Service{
		characters:    characters,
		freeCompanies: freeCompanies,
		achievements:  achievements,
		censusRuns:    censusRuns,
	}
}

// SyncMilestones seeds the milestone registry into the DB (idempotent).
func (s *Service) SyncMilestones(ctx context.Context) error {
	return s.achievements.SyncMilestones(ctx, MilestoneSet)
}

// UpsertCharacter converts a Lodestone character into a CharacterRecord and
// persists it (profile + jobs) atomically. Region is derived from the
// datacenter. nil race/tribe/grand-company are tolerated (stored empty).
func (s *Service) UpsertCharacter(ctx context.Context, char *godestone.Character) error {
	rec := toCharacterRecord(char)
	jobs := toJobRecords(char)
	return s.characters.Upsert(ctx, rec, jobs)
}

func toCharacterRecord(char *godestone.Character) contract.CharacterRecord {
	now := time.Now().UTC()
	rec := contract.CharacterRecord{
		ID:          char.ID,
		Name:        char.Name,
		World:       char.World,
		Datacenter:  char.DC,
		Region:      RegionForDatacenter(char.DC),
		Gender:      uint8(char.Gender),
		FirstSeenAt: now,
		LastCensusAt: &now,
	}
	if char.Race != nil {
		rec.Race = char.Race.Name
	}
	if char.Tribe != nil {
		rec.Tribe = char.Tribe.Name
	}
	if char.GrandCompanyInfo != nil && char.GrandCompanyInfo.GrandCompany != nil {
		rec.GrandCompany = char.GrandCompanyInfo.GrandCompany.Name
	}
	if char.FreeCompanyID != "" {
		rec.FreeCompanyID = &char.FreeCompanyID
	}
	if char.FreeCompanyName != "" {
		rec.FreeCompanyName = &char.FreeCompanyName
	}
	return rec
}

func toJobRecords(char *godestone.Character) []contract.ClassJobRecord {
	jobs := make([]contract.ClassJobRecord, 0, len(char.ClassJobs))
	for _, j := range char.ClassJobs {
		jobs = append(jobs, contract.ClassJobRecord{
			CharacterID: char.ID,
			ClassJobID:  classJobKey(j),
			Name:        j.Name,
			Level:       j.Level,
			ExpLevel:    j.ExpLevel,
		})
	}
	return jobs
}

// classJobKey returns a stable per-entry key: JobID when present, else ClassID
// (a base class has no job crystal, so JobID is 0).
func classJobKey(j *godestone.ClassJob) uint8 {
	if j.JobID != 0 {
		return j.JobID
	}
	return j.ClassID
}
```

- [ ] **Step 4: Run to verify it passes, then commit**

```bash
go test ./domain/census/ -v
git add domain/census/service.go domain/census/service_test.go
git commit -m "feat(census): service character conversion and upsert"
```

---

### Task 4: CensusService — achievement processing + activity

**Files:**
- Modify: `domain/census/service.go`
- Test: `domain/census/service_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestService_ProcessAchievements(t *testing.T) {
	svc, chars := newTestService(t)
	// Register the chocobo milestone and one expansion milestone.
	svc.SyncMilestones(context.Background())

	earned := []*godestone.AchievementInfo{
		{NamedEntity: &models.NamedEntity{ID: 590, Name: "My Little Chocobo"}, Date: time.Now().Add(-48 * time.Hour)},
		{NamedEntity: &models.NamedEntity{ID: 999, Name: "Some Other Achievement"}, Date: time.Now().Add(-1 * time.Hour)},
	}
	all := &godestone.AllAchievementInfo{Private: false, TotalAchievements: 2, TotalAchievementPoints: 25}

	ms, err := svc.ProcessAchievements(context.Background(), 123, earned, all)
	if err != nil {
		t.Fatalf("ProcessAchievements: %v", err)
	}
	// Only the registered milestone (590) is kept; 999 is filtered out.
	if len(ms) != 1 || ms[0].AchievementID != 590 {
		t.Errorf("milestones = %+v, want only 590", ms)
	}
	// Latest achievement (any, not just milestone) is 999 at -1h.
	got, _ := chars.Get(context.Background(), 123)
	if got.LatestAchievementID == nil || *got.LatestAchievementID != 999 {
		t.Errorf("latest achievement = %v, want 999", got.LatestAchievementID)
	}
	if got.AchievementsPrivate {
		t.Error("achievements_private = true, want false")
	}
}

func TestService_ProcessAchievements_Private(t *testing.T) {
	svc, chars := newTestService(t)
	// Create the character first (summary update needs an existing row).
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 5, Name: "X", FirstSeenAt: time.Now()}, nil)

	all := &godestone.AllAchievementInfo{Private: true}
	if _, err := svc.ProcessAchievements(context.Background(), 5, nil, all); err != nil {
		t.Fatalf("ProcessAchievements: %v", err)
	}
	got, _ := chars.Get(context.Background(), 5)
	if !got.AchievementsPrivate {
		t.Error("achievements_private = false, want true")
	}
}

func TestService_IsActive(t *testing.T) {
	svc, _ := newTestService(t)
	now := time.Now().UTC()
	if !svc.IsActive(now.Add(-time.Hour)) {
		t.Error("achievement 1h ago should be active within 30d window")
	}
	if svc.IsActive(now.Add(-31 * 24 * time.Hour)) {
		t.Error("achievement 31d ago should not be active")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./domain/census/ -run 'TestService_ProcessAchievements|TestService_IsActive'
```

Expected: FAIL — `ProcessAchievements`/`IsActive` undefined.

- [ ] **Step 3: Implement**

Add to `service.go`:

```go
const defaultActivityWindow = 30 * 24 * time.Hour

// ProcessAchievements filters earned achievements against the registry, persists
// only the matching milestones, and updates the character's achievement summary
// (private flag + latest achievement, which may be any achievement). Returns the
// milestones that matched the registry.
func (s *Service) ProcessAchievements(ctx context.Context, charID uint32, earned []*godestone.AchievementInfo, all *godestone.AllAchievementInfo) ([]contract.CharacterMilestone, error) {
	registry, err := s.achievements.ListMilestones(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint32]contract.MilestoneAchievement, len(registry))
	for _, m := range registry {
		byID[m.AchievementID] = m
	}

	var milestones []contract.CharacterMilestone
	var latest *godestone.AchievementInfo
	for _, a := range earned {
		if a == nil {
			continue
		}
		if _, ok := byID[a.ID]; ok {
			milestones = append(milestones, contract.CharacterMilestone{
				CharacterID:   charID,
				AchievementID: a.ID,
				AchievedAt:    a.Date,
			})
		}
		if latest == nil || a.Date.After(latest.Date) {
			latest = a
		}
	}

	if err := s.achievements.UpsertCharacterMilestones(ctx, charID, milestones); err != nil {
		return nil, err
	}

	private := all != nil && all.Private
	var latestID *uint32
	var latestAt *time.Time
	if latest != nil {
		id := latest.ID
		at := latest.Date
		latestID = &id
		latestAt = &at
	}
	if err := s.characters.UpdateAchievementSummary(ctx, charID, private, latestID, latestAt); err != nil {
		return nil, err
	}
	return milestones, nil
}

// IsActive reports whether a latest-achievement timestamp falls within the
// census activity window (default 30 days).
func (s *Service) IsActive(latestAt time.Time) bool {
	return !latestAt.IsZero() && time.Since(latestAt) <= defaultActivityWindow
}
```

- [ ] **Step 4: Run to verify they pass, then commit**

```bash
go test ./domain/census/ -v
git add domain/census/service.go domain/census/service_test.go
git commit -m "feat(census): achievement processing, milestone filtering, activity"
```

---

### Task 5: Container wiring

**Files:**
- Modify: `container/domain.go`
- Test: `container/census_service_test.go`

- [ ] **Step 1: Write the failing test**

```go
package container

import (
	"path/filepath"
	"testing"
)

func TestServiceContainer_CensusService(t *testing.T) {
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "census.db"))
	Load = NewServiceContainer()

	svc := Load.CensusService()
	if svc == nil {
		t.Fatal("CensusService nil")
	}
	if Load.CensusService() != svc {
		t.Fatal("expected cached CensusService instance")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./container/ -run TestServiceContainer_CensusService
```

Expected: FAIL — `Load.CensusService` undefined.

- [ ] **Step 3: Implement**

`container/domain.go` becomes:

```go
package container

import (
	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// DomainContainer wires domain services.
type DomainContainer struct {
	censusService *census.Service
}

func (s *ServiceContainer) CensusService() *census.Service {
	if s.domain.censusService != nil {
		return s.domain.censusService
	}
	svc := census.NewService(
		s.CharacterRepository(),
		s.FreeCompanyRepository(),
		s.AchievementRepository(),
		s.CensusRunRepository(),
	)
	s.domain.censusService = svc
	return svc
}
```

Note: `CensusService()` returns the concrete `*census.Service` (not a contract interface) because the handlers live in the domain and need its concrete methods — matching how `cmd/` constructs domain objects directly per `docs/coding-style.md`.

- [ ] **Step 4: Run to verify it passes, then commit**

```bash
go test ./container/ -v
git add container/domain.go container/census_service_test.go
git commit -m "feat(container): census service accessor"
```

---

### Task 6: Documentation

**Files:**
- Modify: `docs/census.md`

- [ ] **Step 1: Update `docs/census.md`**

Mark the previously "not yet implemented" items now implemented: DC→region derivation (list the full DC→region table), the milestone registry (location, `MilestoneSet`, `SyncMilestones`, kind enum, and the XIVAPI verification method for IDs). Add a short "CensusService" section describing its methods (`SyncMilestones`, `UpsertCharacter`, `ProcessAchievements`, `IsActive`) and that handlers (next phase) will call it. Keep the "not yet implemented" list for: ingest handlers, aggregate/stats queries.

- [ ] **Step 2: Commit**

```bash
git add docs/census.md
git commit -m "docs: census service and milestone registry"
```

---

### Task 7: Final verification

- [ ] **Step 1: Full suite with race detector**

```bash
go test ./... -race
```

Expected: all PASS.

- [ ] **Step 2: Lint + build**

```bash
PATH="$HOME/go/bin:$PATH" make lint
make build
```

Expected: clean lint, `bin/ffxiv-census` produced.

- [ ] **Step 3: Commit any fixes**

```bash
git add -A && git commit -m "chore: census domain service verification"
```

---

## Implementation Gotchas

1. **`godestone.AchievementInfo` embeds `*models.NamedEntity`** (anonymous pointer field), so its fields (`ID`, `Name`, `Date`) are promoted: `a.ID`, `a.Name`, `a.Date` work directly. The test constructs them as `&godestone.AchievementInfo{NamedEntity: &models.NamedEntity{ID: 590, ...}, Date: ...}`.

2. **`gender.Gender` is `uint8`-based** (`gender.None/Male/Female`). Cast with `uint8(char.Gender)` when storing; `gender.None` is 0, matching the schema default.

3. **`godestone.Character` has pointer fields** (`Race`, `Tribe`, `GrandCompanyInfo`, and `GrandCompanyInfo.GrandCompany`). Nil-check each before dereferencing (the `TestService_UpsertCharacter_NilSafe` test enforces this).

4. **`MilestoneSet` is additive and initially small** (only chocobo 590 verified). Do NOT invent expansion/job IDs — fetch them from XIVAPI in Task 2 Step 1 and append. The registry can grow in later commits without a migration (sync is idempotent).

5. **`CensusService` returns `*census.Service`** (concrete), not a contract interface — consistent with `cmd/` constructing domain objects directly. Don't add a `CensusService` port to `port/contract`.

6. **The `mock/repository` fakes** (`NewCharacterFake`, etc.) are reused by the service tests — they already mirror SQL semantics (first_seen preservation, jobs replacement, deep-copy) from the previous phase's review fixes.
