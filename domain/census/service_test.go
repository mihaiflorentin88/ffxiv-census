package census

import (
	"context"
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
