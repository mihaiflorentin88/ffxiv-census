package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestAchievementRepository_SyncAndListMilestones(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewAchievementRepository(driver)
	ctx := context.Background()

	arrExp := "A Realm Reborn"
	registry := []contract.MilestoneAchievement{
		{AchievementID: 1129, Kind: "expansion", Expansion: &arrExp, Detail: "Before the Dawn"},
		{AchievementID: 590, Kind: "chocobo", Expansion: nil, Detail: "My Little Chocobo"},
	}

	if err := repo.SyncMilestones(ctx, registry); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}

	list, err := repo.ListMilestones(ctx)
	if err != nil {
		t.Fatalf("ListMilestones: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 milestones, got %d", len(list))
	}
}

func TestAchievementRepository_CharacterMilestones(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewAchievementRepository(driver)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Millisecond)
	milestones := []contract.CharacterMilestone{
		{CharacterID: 555, AchievementID: 1129, AchievedAt: now},
		{CharacterID: 555, AchievementID: 590, AchievedAt: now.Add(-24 * time.Hour)},
	}

	if err := repo.UpsertCharacterMilestones(ctx, 555, milestones); err != nil {
		t.Fatalf("UpsertCharacterMilestones: %v", err)
	}

	got, err := repo.ListCharacterMilestones(ctx, 555)
	if err != nil {
		t.Fatalf("ListCharacterMilestones: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 character milestones, got %d", len(got))
	}
}
