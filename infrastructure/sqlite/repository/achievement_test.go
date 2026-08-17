package repository

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestAchievementRepo(t *testing.T) contract.AchievementRepository {
	t.Helper()
	driver, cleanup := newTestDriver(t)
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

func TestAchievementRepository_CountExpansions(t *testing.T) {
	repo := newTestAchievementRepo(t)
	registry := []contract.MilestoneAchievement{
		{AchievementID: 1139, Kind: contract.MilestoneKindExpansion, Expansion: expStr("Heavensward"), Detail: "Heavensward"},
		// Second registry entry mapping to the same expansion: a character who
		// earns both produces two join rows, proving COUNT(DISTINCT character_id).
		{AchievementID: 1000, Kind: contract.MilestoneKindExpansion, Expansion: expStr("Heavensward"), Detail: "Heavensward 2.1"},
		{AchievementID: 1794, Kind: contract.MilestoneKindExpansion, Expansion: expStr("Stormblood"), Detail: "Stormblood"},
		// Non-expansion milestone: must be excluded from the counts.
		{AchievementID: 590, Kind: contract.MilestoneKindChocobo, Detail: "My Little Chocobo"},
	}
	if err := repo.SyncMilestones(context.Background(), registry); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	// Character 1 earned Heavensward twice (achievements 1139 and 1000) plus
	// Stormblood; the duplicate must collapse via COUNT(DISTINCT character_id).
	// Character 2 earned Heavensward once.
	if err := repo.UpsertCharacterMilestones(context.Background(), 1, []contract.CharacterMilestone{
		{CharacterID: 1, AchievementID: 1139, AchievedAt: now},
		{CharacterID: 1, AchievementID: 1000, AchievedAt: now.Add(-time.Hour)},
		{CharacterID: 1, AchievementID: 1794, AchievedAt: now.Add(-2 * time.Hour)},
	}); err != nil {
		t.Fatalf("UpsertCharacterMilestones(1): %v", err)
	}
	if err := repo.UpsertCharacterMilestones(context.Background(), 2, []contract.CharacterMilestone{
		{CharacterID: 2, AchievementID: 1139, AchievedAt: now.Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("UpsertCharacterMilestones(2): %v", err)
	}

	got, err := repo.CountExpansions(context.Background())
	if err != nil {
		t.Fatalf("CountExpansions: %v", err)
	}
	want := []contract.ExpansionCount{
		{Expansion: "Heavensward", Count: 2}, // characters 1 and 2 (char 1's two rows counted once)
		{Expansion: "Stormblood", Count: 1},  // character 1 only
	}
	if len(got) != len(want) {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("counts[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
