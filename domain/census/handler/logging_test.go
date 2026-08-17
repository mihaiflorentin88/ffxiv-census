package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/xivapi/godestone/v2"
	"github.com/xivapi/godestone/v2/provider/models"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	mocklodestone "github.com/mihaiflorentin88/ffxiv-census/mock/lodestone"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// newBufLogger returns a TextHandler logger writing to a buffer.
func newBufLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func TestCharacterCensus_LogsFetchAndStore(t *testing.T) {
	ls := mocklodestone.NewFake()
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return &godestone.Character{ID: id, Name: "Tataru Taru", World: "Ultros", DC: "Primal"}, nil
	}
	svc := census.NewService(mockrepo.NewCharacterFake(), mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	var buf bytes.Buffer
	h := NewCharacterCensus(ls, svc, newBufLogger(&buf))

	if _, err := h.Handle(context.Background(), characterPayload(42)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	logs := buf.String()
	for _, want := range []string{"handler.character_census.fetched", "handler.character_census.stored", "character_id=42", "Tataru Taru", "Ultros"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestCharacterCensus_LogsFetchError(t *testing.T) {
	ls := mocklodestone.NewFake()
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		return nil, errors.New("boom")
	}
	svc := census.NewService(mockrepo.NewCharacterFake(), mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	var buf bytes.Buffer
	h := NewCharacterCensus(ls, svc, newBufLogger(&buf))

	if _, err := h.Handle(context.Background(), characterPayload(1)); err == nil {
		t.Fatal("expected error on fetch failure")
	}
	logs := buf.String()
	for _, want := range []string{"handler.character_census.fetch_error", "character_id=1", "boom"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestFreeCompanyCensus_LogsFetchAndStore(t *testing.T) {
	ls := mocklodestone.NewFake()
	ls.FetchFreeCompanyFunc = func(id string) (*godestone.FreeCompany, error) {
		return &godestone.FreeCompany{ID: id, Name: "The Scions", World: "Ultros", DC: "Primal", ActiveMemberCount: 42}, nil
	}
	svc := census.NewService(mockrepo.NewCharacterFake(), mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	var buf bytes.Buffer
	h := NewFreeCompanyCensus(ls, svc, newBufLogger(&buf))

	if _, err := h.Handle(context.Background(), fcPayload("9234567890123456789")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	logs := buf.String()
	for _, want := range []string{"handler.fc_census.fetched", "handler.fc_census.stored", "fc_id=9234567890123456789", "The Scions", "members=42"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestAchievementCensus_LogsFetchedLatest(t *testing.T) {
	ls := mocklodestone.NewFake()
	now := time.Now()
	ls.FetchAchievementsFunc = func(id uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
		return []*godestone.AchievementInfo{
			{NamedEntity: &models.NamedEntity{ID: 590, Name: "My Little Chocobo"}, Date: now.Add(-time.Hour)},
			{NamedEntity: &models.NamedEntity{ID: 999, Name: "Other"}, Date: now},
		}, &godestone.AllAchievementInfo{Private: false}, nil
	}
	svc := census.NewService(mockrepo.NewCharacterFake(), mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	if err := svc.SyncMilestones(context.Background()); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	var buf bytes.Buffer
	h := NewAchievementCensus(ls, svc, newBufLogger(&buf))

	if _, err := h.Handle(context.Background(), achievementPayload(123)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	logs := buf.String()
	for _, want := range []string{"handler.achievement_census.fetched", "earned=2", "latest_id=999", "latest_name=Other"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestIDSweep_LogsRealTimeProbesAndDiscoveries(t *testing.T) {
	ls := mocklodestone.NewFake()
	ls.FetchCharacterFunc = func(id uint32) (*godestone.Character, error) {
		if id == 10 {
			return &godestone.Character{ID: 10, Name: "Alisaie Leveilleur", World: "Louisoix"}, nil
		}
		return nil, contract.ErrCharacterNotFound
	}
	svc := census.NewService(mockrepo.NewCharacterFake(), mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	var buf bytes.Buffer
	h := NewIDSweep(ls, nil, svc, newBufLogger(&buf))

	payload, _ := json.Marshal(IDSweepPayload{From: 9, To: 11})
	if _, err := h.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	logs := buf.String()
	for _, want := range []string{
		"handler.id_sweep.start",
		"from=9",
		"to=11",
		"count=3",
		"handler.id_sweep.probe",
		"character_id=9",
		"status=not_found",
		"handler.id_sweep.discovered",
		"character_id=10",
		"Alisaie Leveilleur",
		"Louisoix",
		"character_id=11",
		"handler.id_sweep.done",
		"discovered=1",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}
