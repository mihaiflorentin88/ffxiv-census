package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestCharacterRepository_UpsertAndGet(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewCharacterRepository(driver)
	ctx := context.Background()

	fcID := "fc-123"
	fcName := "Crystal Guard"
	rec := contract.CharacterRecord{
		ID:                  1001,
		Name:                "Tataru Taru",
		World:               "Louisoix",
		Datacenter:          "Chaos",
		Region:              "EU",
		Race:                "Lalafell",
		Tribe:               "Plainsfolk",
		Gender:              1,
		GrandCompany:        "Maelstrom",
		FreeCompanyID:       &fcID,
		FreeCompanyName:     &fcName,
		AvatarURL:           "https://avatar.url",
		PortraitURL:         "https://portrait.url",
		Bio:                 "Accounting master",
		ActiveJob:           "MIN",
		ItemLevel:           650,
		AchievementsPrivate: false,
		FirstSeenAt:         time.Now().UTC().Truncate(time.Millisecond),
	}

	jobs := []contract.ClassJobRecord{
		{CharacterID: 1001, ClassJobID: 1, Name: "Gladiator", Level: 50, ExpLevel: 100},
		{CharacterID: 1001, ClassJobID: 16, Name: "Miner", Level: 90, ExpLevel: 5000},
	}

	if err := repo.Upsert(ctx, rec, jobs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.Get(ctx, 1001)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected character, got nil")
	}
	if got.Name != rec.Name || got.World != rec.World {
		t.Errorf("got name=%q world=%q, want %q %q", got.Name, got.World, rec.Name, rec.World)
	}

	gotJobs, err := repo.GetJobs(ctx, 1001)
	if err != nil {
		t.Fatalf("GetJobs: %v", err)
	}
	if len(gotJobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(gotJobs))
	}
}

func TestCharacterRepository_ListAndCount(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewCharacterRepository(driver)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := uint32(1); i <= 5; i++ {
		_ = repo.Upsert(ctx, contract.CharacterRecord{
			ID:          i,
			Name:        "Warrior",
			World:       "Ragnarok",
			Datacenter:  "Chaos",
			Region:      "EU",
			FirstSeenAt: now,
		}, nil)
	}

	count, err := repo.Count(ctx, contract.CharacterFilter{World: "Ragnarok"})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected count=5, got %d", count)
	}

	list, err := repo.List(ctx, contract.CharacterFilter{World: "Ragnarok"}, 3, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 items, got %d", len(list))
	}
}

func stringPtr(s string) *string {
	return &s
}
