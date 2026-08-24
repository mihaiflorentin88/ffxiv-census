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
	mocktomestone "github.com/mihaiflorentin88/ffxiv-census/mock/tomestone"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestCharacterCensus(t *testing.T) (*CharacterCensus, *mocklodestone.Fake, *mockrepo.CharacterRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewCharacterCensus(ls, nil, svc, nil), ls, chars
}

func newTestDualCharacterCensus(t *testing.T) (*CharacterCensus, *mocklodestone.Fake, *mocktomestone.Fake, *mock.ProviderRateLimiter, *mockrepo.CharacterRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	ts := mocktomestone.NewFake()
	limiter := mock.NewProviderRateLimiter()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewCharacterCensus(ls, ts, svc, nil, limiter), ls, ts, limiter, chars
}

func characterPayload(id uint32) []byte {
	b, _ := json.Marshal(CharacterCensusPayload{CharacterID: id})
	return b
}

func TestCharacterCensus_UpsertAndChain(t *testing.T) {
	h, ls, chars := newTestCharacterCensus(t)
	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		return &contract.CharacterProfile{ID: id, Name: "Char", World: "Ultros", Datacenter: "Primal", Race: "Hyur", FreeCompanyID: "9234567890123456789"}, nil
	}
	next, err := h.Handle(context.Background(), characterPayload(42))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1 (achievement)", len(next))
	}
	if next[0].Type != EventAchievementCensus {
		t.Errorf("next types = %q, want %q", next[0].Type, EventAchievementCensus)
	}
	if got, _ := chars.Get(context.Background(), 42); got == nil {
		t.Errorf("character 42 should be upserted")
	}
}

func TestCharacterCensus_NoFCChainsOnlyAchievement(t *testing.T) {
	h, ls, _ := newTestCharacterCensus(t)
	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		return &contract.CharacterProfile{ID: id, Name: "Char", World: "Ultros", Datacenter: "Primal", Race: "Hyur"}, nil
	}
	next, err := h.Handle(context.Background(), characterPayload(42))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 || next[0].Type != EventAchievementCensus {
		t.Errorf("next = %+v, want only achievement-census", next)
	}
}

func TestCharacterCensus_NotFoundMarksDeleted(t *testing.T) {
	h, ls, chars := newTestCharacterCensus(t)
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 42, Name: "X", FirstSeenAt: time.Now()}, nil)
	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		return nil, contract.ErrCharacterNotFound
	}
	next, err := h.Handle(context.Background(), characterPayload(42))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 0 {
		t.Errorf("next jobs = %d, want 0 (deleted)", len(next))
	}
	got, _ := chars.Get(context.Background(), 42)
	if got.DeletedAt == nil {
		t.Errorf("character 42 should be marked deleted")
	}
}

func TestCharacterCensus_ReturnsDownstreamJobsInNext(t *testing.T) {
	ls := mocklodestone.NewFake()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())

	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		return &contract.CharacterProfile{
			ID:            id,
			Name:          "Immediate Character",
			World:         "Ultros",
			Datacenter:    "Primal",
			Race:          "Hyur",
			FreeCompanyID: "9234567890123456789",
		}, nil
	}

	h := NewCharacterCensus(ls, nil, svc, nil)

	next, err := h.Handle(context.Background(), characterPayload(42))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("expected 1 returned job (ach), got %d", len(next))
	}
	if next[0].Type != EventAchievementCensus {
		t.Errorf("expected first job to be achievement-census, got %q", next[0].Type)
	}
}

func TestCharacterCensus_FetchError(t *testing.T) {
	h, ls, _ := newTestCharacterCensus(t)
	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		return nil, errors.New("boom")
	}
	if _, err := h.Handle(context.Background(), characterPayload(1)); err == nil {
		t.Fatal("expected error on fetch failure")
	}
}

func TestCharacterCensus_LodestonePrimary_Success_ChainsAchievement(t *testing.T) {
	h, ls, ts, _, chars := newTestDualCharacterCensus(t)
	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		return &contract.CharacterProfile{
			ID:            id,
			Name:          "Primary Warrior",
			World:         "Balmung",
			Datacenter:    "Crystal",
			Race:          "Hyur",
			FreeCompanyID: "fc-123456",
		}, nil
	}

	next, err := h.Handle(context.Background(), characterPayload(100))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1", len(next))
	}
	if next[0].Type != EventAchievementCensus {
		t.Errorf("unexpected job types: %+v", next)
	}
	if len(ts.ProfileCalls) != 0 {
		t.Errorf("expected 0 tomestone profile calls, got %d", len(ts.ProfileCalls))
	}
	got, _ := chars.Get(context.Background(), 100)
	if got == nil || got.Name != "Primary Warrior" {
		t.Errorf("expected character to be upserted in repository, got %+v", got)
	}
}

func TestCharacterCensus_LodestoneError_FallbackToTomestone_Success(t *testing.T) {
	h, ls, ts, _, chars := newTestDualCharacterCensus(t)
	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		return nil, errors.New("lodestone 429 rate limited or cloudflare error")
	}
	fcID := "fc-tome-789"
	ts.SetCharacter(&contract.TomestoneCharacter{
		ID:            200,
		Name:          "Fallback Hero",
		Server:        "Gilgamesh",
		FreeCompanyID: &fcID,
	})

	next, err := h.Handle(context.Background(), characterPayload(200))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1 (achievement)", len(next))
	}
	if next[0].Type != EventAchievementCensus {
		t.Errorf("unexpected job types: %+v", next)
	}
	got, _ := chars.Get(context.Background(), 200)
	if got == nil || got.Name != "Fallback Hero" {
		t.Errorf("expected character to be upserted via tomestone fallback, got %+v", got)
	}
}

func TestCharacterCensus_LodestonePaused_UsesTomestoneDirectly(t *testing.T) {
	h, ls, ts, limiter, chars := newTestDualCharacterCensus(t)
	limiter.Pause(contract.ProviderLodestone, 10*time.Minute, "429 rate limited")

	lsCalled := false
	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		lsCalled = true
		return nil, errors.New("should not be called when paused")
	}

	ts.SetCharacter(&contract.TomestoneCharacter{
		ID:     300,
		Name:   "Direct Tomestone Hero",
		Server: "Leviathan",
	})

	next, err := h.Handle(context.Background(), characterPayload(300))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if lsCalled {
		t.Error("lodestone was called while paused")
	}
	if len(next) != 1 || next[0].Type != EventAchievementCensus {
		t.Fatalf("expected 1 achievement job, got %+v", next)
	}
	got, _ := chars.Get(context.Background(), 300)
	if got == nil || got.Name != "Direct Tomestone Hero" {
		t.Errorf("expected character to be upserted, got %+v", got)
	}
}

func TestCharacterCensus_Lodestone404_TomestoneHit(t *testing.T) {
	h, ls, ts, _, chars := newTestDualCharacterCensus(t)
	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		return nil, contract.ErrCharacterNotFound
	}
	ts.SetCharacter(&contract.TomestoneCharacter{
		ID:     400,
		Name:   "Alive On Tomestone",
		Server: "Siren",
		Race:   "Hyur",
	})

	next, err := h.Handle(context.Background(), characterPayload(400))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 || next[0].Type != EventAchievementCensus {
		t.Fatalf("expected 1 achievement job, got %+v", next)
	}
	got, _ := chars.Get(context.Background(), 400)
	if got == nil || got.Name != "Alive On Tomestone" {
		t.Errorf("expected character to be upserted, got %+v", got)
	}
	if got != nil && got.DeletedAt != nil {
		t.Errorf("character should not be marked deleted because it was found on Tomestone")
	}
}

func TestCharacterCensus_Lodestone404_Tomestone404_MarksDeleted(t *testing.T) {
	h, ls, _, _, chars := newTestDualCharacterCensus(t)
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 500, Name: "Old Name", FirstSeenAt: time.Now()}, nil)
	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		return nil, contract.ErrCharacterNotFound
	}
	// ts has no character 500 (returns ErrCharacterNotFound)

	next, err := h.Handle(context.Background(), characterPayload(500))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 0 {
		t.Errorf("expected 0 chained jobs on confirmed delete, got %d", len(next))
	}
	got, _ := chars.Get(context.Background(), 500)
	if got == nil || got.DeletedAt == nil {
		t.Errorf("expected character 500 to be marked deleted")
	}
}

func TestCharacterCensus_LodestoneError_Tomestone404_ReturnsErrorForLodestoneRetry(t *testing.T) {
	h, ls, _, _, chars := newTestDualCharacterCensus(t)
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 550, Name: "Existing Character", FirstSeenAt: time.Now()}, nil)
	ls.FetchCharacterFunc = func(ctx context.Context, id uint32) (*contract.CharacterProfile, error) {
		return nil, errors.New("lodestone 503 or 429 rate limit")
	}
	// ts has no character 550 (returns ErrCharacterNotFound)

	_, err := h.Handle(context.Background(), characterPayload(550))
	if err == nil {
		t.Fatal("expected error to retry on Lodestone when Tomestone 404s during Lodestone failure, got nil")
	}
	got, _ := chars.Get(context.Background(), 550)
	if got == nil || got.DeletedAt != nil {
		t.Errorf("character should NOT be marked deleted when Tomestone 404s on Lodestone error, got %+v", got)
	}
}

func TestCharacterCensus_LodestonePaused_Tomestone404_ReturnsErrorForLodestoneRetry(t *testing.T) {
	h, _, _, limiter, chars := newTestDualCharacterCensus(t)
	_ = chars.Upsert(context.Background(), contract.CharacterRecord{ID: 560, Name: "Existing Character", FirstSeenAt: time.Now()}, nil)
	limiter.Pause(contract.ProviderLodestone, 10*time.Minute, "lodestone paused")
	// ts has no character 560 (returns ErrCharacterNotFound)

	_, err := h.Handle(context.Background(), characterPayload(560))
	if err == nil {
		t.Fatal("expected error to retry on Lodestone when Tomestone 404s while Lodestone is paused, got nil")
	}
	got, _ := chars.Get(context.Background(), 560)
	if got == nil || got.DeletedAt != nil {
		t.Errorf("character should NOT be marked deleted when Tomestone 404s while Lodestone is paused, got %+v", got)
	}
}

func TestCharacterCensus_AllProvidersRateLimited_ReturnsError(t *testing.T) {
	h, _, _, limiter, _ := newTestDualCharacterCensus(t)
	limiter.Pause(contract.ProviderLodestone, 10*time.Minute, "lodestone paused")
	limiter.Pause(contract.ProviderTomestone, 10*time.Minute, "tomestone paused")

	_, err := h.Handle(context.Background(), characterPayload(600))
	if err == nil {
		t.Fatal("expected error when all providers are rate limited")
	}
}
