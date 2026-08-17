package census

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/xivapi/godestone/v2"
	"github.com/xivapi/godestone/v2/data/gender"
	"github.com/xivapi/godestone/v2/provider/models"

	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestService(t *testing.T) (*Service, *mockrepo.CharacterRepository) {
	t.Helper()
	chars := mockrepo.NewCharacterFake()
	svc := NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return svc, chars
}

// newTestServiceAll returns the service plus every fake it depends on, so
// tests can seed jobs/milestones/free companies directly.
func newTestServiceAll(t *testing.T) (*Service, *mockrepo.CharacterRepository, *mockrepo.FreeCompanyRepository, *mockrepo.AchievementRepository) {
	t.Helper()
	chars := mockrepo.NewCharacterFake()
	fcs := mockrepo.NewFreeCompanyFake()
	ach := mockrepo.NewAchievementFake()
	svc := NewService(chars, fcs, ach, mockrepo.NewCensusRunFake())
	return svc, chars, fcs, ach
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

func TestService_ProcessAchievements(t *testing.T) {
	svc, chars := newTestService(t)
	if err := svc.SyncMilestones(context.Background()); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	// The character must exist before the summary update.
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 123, Name: "X", FirstSeenAt: time.Now()}, nil)

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

func TestService_IsActive_ZeroTime(t *testing.T) {
	svc, _ := newTestService(t)
	if svc.IsActive(time.Time{}) {
		t.Error("zero time should not be active")
	}
}

func TestService_ProcessAchievements_PreservesMilestonesOnPrivate(t *testing.T) {
	svc, chars := newTestService(t)
	if err := svc.SyncMilestones(context.Background()); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 7, Name: "X", FirstSeenAt: time.Now()}, nil)

	// First, process public achievements so a milestone + latest are recorded.
	earned := []*godestone.AchievementInfo{
		{NamedEntity: &models.NamedEntity{ID: 590, Name: "My Little Chocobo"}, Date: time.Now()},
	}
	if _, err := svc.ProcessAchievements(context.Background(), 7, earned, &godestone.AllAchievementInfo{}); err != nil {
		t.Fatalf("ProcessAchievements (public): %v", err)
	}

	// Now the profile goes private: milestones and latest must be preserved.
	if _, err := svc.ProcessAchievements(context.Background(), 7, nil, &godestone.AllAchievementInfo{Private: true}); err != nil {
		t.Fatalf("ProcessAchievements (private): %v", err)
	}
	got, _ := chars.Get(context.Background(), 7)
	if !got.AchievementsPrivate {
		t.Error("achievements_private = false, want true")
	}
	if got.LatestAchievementID == nil || *got.LatestAchievementID != 590 {
		t.Errorf("latest achievement was wiped on private turn: %v", got.LatestAchievementID)
	}
	milestones, err := svc.achievements.ListCharacterMilestones(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListCharacterMilestones: %v", err)
	}
	if len(milestones) != 1 || milestones[0].AchievementID != 590 {
		t.Errorf("milestones = %+v, want only 590 preserved", milestones)
	}
}

func TestService_ProcessAchievements_EmptyRegistry(t *testing.T) {
	svc, chars := newTestService(t)
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 9, Name: "X", FirstSeenAt: time.Now()}, nil)

	// No SyncMilestones: registry is empty, processing must error rather than wipe.
	if _, err := svc.ProcessAchievements(context.Background(), 9, nil, &godestone.AllAchievementInfo{}); err == nil {
		t.Fatal("expected error for empty milestone registry")
	}
}

func TestService_Summary(t *testing.T) {
	svc, chars := newTestService(t)
	svc.SetActivityWindow(30 * 24 * time.Hour)
	ctx := context.Background()
	now := time.Now().UTC()
	active := now.Add(-time.Hour)
	inactive := now.Add(-60 * 24 * time.Hour)

	seed := func(id uint32, lat *time.Time) {
		_ = chars.Upsert(ctx, contract.CharacterRecord{ID: id, Name: fmt.Sprintf("c%d", id), World: "Ultros", FirstSeenAt: now}, nil)
		if lat != nil {
			_ = chars.UpdateAchievementSummary(ctx, id, false, nil, lat)
		}
	}
	seed(1, &active)   // active within window
	seed(2, &inactive) // outside window
	seed(3, nil)       // never achievement-censused
	seed(4, &active)   // active, but deleted below
	_ = chars.MarkDeleted(ctx, 4, now)

	total, gotActive, err := svc.Summary(ctx)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (deleted excluded)", total)
	}
	if gotActive != 1 {
		t.Errorf("active = %d, want 1", gotActive)
	}
}

func TestService_ListCharacters_Pagination(t *testing.T) {
	svc, chars := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, id := range []uint32{1, 2, 3} {
		_ = chars.Upsert(ctx, contract.CharacterRecord{ID: id, Name: fmt.Sprintf("c%d", id), World: "Ultros", FirstSeenAt: now}, nil)
	}
	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 4, Name: "gone", World: "Ultros", FirstSeenAt: now}, nil)
	_ = chars.MarkDeleted(ctx, 4, now)

	page, total, err := svc.ListCharacters(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListCharacters: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (deleted excluded)", total)
	}
	if len(page) != 2 || page[0].ID != 1 || page[1].ID != 2 {
		t.Errorf("page(2,0) = %+v, want ids [1 2]", page)
	}

	page, total, err = svc.ListCharacters(ctx, 2, 2)
	if err != nil {
		t.Fatalf("ListCharacters: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(page) != 1 || page[0].ID != 3 {
		t.Errorf("page(2,2) = %+v, want id [3]", page)
	}
}

func TestService_CharacterDetail_Missing(t *testing.T) {
	svc, _ := newTestService(t)
	got, err := svc.CharacterDetail(context.Background(), 999)
	if err != nil {
		t.Fatalf("CharacterDetail: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil for unknown id", got)
	}
}

func TestService_CharacterDetail_WithFreeCompany(t *testing.T) {
	svc, chars, fcs, ach := newTestServiceAll(t)
	ctx := context.Background()
	now := time.Now().UTC()
	fcID := "9234567890123456789"
	fcName := "The Scions"
	_ = chars.Upsert(ctx, contract.CharacterRecord{
		ID: 123, Name: "Tataru Taru", World: "Ultros", Datacenter: "Primal", Region: "NA",
		Race: "Lalafell", Gender: 2, FirstSeenAt: now,
		FreeCompanyID:       &fcID,
		FreeCompanyName:     &fcName,
		LatestAchievementAt: &now,
	}, []contract.ClassJobRecord{
		{CharacterID: 123, ClassJobID: 19, Name: "Paladin", Level: 90, ExpLevel: 12345},
	})
	_ = fcs.Upsert(ctx, contract.FreeCompanyRecord{ID: fcID, Name: fcName, World: "Ultros", Datacenter: "Primal", MemberCount: 100, LastSeenAt: now})
	_ = ach.UpsertCharacterMilestones(ctx, 123, []contract.CharacterMilestone{
		{CharacterID: 123, AchievementID: 590, AchievedAt: now},
	})

	got, err := svc.CharacterDetail(ctx, 123)
	if err != nil {
		t.Fatalf("CharacterDetail: %v", err)
	}
	if got == nil {
		t.Fatal("CharacterDetail = nil, want detail")
	}
	if got.Character.ID != 123 || got.Character.Name != "Tataru Taru" {
		t.Errorf("Character = %+v", got.Character)
	}
	if len(got.Jobs) != 1 || got.Jobs[0].ClassJobID != 19 || got.Jobs[0].Level != 90 {
		t.Errorf("Jobs = %+v", got.Jobs)
	}
	if len(got.Milestones) != 1 || got.Milestones[0].AchievementID != 590 {
		t.Errorf("Milestones = %+v", got.Milestones)
	}
	if got.FreeCompany == nil || got.FreeCompany.ID != fcID || got.FreeCompany.Name != fcName {
		t.Errorf("FreeCompany = %+v, want id %q", got.FreeCompany, fcID)
	}
}

func TestService_CharacterDetail_MissingFreeCompany(t *testing.T) {
	svc, chars, _, _ := newTestServiceAll(t)
	ctx := context.Background()
	fcID := "9234567890123456789"
	_ = chars.Upsert(ctx, contract.CharacterRecord{ID: 7, Name: "X", World: "Ultros", FirstSeenAt: time.Now(), FreeCompanyID: &fcID}, nil)

	got, err := svc.CharacterDetail(ctx, 7)
	if err != nil {
		t.Fatalf("CharacterDetail: %v", err)
	}
	if got == nil {
		t.Fatal("CharacterDetail = nil, want detail")
	}
	if got.FreeCompany != nil {
		t.Errorf("FreeCompany = %+v, want nil when the FC was never ingested", got.FreeCompany)
	}
}

func TestService_Breakdown_InvalidDimension(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Breakdown(context.Background(), "level")
	if !errors.Is(err, ErrInvalidDimension) {
		t.Fatalf("err = %v, want ErrInvalidDimension", err)
	}
}

func TestService_Breakdown_Delegates(t *testing.T) {
	svc, chars := newTestService(t)
	svc.SetActivityWindow(30 * 24 * time.Hour)
	ctx := context.Background()
	now := time.Now().UTC()
	active := now.Add(-time.Hour)
	inactive := now.Add(-60 * 24 * time.Hour)

	seed := func(id uint32, world string, lat *time.Time) {
		_ = chars.Upsert(ctx, contract.CharacterRecord{ID: id, Name: fmt.Sprintf("c%d", id), World: world, FirstSeenAt: now}, nil)
		if lat != nil {
			_ = chars.UpdateAchievementSummary(ctx, id, false, nil, lat)
		}
	}
	seed(1, "Ultros", &active)
	seed(2, "Ultros", &inactive)
	seed(3, "Leviathan", &active)

	got, err := svc.Breakdown(ctx, "world")
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	want := []contract.GroupCount{
		{Key: "Ultros", Total: 2, Active: 1},
		{Key: "Leviathan", Total: 1, Active: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Breakdown = %+v, want %+v", got, want)
	}
}

func TestService_NewCharacters(t *testing.T) {
	svc, chars := newTestService(t)
	ctx := context.Background()
	mk := func(day int) time.Time { return time.Date(2026, 8, day, 12, 0, 0, 0, time.UTC) }
	seed := func(id uint32, firstSeen time.Time) {
		_ = chars.Upsert(ctx, contract.CharacterRecord{ID: id, Name: fmt.Sprintf("c%d", id), World: "Ultros", FirstSeenAt: firstSeen}, nil)
	}
	seed(1, mk(11))
	seed(2, mk(11))
	seed(3, mk(13))
	seed(4, mk(1))  // before since: excluded
	seed(5, mk(11)) // deleted: excluded
	_ = chars.MarkDeleted(ctx, 5, mk(11))

	got, err := svc.NewCharacters(ctx, mk(10), mk(14))
	if err != nil {
		t.Fatalf("NewCharacters: %v", err)
	}
	want := []contract.DailyCount{
		{Day: "2026-08-11", Count: 2},
		{Day: "2026-08-13", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewCharacters = %+v, want %+v", got, want)
	}
}

func TestService_ExpansionCompletions(t *testing.T) {
	svc, _, _, ach := newTestServiceAll(t)
	ctx := context.Background()
	if err := svc.SyncMilestones(ctx); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	now := time.Now().UTC()
	_ = ach.UpsertCharacterMilestones(ctx, 1, []contract.CharacterMilestone{
		{CharacterID: 1, AchievementID: 1139, AchievedAt: now}, // Heavensward
		{CharacterID: 1, AchievementID: 1794, AchievedAt: now}, // Stormblood
	})
	_ = ach.UpsertCharacterMilestones(ctx, 2, []contract.CharacterMilestone{
		{CharacterID: 2, AchievementID: 1139, AchievedAt: now},
	})
	_ = ach.UpsertCharacterMilestones(ctx, 3, []contract.CharacterMilestone{
		{CharacterID: 3, AchievementID: 590, AchievedAt: now}, // non-expansion milestone
	})

	got, err := svc.ExpansionCompletions(ctx)
	if err != nil {
		t.Fatalf("ExpansionCompletions: %v", err)
	}
	want := []contract.ExpansionCount{
		{Expansion: "Heavensward", Count: 2},
		{Expansion: "Stormblood", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExpansionCompletions = %+v, want %+v", got, want)
	}
}

func TestService_SetActivityWindow_Noop(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SetActivityWindow(0)
	svc.SetActivityWindow(-time.Hour)
	if !svc.IsActive(time.Now().UTC().Add(-15 * 24 * time.Hour)) {
		t.Error("no-op SetActivityWindow should keep the default 30-day window")
	}
}

func TestService_IsActive_ConfiguredWindow(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SetActivityWindow(7 * 24 * time.Hour)
	now := time.Now().UTC()
	if !svc.IsActive(now.Add(-6 * 24 * time.Hour)) {
		t.Error("achievement 6d ago should be active within a 7d window")
	}
	if svc.IsActive(now.Add(-8 * 24 * time.Hour)) {
		t.Error("achievement 8d ago should not be active within a 7d window")
	}
}
