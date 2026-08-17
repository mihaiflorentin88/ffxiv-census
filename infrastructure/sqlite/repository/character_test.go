package repository

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

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

func TestCharacterRepository_UpsertPreservesFirstSeenAndClearsDeleted(t *testing.T) {
	repo := newTestCharacterRepo(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	firstSeen := now.Add(-48 * time.Hour)
	rec := contract.CharacterRecord{ID: 777, Name: "A", FirstSeenAt: firstSeen, LastCensusAt: &firstSeen}
	if err := repo.Upsert(context.Background(), rec, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	deletedAt := now.Add(-time.Hour)
	if err := repo.MarkDeleted(context.Background(), 777, deletedAt); err != nil {
		t.Fatalf("MarkDeleted: %v", err)
	}
	// Re-upsert with a fresh census: deleted_at cleared, first_seen_at preserved.
	reCensus := now
	if err := repo.Upsert(context.Background(),
		contract.CharacterRecord{ID: 777, Name: "B", FirstSeenAt: now, LastCensusAt: &reCensus}, nil); err != nil {
		t.Fatalf("Upsert (re): %v", err)
	}
	got, err := repo.Get(context.Background(), 777)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected character, got nil")
	}
	if got.DeletedAt != nil {
		t.Errorf("deleted_at = %v, want nil after re-upsert", got.DeletedAt)
	}
	if !got.FirstSeenAt.Equal(firstSeen) {
		t.Errorf("first_seen_at = %v, want preserved %v", got.FirstSeenAt, firstSeen)
	}
	if got.Name != "B" {
		t.Errorf("name = %q, want updated %q", got.Name, "B")
	}
}

func TestCharacterRepository_UpsertCollidingJobs(t *testing.T) {
	repo := newTestCharacterRepo(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	rec := contract.CharacterRecord{ID: 999, Name: "Collide", FirstSeenAt: now}
	// Two entries mapping to the same class_job_id key (godestone class/job
	// pairing). The upsert must not fail and must collapse to one row.
	jobs := []contract.ClassJobRecord{
		{CharacterID: 999, ClassJobID: 8, Name: "Carpenter", Level: 1, ExpLevel: 0},
		{CharacterID: 999, ClassJobID: 8, Name: "Carpenter", Level: 90, ExpLevel: 555},
	}
	if err := repo.Upsert(context.Background(), rec, jobs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	gotJobs, err := repo.GetJobs(context.Background(), 999)
	if err != nil {
		t.Fatalf("GetJobs: %v", err)
	}
	if len(gotJobs) != 1 {
		t.Fatalf("jobs = %d, want 1 (collision collapsed)", len(gotJobs))
	}
	if gotJobs[0].Level != 90 || gotJobs[0].ExpLevel != 555 {
		t.Errorf("job = %+v, want later entry's values (level 90, exp 555)", gotJobs[0])
	}
}

func TestCharacterRepository_UpsertReplacesJobs(t *testing.T) {
	repo := newTestCharacterRepo(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	rec := contract.CharacterRecord{ID: 888, Name: "C", FirstSeenAt: now}
	jobs := []contract.ClassJobRecord{{CharacterID: 888, ClassJobID: 1, Name: "Gladiator", Level: 1}}
	if err := repo.Upsert(context.Background(), rec, jobs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// Re-upsert with nil jobs: the job set must be wiped.
	if err := repo.Upsert(context.Background(), rec, nil); err != nil {
		t.Fatalf("Upsert (re): %v", err)
	}
	gotJobs, err := repo.GetJobs(context.Background(), 888)
	if err != nil {
		t.Fatalf("GetJobs: %v", err)
	}
	if len(gotJobs) != 0 {
		t.Errorf("jobs = %d, want 0 after nil-jobs upsert", len(gotJobs))
	}
}
