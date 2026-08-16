package repository

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func strPtr(s string) *string { return &s }

func TestCharacterRepository_UpsertAndGet(t *testing.T) {
	repo := newTestCharacterRepo(t)

	now := time.Now().UTC().Truncate(time.Millisecond)
	fc := "9234567890123456789"
	rec := contract.CharacterRecord{
		ID:            12345678,
		Name:          "Tataru Taru",
		World:         "Ultros",
		Datacenter:    "Primal",
		Region:        "NA",
		Race:          "Lalafell",
		Tribe:         "Dunesfolk",
		Gender:        2,
		GrandCompany:  "Maelstrom",
		FreeCompanyID: &fc,
		FirstSeenAt:   now,
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
	repo := newTestCharacterRepo(t)
	got, err := repo.Get(context.Background(), 99999999)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing character, got %+v", got)
	}
}

func TestCharacterRepository_MarkDeleted(t *testing.T) {
	repo := newTestCharacterRepo(t)
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
	repo := newTestCharacterRepo(t)
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
	repo := newTestCharacterRepo(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	old := now.Add(-48 * time.Hour)
	if err := repo.Upsert(context.Background(),
		contract.CharacterRecord{ID: 301, Name: "A", FirstSeenAt: old, LastCensusAt: &old}, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
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
