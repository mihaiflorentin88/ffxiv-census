package handler

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/xivapi/godestone/v2"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	mocklodestone "github.com/mihaiflorentin88/ffxiv-census/mock/lodestone"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestIDSweep(t *testing.T) (*IDSweep, *mocklodestone.Fake, *mockrepo.CharacterRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewIDSweep(ls, svc, nil), ls, chars
}

func idsweepPayload(from, to uint32) []byte {
	b, _ := json.Marshal(IDSweepPayload{From: from, To: to})
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
