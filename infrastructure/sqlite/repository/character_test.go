package repository

import (
	"context"
	"fmt"
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

func TestCharacterRepository_ListPagination(t *testing.T) {
	repo := newTestCharacterRepo(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, id := range []uint32{1, 2, 3} {
		rec := contract.CharacterRecord{ID: id, Name: fmt.Sprintf("C%d", id), FirstSeenAt: now}
		if err := repo.Upsert(context.Background(), rec, nil); err != nil {
			t.Fatalf("Upsert %d: %v", id, err)
		}
	}

	first, err := repo.List(context.Background(), 2, 0)
	if err != nil {
		t.Fatalf("List(2, 0): %v", err)
	}
	if len(first) != 2 || first[0].ID != 1 || first[1].ID != 2 {
		t.Errorf("List(2, 0) = ids %v, want [1 2]", idsOf(first))
	}

	rest, err := repo.List(context.Background(), 2, 2)
	if err != nil {
		t.Fatalf("List(2, 2): %v", err)
	}
	if len(rest) != 1 || rest[0].ID != 3 {
		t.Errorf("List(2, 2) = ids %v, want [3]", idsOf(rest))
	}

	// Offset past the end returns an empty slice, not an error.
	empty, err := repo.List(context.Background(), 2, 10)
	if err != nil {
		t.Fatalf("List(2, 10): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("List(2, 10) = %d rows, want 0", len(empty))
	}
}

func TestCharacterRepository_Counts(t *testing.T) {
	repo := newTestCharacterRepo(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, id := range []uint32{11, 12, 13} {
		rec := contract.CharacterRecord{ID: id, Name: fmt.Sprintf("C%d", id), FirstSeenAt: now}
		if err := repo.Upsert(context.Background(), rec, nil); err != nil {
			t.Fatalf("Upsert %d: %v", id, err)
		}
	}
	if err := repo.MarkDeleted(context.Background(), 13, now.Add(time.Hour)); err != nil {
		t.Fatalf("MarkDeleted: %v", err)
	}

	// One active (latest within the window), one inactive (latest before it).
	latest := uint32(590)
	recent := now.Add(-time.Hour)
	if err := repo.UpdateAchievementSummary(context.Background(), 11, false, &latest, &recent); err != nil {
		t.Fatalf("UpdateAchievementSummary(11): %v", err)
	}
	old := now.Add(-72 * time.Hour)
	if err := repo.UpdateAchievementSummary(context.Background(), 12, false, &latest, &old); err != nil {
		t.Fatalf("UpdateAchievementSummary(12): %v", err)
	}

	total, err := repo.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if total != 2 {
		t.Errorf("Count = %d, want 2 (deleted excluded)", total)
	}

	since := now.Add(-24 * time.Hour)
	active, err := repo.CountActive(context.Background(), since)
	if err != nil {
		t.Fatalf("CountActive: %v", err)
	}
	if active != 1 {
		t.Errorf("CountActive = %d, want 1 (only id 11 in window)", active)
	}
}

func TestCharacterRepository_Breakdown(t *testing.T) {
	repo := newTestCharacterRepo(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	latest := uint32(590)
	recent := now.Add(-time.Hour)
	old := now.Add(-72 * time.Hour)

	seed := func(id uint32, world string) {
		rec := contract.CharacterRecord{ID: id, Name: fmt.Sprintf("C%d", id), World: world,
			Datacenter: "Primal", Region: "NA", Race: "Hyur", FirstSeenAt: now}
		if err := repo.Upsert(context.Background(), rec, nil); err != nil {
			t.Fatalf("Upsert %d: %v", id, err)
		}
	}
	seed(21, "Ultros")
	seed(22, "Ultros")
	seed(23, "Mateus")
	seed(24, "Mateus")
	// Ultros: one active + one inactive; Mateus: one active + one deleted.
	if err := repo.UpdateAchievementSummary(context.Background(), 21, false, &latest, &recent); err != nil {
		t.Fatalf("UpdateAchievementSummary(21): %v", err)
	}
	if err := repo.UpdateAchievementSummary(context.Background(), 22, false, &latest, &old); err != nil {
		t.Fatalf("UpdateAchievementSummary(22): %v", err)
	}
	if err := repo.UpdateAchievementSummary(context.Background(), 23, false, &latest, &recent); err != nil {
		t.Fatalf("UpdateAchievementSummary(23): %v", err)
	}
	if err := repo.MarkDeleted(context.Background(), 24, now.Add(time.Hour)); err != nil {
		t.Fatalf("MarkDeleted: %v", err)
	}

	since := now.Add(-24 * time.Hour)
	groups, err := repo.Breakdown(context.Background(), "world", since)
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	want := []contract.GroupCount{
		{Key: "Ultros", Total: 2, Active: 1},
		{Key: "Mateus", Total: 1, Active: 1},
	}
	if len(groups) != len(want) {
		t.Fatalf("Breakdown = %+v, want %+v", groups, want)
	}
	for i := range want {
		if groups[i] != want[i] {
			t.Errorf("Breakdown[%d] = %+v, want %+v", i, groups[i], want[i])
		}
	}

	// Column whitelist: unknown columns are rejected, not interpolated.
	if _, err := repo.Breakdown(context.Background(), "name", since); err == nil {
		t.Error("Breakdown(name) = nil error, want invalid-column error")
	}
}

func TestCharacterRepository_NewPerDay(t *testing.T) {
	repo := newTestCharacterRepo(t)
	day := func(y int, m time.Month, d, h int) time.Time {
		return time.Date(y, m, d, h, 0, 0, 0, time.UTC)
	}
	seed := func(id uint32, firstSeen time.Time) {
		rec := contract.CharacterRecord{ID: id, Name: fmt.Sprintf("C%d", id), FirstSeenAt: firstSeen}
		if err := repo.Upsert(context.Background(), rec, nil); err != nil {
			t.Fatalf("Upsert %d: %v", id, err)
		}
	}
	seed(31, day(2026, 8, 1, 10)) // day 2026-08-01
	seed(32, day(2026, 8, 1, 15)) // day 2026-08-01
	seed(33, day(2026, 8, 2, 8))  // day 2026-08-02
	seed(34, day(2026, 7, 20, 9)) // before since -> excluded
	seed(35, day(2026, 8, 5, 9))  // after until -> excluded
	seed(36, day(2026, 8, 1, 11)) // in window but deleted -> excluded
	if err := repo.MarkDeleted(context.Background(), 36, day(2026, 8, 2, 12)); err != nil {
		t.Fatalf("MarkDeleted: %v", err)
	}

	since := day(2026, 7, 25, 0)
	until := day(2026, 8, 3, 0)
	days, err := repo.NewPerDay(context.Background(), since, until)
	if err != nil {
		t.Fatalf("NewPerDay: %v", err)
	}
	want := []contract.DailyCount{
		{Day: "2026-08-01", Count: 2},
		{Day: "2026-08-02", Count: 1},
	}
	if len(days) != len(want) {
		t.Fatalf("NewPerDay = %+v, want %+v", days, want)
	}
	for i := range want {
		if days[i] != want[i] {
			t.Errorf("NewPerDay[%d] = %+v, want %+v", i, days[i], want[i])
		}
	}
}

// idsOf returns the IDs of the given records for concise assertions.
func idsOf(recs []contract.CharacterRecord) []uint32 {
	out := make([]uint32, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}
