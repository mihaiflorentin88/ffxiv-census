package handler

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/xivapi/godestone/v2"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/mock"
	mocklodestone "github.com/mihaiflorentin88/ffxiv-census/mock/lodestone"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	mocktomestone "github.com/mihaiflorentin88/ffxiv-census/mock/tomestone"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestIDSweep(t *testing.T) (*IDSweep, *mocklodestone.Fake, *mockrepo.CharacterRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewIDSweep(ls, nil, svc, nil), ls, chars
}

func newTestDualIDSweep(t *testing.T) (*IDSweep, *mocklodestone.Fake, *mocktomestone.Fake, *mockrepo.CharacterRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	ts := mocktomestone.NewFake()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewIDSweep(ls, ts, svc, nil), ls, ts, chars
}

func newTestDualIDSweepWithLimiter(t *testing.T) (*IDSweep, *mocklodestone.Fake, *mocktomestone.Fake, *mock.ProviderRateLimiter, *mockrepo.CharacterRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	ts := mocktomestone.NewFake()
	limiter := mock.NewProviderRateLimiter()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewIDSweep(ls, ts, svc, nil, limiter), ls, ts, limiter, chars
}

func idsweepPayload(from, to uint32) []byte {
	b, _ := json.Marshal(IDSweepPayload{From: from, To: to})
	return b
}

func idsweepPayloadWithSource(from, to uint32, source string) []byte {
	b, _ := json.Marshal(IDSweepPayload{From: from, To: to, Source: source})
	return b
}

func TestIDSweep_DiscoversAndChains(t *testing.T) {
	h, ls, chars := newTestIDSweep(t)
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		if id == 2 {
			return nil, contract.ErrCharacterNotFound
		}
		return &godestone.Character{ID: id, Name: "Char", World: "Ultros", DC: "Primal"}, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayload(1, 3))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 2 {
		t.Fatalf("next jobs = %d, want 2 (ids 1 and 3)", len(next))
	}
	for _, j := range next {
		if j.Type != EventAchievementCensus {
			t.Errorf("job type = %q, want %q", j.Type, EventAchievementCensus)
		}
		var p AchievementCensusPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if p.CharacterID != 1 && p.CharacterID != 3 {
			t.Errorf("chained character id = %d, want 1 or 3", p.CharacterID)
		}
	}
	// 404 (id 2) must not be upserted; 1 and 3 must.
	if got, _ := chars.Get(context.Background(), 2); got != nil {
		t.Errorf("id 2 should not be upserted (404)")
	}
	if got, _ := chars.Get(context.Background(), 1); got == nil {
		t.Errorf("id 1 should be upserted")
	}
	if got, _ := chars.Get(context.Background(), 3); got == nil {
		t.Errorf("id 3 should be upserted")
	}
}

func TestIDSweep_TransientErrorReturnsError(t *testing.T) {
	h, ls, _ := newTestIDSweep(t)
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return nil, errors.New("transient network error")
	}
	if _, err := h.Handle(context.Background(), idsweepPayload(1, 1)); err == nil {
		t.Fatal("expected error on transient fetch failure")
	}
}

func TestIDSweep_MaxUint32DoesNotOverflow(t *testing.T) {
	h, ls, _ := newTestIDSweep(t)
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return nil, contract.ErrCharacterNotFound
	}
	// A single ID at MaxUint32 must terminate, not wrap into an infinite loop.
	if _, err := h.Handle(context.Background(), idsweepPayload(math.MaxUint32, math.MaxUint32)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

func TestIDSweep_InvalidRange(t *testing.T) {
	h, _, _ := newTestIDSweep(t)
	if _, err := h.Handle(context.Background(), idsweepPayload(5, 3)); err == nil {
		t.Fatal("expected error for from > to")
	}
}

func TestIDSweep_NotFoundSkipsCharacterWithoutFailingChunk(t *testing.T) {
	h, ls, chars := newTestIDSweep(t)
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		if id == 75 {
			return nil, contract.ErrCharacterNotFound
		}
		return &godestone.Character{ID: id, Name: "Char", World: "Ultros", DC: "Primal"}, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayload(74, 76))
	if err != nil {
		t.Fatalf("Handle should succeed when character returns ErrCharacterNotFound: %v", err)
	}
	if len(next) != 2 {
		t.Fatalf("next jobs = %d, want 2 (ids 74 and 76)", len(next))
	}
	if got, _ := chars.Get(context.Background(), 75); got != nil {
		t.Errorf("id 75 should not be upserted (non-existent)")
	}
	if got, _ := chars.Get(context.Background(), 74); got == nil {
		t.Errorf("id 74 should be upserted")
	}
	if got, _ := chars.Get(context.Background(), 76); got == nil {
		t.Errorf("id 76 should be upserted")
	}
}

func TestIDSweep_LodestonePrimary_Success(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{
			ID:    id,
			Name:  "Lodestone Primary Hero",
			World: "Balmung",
			DC:    "Crystal",
		}, nil
	}
	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		t.Fatalf("Tomestone should NOT be called when Lodestone succeeds for id %d", id)
		return nil, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(101, 101, "auto"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1", len(next))
	}

	got, err := chars.Get(context.Background(), 101)
	if err != nil || got == nil {
		t.Fatalf("Get(101): %v / %+v", err, got)
	}
	if got.Name != "Lodestone Primary Hero" || got.World != "Balmung" || got.Region != "NA" {
		t.Errorf("got %+v, want Lodestone Primary Hero from NA", got)
	}
}

func TestIDSweep_LodestoneError_FallbackToTomestone_Success(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return nil, errors.New("lodestone 429 rate limit or scrape error")
	}
	ts.SetCharacter(&contract.TomestoneCharacter{
		ID:         201,
		Name:       "Tomestone Fallback Hero",
		Server:     "Ragnarok",
		Datacenter: "Chaos",
	})

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(201, 201, "auto"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1", len(next))
	}

	got201, err := chars.Get(context.Background(), 201)
	if err != nil || got201 == nil {
		t.Fatalf("Get(201): %v / %+v", err, got201)
	}
	if got201.Name != "Tomestone Fallback Hero" || got201.Region != "EU" {
		t.Errorf("got201 = %+v", got201)
	}
}

func TestIDSweep_LodestonePaused_UsesTomestoneDirectly(t *testing.T) {
	h, ls, ts, limiter, chars := newTestDualIDSweepWithLimiter(t)
	limiter.Pause(contract.ProviderLodestone, 10*time.Minute, "lodestone paused")

	lsCalled := false
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		lsCalled = true
		return nil, errors.New("lodestone should not be called when paused")
	}
	ts.SetCharacter(&contract.TomestoneCharacter{
		ID:         205,
		Name:       "Direct Tomestone Hero",
		Server:     "Moogle",
		Datacenter: "Chaos",
	})

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(205, 205, "auto"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if lsCalled {
		t.Error("lodestone was called while paused")
	}
	if len(next) != 1 {
		t.Fatalf("expected 1 job, got %d", len(next))
	}
	got, _ := chars.Get(context.Background(), 205)
	if got == nil || got.Name != "Direct Tomestone Hero" {
		t.Errorf("expected character to be upserted, got %+v", got)
	}
}

func TestIDSweep_Lodestone404_FallbackToTomestoneHit(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return nil, contract.ErrCharacterNotFound
	}
	ts.SetCharacter(&contract.TomestoneCharacter{
		ID:         210,
		Name:       "Found on Tomestone",
		Server:     "Cerberus",
		Datacenter: "Chaos",
	})

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(210, 210, "auto"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1", len(next))
	}
	got, _ := chars.Get(context.Background(), 210)
	if got == nil || got.Name != "Found on Tomestone" {
		t.Errorf("expected character to be found on tomestone, got %+v", got)
	}
}

func TestIDSweep_LodestoneError_Tomestone404_ReturnsErrorForLodestoneRetry(t *testing.T) {
	h, ls, _, _ := newTestDualIDSweep(t)

	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return nil, errors.New("lodestone 503 or 429 rate limit")
	}
	// ts has no character 215 (returns ErrCharacterNotFound)

	_, err := h.Handle(context.Background(), idsweepPayloadWithSource(215, 215, "auto"))
	if err == nil {
		t.Fatal("expected error to retry on Lodestone when Tomestone 404s on Lodestone error, got nil")
	}
}

func TestIDSweep_LodestonePaused_Tomestone404_ReturnsErrorForLodestoneRetry(t *testing.T) {
	h, _, _, limiter, _ := newTestDualIDSweepWithLimiter(t)
	limiter.Pause(contract.ProviderLodestone, 10*time.Minute, "lodestone paused")
	// ts has no character 216 (returns ErrCharacterNotFound)

	_, err := h.Handle(context.Background(), idsweepPayloadWithSource(216, 216, "auto"))
	if err == nil {
		t.Fatal("expected error to retry on Lodestone when Tomestone 404s while Lodestone is paused, got nil")
	}
}

func TestIDSweep_DualSource_Double404(t *testing.T) {
	h, ls, _, chars := newTestDualIDSweep(t)

	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return nil, contract.ErrCharacterNotFound
	}

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(301, 303, "auto"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 0 {
		t.Fatalf("next jobs = %d, want 0", len(next))
	}
	for id := uint32(301); id <= 303; id++ {
		if got, _ := chars.Get(context.Background(), id); got != nil {
			t.Errorf("id %d should not exist", id)
		}
	}
}

func TestIDSweep_ExplicitTomestoneSource(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	ts.SetCharacter(&contract.TomestoneCharacter{
		ID:         401,
		Name:       "Tomestone Only",
		Server:     "Tonberry",
		Datacenter: "Elemental",
	})
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		t.Fatalf("Lodestone should NEVER be called when source is 'tomestone'")
		return nil, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(401, 402, "tomestone"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1", len(next))
	}
	if got, _ := chars.Get(context.Background(), 401); got == nil {
		t.Errorf("id 401 should be upserted")
	}
	if got, _ := chars.Get(context.Background(), 402); got != nil {
		t.Errorf("id 402 should not be upserted")
	}
}

func TestIDSweep_ExplicitLodestoneSource(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		t.Fatalf("Tomestone should NEVER be called when source is 'lodestone'")
		return nil, nil
	}
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{
			ID:    501,
			Name:  "Lodestone Only",
			World: "Shinryu",
			DC:    "Mana",
		}, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(501, 501, "lodestone"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1", len(next))
	}
	if got, _ := chars.Get(context.Background(), 501); got == nil {
		t.Errorf("id 501 should be upserted")
	}
}

func TestIDSweep_AllProvidersRateLimited_ReturnsError(t *testing.T) {
	h, _, _, limiter, _ := newTestDualIDSweepWithLimiter(t)
	limiter.Pause(contract.ProviderLodestone, 10*time.Minute, "lodestone paused")
	limiter.Pause(contract.ProviderTomestone, 10*time.Minute, "tomestone paused")

	_, err := h.Handle(context.Background(), idsweepPayloadWithSource(601, 601, "auto"))
	if err == nil {
		t.Fatal("expected error when all providers are rate limited in auto mode")
	}
}

func TestIDSweep_TomestoneTransientError(t *testing.T) {
	h, ls, ts, _ := newTestDualIDSweep(t)

	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return nil, contract.ErrCharacterNotFound
	}
	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		return nil, errors.New("tomestone server error 500")
	}

	if _, err := h.Handle(context.Background(), idsweepPayloadWithSource(601, 601, "auto")); err == nil {
		t.Fatal("expected error on tomestone transient error")
	}
}

func TestIDSweep_NilTomestoneClient_ExplicitTomestoneSource(t *testing.T) {
	h, _, _ := newTestIDSweep(t)

	payload, _ := json.Marshal(IDSweepPayload{
		From:   100,
		To:     105,
		Source: "tomestone",
	})

	_, err := h.Handle(context.Background(), payload)
	if err == nil {
		t.Fatal("expected error when source is tomestone but client is nil, got nil")
	}
	if !strings.Contains(err.Error(), "tomestone client unconfigured") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestIDSweep_NilClients_Error(t *testing.T) {
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	h := NewIDSweep(nil, nil, svc, nil)

	payload, _ := json.Marshal(IDSweepPayload{
		From:   100,
		To:     105,
		Source: "auto",
	})

	_, err := h.Handle(context.Background(), payload)
	if err == nil {
		t.Fatal("expected error when both clients are nil, got nil")
	}
}

func TestIDSweep_NilLodestoneClient_ExplicitLodestoneSource(t *testing.T) {
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	ts := mocktomestone.NewFake()
	h := NewIDSweep(nil, ts, svc, nil)

	payload, _ := json.Marshal(IDSweepPayload{
		From:   100,
		To:     105,
		Source: "lodestone",
	})

	_, err := h.Handle(context.Background(), payload)
	if err == nil {
		t.Fatal("expected error when source is lodestone but client is nil, got nil")
	}
	if !strings.Contains(err.Error(), "lodestone client unconfigured") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestIDSweep_ChainsFreeCompanyJobWhenFCIDPresent_Lodestone(t *testing.T) {
	h, ls, _ := newTestIDSweep(t)
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{
			ID:            id,
			Name:          "Hero",
			World:         "Spriggan",
			DC:            "Chaos",
			FreeCompanyID: "9231234567890123456",
		}, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayload(100, 100))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var foundAchievement, foundFC bool
	for _, j := range next {
		if j.Type == EventAchievementCensus {
			foundAchievement = true
		}
		if j.Type == EventFreeCompanyCensus {
			foundFC = true
			var p FreeCompanyCensusPayload
			if err := json.Unmarshal(j.Payload, &p); err != nil {
				t.Fatalf("unmarshal fc payload: %v", err)
			}
			if p.FCID != "9231234567890123456" {
				t.Errorf("fc id = %q, want 9231234567890123456", p.FCID)
			}
		}
	}
	if !foundAchievement {
		t.Errorf("expected achievement census job to be chained")
	}
	if !foundFC {
		t.Errorf("expected free company census job to be chained")
	}
}

func TestIDSweep_ChainsFreeCompanyJobWhenFCIDPresent_Tomestone(t *testing.T) {
	h, _, ts, _ := newTestDualIDSweep(t)
	fcID := "9231234567890123456"
	fcName := "Crystal Braves"
	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, retry429 bool) (*contract.TomestoneCharacter, error) {
		return &contract.TomestoneCharacter{
			ID:              id,
			Name:            "Alphinaud",
			Server:          "Spriggan",
			Datacenter:      "Chaos",
			FreeCompanyID:   &fcID,
			FreeCompanyName: &fcName,
		}, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(200, 200, "tomestone"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var foundAchievement, foundFC bool
	for _, j := range next {
		if j.Type == EventAchievementCensus {
			foundAchievement = true
		}
		if j.Type == EventFreeCompanyCensus {
			foundFC = true
			var p FreeCompanyCensusPayload
			if err := json.Unmarshal(j.Payload, &p); err != nil {
				t.Fatalf("unmarshal fc payload: %v", err)
			}
			if p.FCID != "9231234567890123456" {
				t.Errorf("fc id = %q, want 9231234567890123456", p.FCID)
			}
		}
	}
	if !foundAchievement {
		t.Errorf("expected achievement census job to be chained")
	}
	if !foundFC {
		t.Errorf("expected free company census job to be chained")
	}
}
