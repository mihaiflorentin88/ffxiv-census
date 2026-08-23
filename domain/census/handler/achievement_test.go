package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/xivapi/godestone/v2"
	"github.com/xivapi/godestone/v2/provider/models"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/mock"
	mocklodestone "github.com/mihaiflorentin88/ffxiv-census/mock/lodestone"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestAchievementCensus(t *testing.T) (*AchievementCensus, *mocklodestone.Fake, *mockrepo.CharacterRepository, *mockrepo.AchievementRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	chars := mockrepo.NewCharacterFake()
	ach := mockrepo.NewAchievementFake()
	svc := census.NewService(chars, ach, mockrepo.NewCensusRunFake())
	if err := svc.SyncMilestones(context.Background()); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	return NewAchievementCensus(ls, svc, nil), ls, chars, ach
}

func achievementPayload(characterID uint32) []byte {
	b, _ := json.Marshal(AchievementCensusPayload{CharacterID: characterID})
	return b
}

func TestAchievementCensus_Processes(t *testing.T) {
	h, ls, chars, ach := newTestAchievementCensus(t)
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 123, Name: "X", FirstSeenAt: time.Now()}, nil)

	now := time.Now()
	ls.FetchAchievementsFunc = func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
		return []*godestone.AchievementInfo{
			{NamedEntity: &models.NamedEntity{ID: 590, Name: "My Little Chocobo"}, Date: now.Add(-time.Hour)},
			{NamedEntity: &models.NamedEntity{ID: 999, Name: "Other"}, Date: now},
		}, &godestone.AllAchievementInfo{Private: false}, nil
	}

	next, err := h.Handle(context.Background(), achievementPayload(123))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 0 {
		t.Fatalf("next jobs = %d, want 0 (leaf event)", len(next))
	}
	// Latest achievement is 999 (any achievement), not just the milestone.
	got, _ := chars.Get(context.Background(), 123)
	if got.LatestAchievementID == nil || *got.LatestAchievementID != 999 {
		t.Errorf("latest achievement = %v, want 999", got.LatestAchievementID)
	}
	// Only the registered milestone (590) is recorded.
	milestones, err := ach.ListCharacterMilestones(context.Background(), 123)
	if err != nil {
		t.Fatalf("ListCharacterMilestones: %v", err)
	}
	if len(milestones) != 1 || milestones[0].AchievementID != 590 {
		t.Errorf("milestones = %+v, want only 590", milestones)
	}
}

func TestAchievementCensus_FetchError(t *testing.T) {
	h, ls, _, _ := newTestAchievementCensus(t)
	ls.FetchAchievementsFunc = func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
		return nil, nil, errors.New("boom")
	}
	if _, err := h.Handle(context.Background(), achievementPayload(1)); err == nil {
		t.Fatal("expected error on fetch failure")
	}
}

func TestAchievementCensus_WaitsForRateLimitedLodestone(t *testing.T) {
	ls := mocklodestone.NewFake()
	svc := census.NewService(mockrepo.NewCharacterFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	if err := svc.SyncMilestones(context.Background()); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	rl := mock.NewProviderRateLimiter()
	rl.Pause(contract.ProviderLodestone, 100*time.Millisecond, "test pause")

	var fetched bool
	ls.FetchAchievementsFunc = func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
		fetched = true
		return []*godestone.AchievementInfo{}, &godestone.AllAchievementInfo{Private: false}, nil
	}

	h := NewAchievementCensus(ls, svc, nil, rl)
	start := time.Now()
	_, err := h.Handle(context.Background(), achievementPayload(1))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !fetched {
		t.Fatal("FetchAchievements was not called after wait")
	}
	if elapsed < 90*time.Millisecond {
		t.Errorf("Handle returned too quickly (%v), expected wait ~100ms", elapsed)
	}
}

func TestAchievementCensus_SetsStopFn(t *testing.T) {
	h, ls, _, _ := newTestAchievementCensus(t)

	// Capture the stop function that the handler sets on the mock.
	// The handler calls SetAchievementStopFn before FetchAchievements,
	// so we capture it inside the FetchAchievementsFunc callback.
	var capturedStopFn func([]*godestone.AchievementInfo) bool
	ls.FetchAchievementsFunc = func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
		capturedStopFn = ls.StopFn
		return []*godestone.AchievementInfo{}, &godestone.AllAchievementInfo{Private: false}, nil
	}

	_, err := h.Handle(context.Background(), achievementPayload(1))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if capturedStopFn == nil {
		t.Fatal("handler did not set stop function before FetchAchievements")
	}

	// The stop function should return false when no milestones are found.
	if capturedStopFn([]*godestone.AchievementInfo{
		{NamedEntity: &models.NamedEntity{ID: 100}},
	}) {
		t.Error("stopFn returned true for non-milestone achievements")
	}

	// The stop function should return true when all milestones are found.
	// DefaultExpansions has 7 milestones; simulate finding them all.
	milestoneIDs := []uint32{590, 1129, 1139, 1794, 2298, 2958, 3496}
	page := make([]*godestone.AchievementInfo, len(milestoneIDs))
	for i, id := range milestoneIDs {
		page[i] = &godestone.AchievementInfo{NamedEntity: &models.NamedEntity{ID: id}}
	}
	if !capturedStopFn(page) {
		t.Error("stopFn returned false when all milestones found")
	}
}

func TestAchievementCensus_PreSeededStopFn(t *testing.T) {
	h, ls, chars, ach := newTestAchievementCensus(t)
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 100, Name: "X", FirstSeenAt: time.Now()}, nil)

	// Pre-seed 5 of 7 milestones in the DB.
	now := time.Now()
	preSeeded := []contract.CharacterMilestone{
		{CharacterID: 100, AchievementID: 590, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 100, AchievementID: 1129, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 100, AchievementID: 1139, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 100, AchievementID: 1794, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 100, AchievementID: 2298, AchievedAt: now.Add(-48 * time.Hour)},
	}
	if err := ach.UpsertCharacterMilestones(context.Background(), 100, preSeeded); err != nil {
		t.Fatalf("seed milestones: %v", err)
	}

	// Capture the stop function to verify pre-seeding.
	var capturedStopFn func([]*godestone.AchievementInfo) bool
	ls.FetchAchievementsFunc = func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
		capturedStopFn = ls.StopFn
		// Return only the 2 missing milestones.
		return []*godestone.AchievementInfo{
			{NamedEntity: &models.NamedEntity{ID: 2958}, Date: now.Add(-time.Hour)},
			{NamedEntity: &models.NamedEntity{ID: 3496}, Date: now},
		}, &godestone.AllAchievementInfo{Private: false}, nil
	}

	_, err := h.Handle(context.Background(), achievementPayload(100))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if capturedStopFn == nil {
		t.Fatal("handler did not set stop function")
	}

	// The stop function should return true when only the 2 missing milestones are found,
	// because the 5 pre-seeded ones are already counted. This is the key optimization:
	// the scraper only needs to find 2 milestones instead of all 7.
	missingPage := []*godestone.AchievementInfo{
		{NamedEntity: &models.NamedEntity{ID: 2958}},
		{NamedEntity: &models.NamedEntity{ID: 3496}},
	}
	if !capturedStopFn(missingPage) {
		t.Error("stopFn returned false when missing milestones found (pre-seeded should be counted)")
	}
}

func TestAchievementCensus_SkipsWhenAllMilestonesKnownAndFresh(t *testing.T) {
	h, ls, chars, ach := newTestAchievementCensus(t)
	now := time.Now()
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:                  200,
		Name:                "Fresh",
		FirstSeenAt:         now,
		LatestAchievementAt: &now, // Fresh data
	}, nil)

	// Pre-seed all 7 milestones.
	allMilestones := []contract.CharacterMilestone{
		{CharacterID: 200, AchievementID: 590, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 200, AchievementID: 1129, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 200, AchievementID: 1139, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 200, AchievementID: 1794, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 200, AchievementID: 2298, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 200, AchievementID: 2958, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 200, AchievementID: 3496, AchievedAt: now.Add(-48 * time.Hour)},
	}
	if err := ach.UpsertCharacterMilestones(context.Background(), 200, allMilestones); err != nil {
		t.Fatalf("seed milestones: %v", err)
	}

	var fetched bool
	ls.FetchAchievementsFunc = func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
		fetched = true
		return nil, nil, nil
	}

	next, err := h.Handle(context.Background(), achievementPayload(200))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 0 {
		t.Fatalf("next jobs = %d, want 0", len(next))
	}
	if fetched {
		t.Error("FetchAchievements was called when all milestones known and data is fresh")
	}
}

func TestAchievementCensus_ScrapesWhenDataStale(t *testing.T) {
	h, ls, chars, ach := newTestAchievementCensus(t)
	now := time.Now()
	stale := now.Add(-10 * 24 * time.Hour) // 10 days old > 7-day threshold
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:                  300,
		Name:                "Stale",
		FirstSeenAt:         now,
		LatestAchievementAt: &stale,
	}, nil)

	// Pre-seed all 7 milestones.
	allMilestones := []contract.CharacterMilestone{
		{CharacterID: 300, AchievementID: 590, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 300, AchievementID: 1129, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 300, AchievementID: 1139, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 300, AchievementID: 1794, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 300, AchievementID: 2298, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 300, AchievementID: 2958, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 300, AchievementID: 3496, AchievedAt: now.Add(-48 * time.Hour)},
	}
	if err := ach.UpsertCharacterMilestones(context.Background(), 300, allMilestones); err != nil {
		t.Fatalf("seed milestones: %v", err)
	}

	var fetched bool
	ls.FetchAchievementsFunc = func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
		fetched = true
		return []*godestone.AchievementInfo{}, &godestone.AllAchievementInfo{Private: false}, nil
	}

	_, err := h.Handle(context.Background(), achievementPayload(300))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !fetched {
		t.Error("FetchAchievements was not called when data is stale")
	}
}

func TestAchievementCensus_ScrapesWhenMilestonesMissing(t *testing.T) {
	h, ls, chars, ach := newTestAchievementCensus(t)
	now := time.Now()
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:                  400,
		Name:                "Partial",
		FirstSeenAt:         now,
		LatestAchievementAt: &now,
	}, nil)

	// Pre-seed only 3 of 7 milestones.
	partialMilestones := []contract.CharacterMilestone{
		{CharacterID: 400, AchievementID: 590, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 400, AchievementID: 1129, AchievedAt: now.Add(-48 * time.Hour)},
		{CharacterID: 400, AchievementID: 1139, AchievedAt: now.Add(-48 * time.Hour)},
	}
	if err := ach.UpsertCharacterMilestones(context.Background(), 400, partialMilestones); err != nil {
		t.Fatalf("seed milestones: %v", err)
	}

	var fetched bool
	ls.FetchAchievementsFunc = func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
		fetched = true
		return []*godestone.AchievementInfo{
			{NamedEntity: &models.NamedEntity{ID: 1794}, Date: now.Add(-time.Hour)},
			{NamedEntity: &models.NamedEntity{ID: 2298}, Date: now.Add(-time.Hour)},
			{NamedEntity: &models.NamedEntity{ID: 2958}, Date: now.Add(-time.Hour)},
			{NamedEntity: &models.NamedEntity{ID: 3496}, Date: now},
		}, &godestone.AllAchievementInfo{Private: false}, nil
	}

	_, err := h.Handle(context.Background(), achievementPayload(400))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !fetched {
		t.Error("FetchAchievements was not called when milestones are missing")
	}
}
