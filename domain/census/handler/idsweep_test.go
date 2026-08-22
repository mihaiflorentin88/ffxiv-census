package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	svc := census.NewService(chars, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewIDSweep(ls, nil, svc, nil), ls, chars
}

func newTestDualIDSweep(t *testing.T) (*IDSweep, *mocklodestone.Fake, *mocktomestone.Fake, *mockrepo.CharacterRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	ts := mocktomestone.NewFake()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewIDSweep(ls, ts, svc, nil), ls, ts, chars
}

func newTestDualIDSweepWithLimiter(t *testing.T) (*IDSweep, *mocklodestone.Fake, *mocktomestone.Fake, *mock.ProviderRateLimiter, *mockrepo.CharacterRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	ts := mocktomestone.NewFake()
	limiter := mock.NewProviderRateLimiter()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
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

func TestIDSweep_TomestonePrimary_Success(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		t.Fatalf("Lodestone should NOT be called when Tomestone succeeds for id %d", id)
		return nil, nil
	}
	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		return &contract.TomestoneCharacter{
			ID:         id,
			Name:       "Tomestone Primary Hero",
			Server:     "Balmung",
			Datacenter: "Crystal",
		}, nil
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
	if got.Name != "Tomestone Primary Hero" || got.World != "Balmung" || got.Region != "NA" {
		t.Errorf("got %+v, want Tomestone Primary Hero from NA", got)
	}
}

func TestIDSweep_TomestoneHit_NoLodestoneCall(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	lodestoneCalled := false
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		lodestoneCalled = true
		return nil, errors.New("lodestone should not be called")
	}
	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		return &contract.TomestoneCharacter{
			ID:         150,
			Name:       "Tomestone Only",
			Server:     "Gilgamesh",
			Datacenter: "Aether",
		}, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(150, 150, "auto"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if lodestoneCalled {
		t.Error("Lodestone should NOT be called when Tomestone succeeds")
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1", len(next))
	}
	got, _ := chars.Get(context.Background(), 150)
	if got == nil || got.Name != "Tomestone Only" {
		t.Errorf("expected character from tomestone, got %+v", got)
	}
}

func TestIDSweep_TomestoneError_FallbackToLodestone_Success(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		return nil, errors.New("tomestone 500 server error")
	}
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{
			ID:    201,
			Name:  "Lodestone Fallback Hero",
			World: "Ragnarok",
			DC:    "Chaos",
		}, nil
	}

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
	if got201.Name != "Lodestone Fallback Hero" || got201.Region != "EU" {
		t.Errorf("got201 = %+v", got201)
	}
}

func TestIDSweep_TomestonePaused_UsesLodestoneDirectly(t *testing.T) {
	h, ls, ts, limiter, chars := newTestDualIDSweepWithLimiter(t)
	limiter.Pause(contract.ProviderTomestone, 10*time.Minute, "tomestone paused")

	tsCalled := false
	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		tsCalled = true
		return nil, errors.New("tomestone should not be called when paused")
	}
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{
			ID:    205,
			Name:  "Direct Lodestone Hero",
			World: "Moogle",
			DC:    "Chaos",
		}, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(205, 205, "auto"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if tsCalled {
		t.Error("tomestone was called while paused")
	}
	if len(next) != 1 {
		t.Fatalf("expected 1 job, got %d", len(next))
	}
	got, _ := chars.Get(context.Background(), 205)
	if got == nil || got.Name != "Direct Lodestone Hero" {
		t.Errorf("expected character to be upserted, got %+v", got)
	}
}

func TestIDSweep_Tomestone404_FallbackToLodestoneHit(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		return nil, contract.ErrCharacterNotFound
	}
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{
			ID:    210,
			Name:  "Found on Lodestone",
			World: "Cerberus",
			DC:    "Chaos",
		}, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(210, 210, "auto"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1", len(next))
	}
	got, _ := chars.Get(context.Background(), 210)
	if got == nil || got.Name != "Found on Lodestone" {
		t.Errorf("expected character to be found on lodestone, got %+v", got)
	}
}

func TestIDSweep_Tomestone404_LodestonePaused_ReturnsErrorForRetry(t *testing.T) {
	h, _, _, limiter, _ := newTestDualIDSweepWithLimiter(t)
	limiter.Pause(contract.ProviderLodestone, 10*time.Minute, "lodestone paused")
	// ts has no character 215 (returns ErrCharacterNotFound)

	_, err := h.Handle(context.Background(), idsweepPayloadWithSource(215, 215, "auto"))
	if err == nil {
		t.Fatal("expected error to retry on Lodestone when Tomestone 404s and Lodestone is paused, got nil")
	}
	if !strings.Contains(err.Error(), "retrying on lodestone") {
		t.Errorf("expected retry-on-lodestone error, got: %v", err)
	}
}

func TestIDSweep_TomestoneError_Lodestone404_ConfirmedNotFound(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		return nil, errors.New("tomestone 503 server error")
	}
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return nil, contract.ErrCharacterNotFound
	}

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(216, 216, "auto"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 0 {
		t.Fatalf("next jobs = %d, want 0 (confirmed not found)", len(next))
	}
	if got, _ := chars.Get(context.Background(), 216); got != nil {
		t.Errorf("id 216 should not exist (confirmed not found)")
	}
}

func TestIDSweep_DualSource_Double404(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	// Tomestone returns 404 first (primary), then Lodestone 404 (fallback).
	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		return nil, contract.ErrCharacterNotFound
	}
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

func TestIDSweep_TomestoneTransientError_FallbackToLodestone(t *testing.T) {
	h, ls, ts, chars := newTestDualIDSweep(t)

	ts.FetchCharacterProfileFunc = func(ctx context.Context, id uint32, update bool) (*contract.TomestoneCharacter, error) {
		return nil, errors.New("tomestone server error 500")
	}
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{
			ID:    601,
			Name:  "Lodestone After Tomestone Error",
			World: "Tonberry",
			DC:    "Elemental",
		}, nil
	}

	next, err := h.Handle(context.Background(), idsweepPayloadWithSource(601, 601, "auto"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 1 {
		t.Fatalf("next jobs = %d, want 1", len(next))
	}
	got, _ := chars.Get(context.Background(), 601)
	if got == nil || got.Name != "Lodestone After Tomestone Error" {
		t.Errorf("expected character to be upserted from lodestone fallback, got %+v", got)
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
	svc := census.NewService(chars, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
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
	svc := census.NewService(chars, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
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

func TestIDSweep_ReturnsDownstreamJobsInNext(t *testing.T) {
	ls := mocklodestone.NewFake()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())

	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{
			ID:            id,
			Name:          "Immediate Hero",
			World:         "Ultros",
			DC:            "Primal",
			FreeCompanyID: fmt.Sprintf("fc-%d", id),
		}, nil
	}

	h := NewIDSweep(ls, nil, svc, nil)

	next, err := h.Handle(context.Background(), idsweepPayload(1, 2))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 2 {
		t.Fatalf("expected 2 returned jobs (2 ach), got %d", len(next))
	}
	for _, j := range next {
		if j.Type != EventAchievementCensus {
			t.Errorf("unexpected job type: %q", j.Type)
		}
	}
}

func BenchmarkIDSweepNotFoundInfo(b *testing.B) {
	ls := mocklodestone.NewFake()
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return nil, contract.ErrCharacterNotFound
	}
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := NewIDSweep(ls, nil, svc, logger)
	payload := idsweepPayload(1, 100)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, err := h.Handle(context.Background(), payload)
		if err != nil {
			b.Fatalf("Handle: %v", err)
		}
		if len(next) != 0 {
			b.Fatalf("expected 0 next jobs, got %d", len(next))
		}
	}
}
