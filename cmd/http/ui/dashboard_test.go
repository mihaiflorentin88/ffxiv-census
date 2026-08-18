package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	census "github.com/mihaiflorentin88/ffxiv-census/domain/census"
	mockqueue "github.com/mihaiflorentin88/ffxiv-census/mock/queue"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type testRig struct {
	svc   *census.Service
	chars *mockrepo.CharacterRepository
	fcs   *mockrepo.FreeCompanyRepository
	ach   *mockrepo.AchievementRepository
	q     *mockqueue.Fake
	ctrl  *UIController
}

func newTestRig(t *testing.T) *testRig {
	t.Helper()
	chars := mockrepo.NewCharacterFake()
	fcs := mockrepo.NewFreeCompanyFake()
	ach := mockrepo.NewAchievementFake()
	runs := mockrepo.NewCensusRunFake()
	svc := census.NewService(chars, fcs, ach, runs)
	q := mockqueue.NewFake()
	ctrl := NewUIController(svc, q)
	return &testRig{
		svc:   svc,
		chars: chars,
		fcs:   fcs,
		ach:   ach,
		q:     q,
		ctrl:  ctrl,
	}
}

func TestDashboardHandler(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	// Seed test characters
	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:                  1001,
		Name:                "Tataru Taru",
		World:               "Balmung",
		Datacenter:          "Crystal",
		Region:              "NA",
		Race:                "Lalafell",
		Tribe:               "Plainsfolk",
		FirstSeenAt:         recent,
		LatestAchievementAt: &recent,
	}, []contract.ClassJobRecord{
		{CharacterID: 1001, Level: 100, Name: "Paladin"},
	})

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:          1002,
		Name:        "Alphinaud Leveilleur",
		World:       "Ragnarok",
		Datacenter:  "Chaos",
		Region:      "EU",
		Race:        "Elezen",
		Tribe:       "Wildwood",
		FirstSeenAt: recent,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.Dashboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Total Population") {
		t.Errorf("expected body to contain 'Total Population', got:\n%s", body)
	}
	if !strings.Contains(body, "Active Players") {
		t.Errorf("expected body to contain 'Active Players', got:\n%s", body)
	}
	if !strings.Contains(body, "Max Level (Lv. 100)") {
		t.Errorf("expected body to contain 'Max Level (Lv. 100)', got:\n%s", body)
	}
	if !strings.Contains(body, "Characters at Cap") {
		t.Errorf("expected body to contain 'Characters at Cap', got:\n%s", body)
	}
	if !strings.Contains(body, "Crystal") && !strings.Contains(body, "NA") {
		t.Errorf("expected body to contain region stats, got:\n%s", body)
	}
}

func TestWorldDrilldownHandler(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:                  1001,
		Name:                "Tataru Taru",
		World:               "Balmung",
		Datacenter:          "Crystal",
		Region:              "NA",
		Race:                "Lalafell",
		Tribe:               "Plainsfolk",
		FirstSeenAt:         recent,
		LatestAchievementAt: &recent,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/ui/partials/world-breakdown?region=NA", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.WorldDrilldown(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Balmung") {
		t.Errorf("expected body to contain 'Balmung', got:\n%s", body)
	}
}
