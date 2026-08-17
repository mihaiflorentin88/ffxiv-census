package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/xivapi/godestone/v2"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	mocklodestone "github.com/mihaiflorentin88/ffxiv-census/mock/lodestone"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
)

func newTestFCCensus(t *testing.T) (*FreeCompanyCensus, *mocklodestone.Fake, *mockrepo.FreeCompanyRepository) {
	t.Helper()
	ls := mocklodestone.NewFake()
	fcs := mockrepo.NewFreeCompanyFake()
	svc := census.NewService(mockrepo.NewCharacterFake(), fcs, mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return NewFreeCompanyCensus(ls, svc, nil), ls, fcs
}

func fcPayload(id string) []byte {
	b, _ := json.Marshal(FreeCompanyCensusPayload{FCID: id})
	return b
}

func TestFreeCompanyCensus_UpsertsAndChainsMembers(t *testing.T) {
	h, ls, fcs := newTestFCCensus(t)
	ls.FetchFreeCompanyFunc = func(id string) (*godestone.FreeCompany, error) {
		return &godestone.FreeCompany{ID: id, Name: "The Scions", World: "Ultros", DC: "Primal", ActiveMemberCount: 42}, nil
	}
	ls.FetchFreeCompanyMembersFunc = func(fcID string) ([]uint32, error) {
		return []uint32{101, 102, 103}, nil
	}
	next, err := h.Handle(context.Background(), fcPayload("9234567890123456789"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(next) != 3 {
		t.Fatalf("next jobs = %d, want 3 chained member jobs", len(next))
	}
	for i, wantID := range []uint32{101, 102, 103} {
		if next[i].Type != EventCharacterCensus {
			t.Errorf("job[%d].Type = %q, want %q", i, next[i].Type, EventCharacterCensus)
		}
		var p CharacterCensusPayload
		if err := json.Unmarshal(next[i].Payload, &p); err != nil {
			t.Fatalf("unmarshal payload[%d]: %v", i, err)
		}
		if p.CharacterID != wantID {
			t.Errorf("job[%d].CharacterID = %d, want %d", i, p.CharacterID, wantID)
		}
	}
	got, _ := fcs.Get(context.Background(), "9234567890123456789")
	if got == nil || got.Name != "The Scions" || got.MemberCount != 42 {
		t.Errorf("got %+v", got)
	}
}

func TestFreeCompanyCensus_FetchError(t *testing.T) {
	h, ls, _ := newTestFCCensus(t)
	ls.FetchFreeCompanyFunc = func(id string) (*godestone.FreeCompany, error) {
		return nil, errors.New("boom")
	}
	if _, err := h.Handle(context.Background(), fcPayload("9234567890123456789")); err == nil {
		t.Fatal("expected error on fetch failure")
	}
}
