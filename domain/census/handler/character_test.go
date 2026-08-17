package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/xivapi/godestone/v2"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	mocklodestone "github.com/mihaiflorentin88/ffxiv-census/mock/lodestone"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestCharacterCensus(t *testing.T) (*CharacterCensus, *mocklodestone.Fake, *mockrepo.CharacterRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	chars := mockrepo.NewCharacterFake()
	svc := census.NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewCharacterCensus(ls, svc), ls, chars
}

func characterPayload(id uint32) []byte {
	b, _ := json.Marshal(CharacterCensusPayload{CharacterID: id})
	return b
}

func TestCharacterCensus_UpsertAndChain(t *testing.T) {
	h, ls, chars := newTestCharacterCensus(t)
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{ID: id, Name: "Char", World: "Ultros", DC: "Primal", FreeCompanyID: "9234567890123456789"}, nil
	}
	next, err := h.Handle(context.Background(), characterPayload(42))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 2 {
		t.Fatalf("next jobs = %d, want 2 (achievement + fc)", len(next))
	}
	if next[0].Type != EventAchievementCensus || next[1].Type != EventFreeCompanyCensus {
		t.Errorf("next types = %q, %q", next[0].Type, next[1].Type)
	}
	if got, _ := chars.Get(context.Background(), 42); got == nil {
		t.Errorf("character 42 should be upserted")
	}
}

func TestCharacterCensus_NoFCChainsOnlyAchievement(t *testing.T) {
	h, ls, _ := newTestCharacterCensus(t)
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{ID: id, Name: "Char", World: "Ultros", DC: "Primal"}, nil
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
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
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
