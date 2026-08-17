package repository

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestAchievementRepo(t *testing.T) (contract.AchievementRepository, contract.CharacterRepository) {
	t.Helper()
	driver, cleanup := newTestDriver(t)
	t.Cleanup(cleanup)
	return NewAchievementRepository(driver), NewCharacterRepository(driver)
}

func expStr(s string) *string { return &s }

func TestAchievementRepository_SyncAndList(t *testing.T) {
	repo, _ := newTestAchievementRepo(t)
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
	repo, _ := newTestAchievementRepo(t)
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
	repo, charRepo := newTestAchievementRepo(t)
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

	// Upsert corresponding characters
	now := time.Now().UTC().Truncate(time.Millisecond)
	_ = charRepo.Upsert(context.Background(), contract.CharacterRecord{ID: 1, Name: "Char 1", World: "Balmung", FirstSeenAt: now}, nil)
	_ = charRepo.Upsert(context.Background(), contract.CharacterRecord{ID: 2, Name: "Char 2", World: "Mateus", FirstSeenAt: now}, nil)

	if err := repo.UpsertCharacterMilestones(context.Background(), 1, []contract.CharacterMilestone{
		{CharacterID: 1, AchievementID: 1139, AchievedAt: now},
		{CharacterID: 1, AchievementID: 1000, AchievedAt: now.Add(-time.Hour)},
		{CharacterID: 1, AchievementID: 1794, AchievedAt: now.Add(-2 * time.Hour)},
		{CharacterID: 1, AchievementID: 590, AchievedAt: now.Add(-3 * time.Hour)}, // chocobo, non-expansion
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

	// Filtered by World
	gotFiltered, err := repo.CountExpansionsFiltered(context.Background(), contract.CharacterFilter{World: "Balmung"})
	if err != nil {
		t.Fatalf("CountExpansionsFiltered: %v", err)
	}
	wantFiltered := []contract.ExpansionCount{
		{Expansion: "Heavensward", Count: 1},
		{Expansion: "Stormblood", Count: 1},
	}
	if len(gotFiltered) != len(wantFiltered) {
		t.Fatalf("filtered counts = %+v, want %+v", gotFiltered, wantFiltered)
	}
}

func TestAchievementRepository_NewCharactersAndChocobo(t *testing.T) {
	repo, charRepo := newTestAchievementRepo(t)
	day := func(y int, m time.Month, d, h int) time.Time {
		return time.Date(y, m, d, h, 0, 0, 0, time.UTC)
	}

	// Char 1: Chocobo milestone on 2026-08-01 (first_seen 2026-07-20) -> event_time 2026-08-01
	_ = charRepo.Upsert(context.Background(), contract.CharacterRecord{ID: 1, Name: "Char 1", World: "Balmung", FirstSeenAt: day(2026, 7, 20, 10)}, nil)
	_ = repo.UpsertCharacterMilestones(context.Background(), 1, []contract.CharacterMilestone{
		{CharacterID: 1, AchievementID: 590, AchievedAt: day(2026, 8, 1, 12)},
	})

	// Char 2: No milestone, first_seen on 2026-08-01 -> event_time 2026-08-01
	_ = charRepo.Upsert(context.Background(), contract.CharacterRecord{ID: 2, Name: "Char 2", World: "Balmung", FirstSeenAt: day(2026, 8, 1, 15)}, nil)

	// Char 3: No milestone, first_seen on 2026-08-02, World: Mateus -> event_time 2026-08-02
	_ = charRepo.Upsert(context.Background(), contract.CharacterRecord{ID: 3, Name: "Char 3", World: "Mateus", FirstSeenAt: day(2026, 8, 2, 8)}, nil)

	since := day(2026, 7, 25, 0)
	until := day(2026, 8, 3, 0)

	// All worlds
	days, err := repo.NewCharactersPerDay(context.Background(), since, until, contract.CharacterFilter{})
	if err != nil {
		t.Fatalf("NewCharactersPerDay: %v", err)
	}
	if len(days) != 2 || days[0].Count != 2 || days[1].Count != 1 {
		t.Fatalf("NewCharactersPerDay = %+v, want 2 on day 1 and 1 on day 2", days)
	}

	// Filtered by World
	daysBalmung, err := repo.NewCharactersPerDay(context.Background(), since, until, contract.CharacterFilter{World: "Balmung"})
	if err != nil {
		t.Fatalf("NewCharactersPerDay Balmung: %v", err)
	}
	if len(daysBalmung) != 1 || daysBalmung[0].Count != 2 {
		t.Fatalf("NewCharactersPerDay Balmung = %+v, want 2 on day 1", daysBalmung)
	}

	// CountChocoboMilestones
	chocoboCount, err := repo.CountChocoboMilestones(context.Background(), since, contract.CharacterFilter{})
	if err != nil {
		t.Fatalf("CountChocoboMilestones: %v", err)
	}
	if chocoboCount != 3 {
		t.Fatalf("CountChocoboMilestones = %d, want 3", chocoboCount)
	}
}
