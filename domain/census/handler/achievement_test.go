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
