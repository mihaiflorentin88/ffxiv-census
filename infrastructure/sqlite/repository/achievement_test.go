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
