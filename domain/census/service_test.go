package census

import (
	"context"
	"testing"

	"github.com/xivapi/godestone/v2"
	"github.com/xivapi/godestone/v2/data/gender"
	"github.com/xivapi/godestone/v2/provider/models"

	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
)

func newTestService(t *testing.T) (*Service, *mockrepo.CharacterRepository) {
	t.Helper()
	chars := mockrepo.NewCharacterFake()
	svc := NewService(chars, mockrepo.NewFreeCompanyFake(), mockrepo.NewAchievementFake(), mockrepo.NewCensusRunFake())
	return svc, chars
}

func TestService_UpsertCharacter(t *testing.T) {
	svc, chars := newTestService(t)

	char := &godestone.Character{
		ID:     123,
		Name:   "Tataru Taru",
		World:  "Ultros",
		DC:     "Primal",
		Gender: gender.Female,
		Race:   &models.GenderedEntity{Name: "Lalafell"},
		Tribe:  &models.GenderedEntity{Name: "Dunesfolk"},
		GrandCompanyInfo: &godestone.GrandCompanyInfo{
			GrandCompany: &models.NamedEntity{Name: "Maelstrom"},
		},
		FreeCompanyID:   "9234567890123456789",
		FreeCompanyName: "The Scions",
		ClassJobs: []*godestone.ClassJob{
			{JobID: 19, Name: "Paladin", Level: 90, ExpLevel: 12345},
			{JobID: 25, Name: "White Mage", Level: 90, ExpLevel: 0},
		},
	}

	if err := svc.UpsertCharacter(context.Background(), char); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}
	got, err := chars.Get(context.Background(), 123)
	if err != nil || got == nil {
		t.Fatalf("Get: %v / %+v", err, got)
	}
	if got.Region != "NA" {
		t.Errorf("region = %q, want NA (derived from Primal)", got.Region)
	}
	if got.Race != "Lalafell" || got.GrandCompany != "Maelstrom" {
		t.Errorf("got %+v", got)
	}
	if got.FreeCompanyID == nil || *got.FreeCompanyID != "9234567890123456789" {
		t.Errorf("free company id = %v", got.FreeCompanyID)
	}
	jobs, _ := chars.GetJobs(context.Background(), 123)
	if len(jobs) != 2 {
		t.Errorf("jobs = %d, want 2", len(jobs))
	}
}

func TestService_UpsertCharacter_NilSafe(t *testing.T) {
	svc, _ := newTestService(t)
	// Minimal character with nil race/tribe/grand company must not panic.
	char := &godestone.Character{ID: 1, Name: "X", World: "W", DC: "Primal", Gender: gender.None}
	if err := svc.UpsertCharacter(context.Background(), char); err != nil {
		t.Fatalf("UpsertCharacter: %v", err)
	}
}
