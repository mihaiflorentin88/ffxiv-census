package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	ls.FetchAchievementsFunc = func(ctx context.Context, id uint32, milestoneIDs []uint32) (*contract.AchievementSummary, error) {
		return &contract.AchievementSummary{
			Milestones: []contract.AchievementResult{
				{AchievementID: 590, Name: "My Little Chocobo", Earned: true, EarnedAt: now.Add(-time.Hour)},
			},
			LatestAchievement: &contract.AchievementResult{AchievementID: 590, Name: "My Little Chocobo", Earned: true, EarnedAt: now},
		}, nil
	}

	next, err := h.Handle(context.Background(), achievementPayload(123))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 0 {
		t.Fatalf("next jobs = %d, want 0 (leaf event)", len(next))
	}
	got, _ := chars.Get(context.Background(), 123)
	if got.LatestAchievementID == nil || *got.LatestAchievementID != 590 {
		t.Errorf("latest achievement = %v, want 590", got.LatestAchievementID)
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
	ls.FetchAchievementsFunc = func(ctx context.Context, id uint32, milestoneIDs []uint32) (*contract.AchievementSummary, error) {
		return nil, errors.New("boom")
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
	ls.FetchAchievementsFunc = func(ctx context.Context, id uint32, milestoneIDs []uint32) (*contract.AchievementSummary, error) {
		fetched = true
		return &contract.AchievementSummary{}, nil
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
	ls.FetchAchievementsFunc = func(ctx context.Context, id uint32, milestoneIDs []uint32) (*contract.AchievementSummary, error) {
		fetched = true
		return nil, nil
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
	ls.FetchAchievementsFunc = func(ctx context.Context, id uint32, milestoneIDs []uint32) (*contract.AchievementSummary, error) {
		fetched = true
		return &contract.AchievementSummary{}, nil
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
	ls.FetchAchievementsFunc = func(ctx context.Context, id uint32, milestoneIDs []uint32) (*contract.AchievementSummary, error) {
		fetched = true
		return &contract.AchievementSummary{
			Milestones: []contract.AchievementResult{
				{AchievementID: 1794, Name: "Stormblood", Earned: true, EarnedAt: now.Add(-time.Hour)},
				{AchievementID: 2298, Name: "Shadowbringers", Earned: true, EarnedAt: now.Add(-time.Hour)},
				{AchievementID: 2958, Name: "Endwalker", Earned: true, EarnedAt: now.Add(-time.Hour)},
				{AchievementID: 3496, Name: "Dawntrail", Earned: true, EarnedAt: now},
			},
			LatestAchievement: &contract.AchievementResult{AchievementID: 3496, Name: "Dawntrail", Earned: true, EarnedAt: now},
		}, nil
	}

	_, err := h.Handle(context.Background(), achievementPayload(400))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !fetched {
		t.Error("FetchAchievements was not called when milestones are missing")
	}
}
