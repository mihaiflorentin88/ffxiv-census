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
	ach   *mockrepo.AchievementRepository
	q     *mockqueue.Fake
	ctrl  *UIController
}

func newTestRig(t *testing.T) *testRig {
	t.Helper()
	chars := mockrepo.NewCharacterFake()
	ach := mockrepo.NewAchievementFake()
	runs := mockrepo.NewCensusRunFake()
	svc := census.NewService(chars, ach, runs)
	q := mockqueue.NewFake()
	ctrl := NewUIController(svc, q)
	return &testRig{
		svc:   svc,
		chars: chars,
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

func TestDashboardHandler_RaceChartLayout(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	// Seed characters with distinct races so the chart renders.
	races := []string{"Hyur", "Elezen", "Lalafell", "Miqo'te", "Roegadyn", "Au Ra", "Hrothgar", "Viera"}
	for i, race := range races {
		_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
			ID:                  uint32(5001 + i),
			Name:                "Char " + race,
			World:               "Balmung",
			Datacenter:          "Crystal",
			Region:              "NA",
			Race:                race,
			FirstSeenAt:         recent,
			LatestAchievementAt: &recent,
		}, nil)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.Dashboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()

	// Responsive grid replaces fixed 1fr 1fr.
	if !strings.Contains(body, "repeat(auto-fit, minmax(360px, 1fr)") {
		t.Error("expected responsive grid with repeat(auto-fit, minmax(360px, 1fr))")
	}
	// Race chart container must be 340px.
	if !strings.Contains(body, "height: 340px") {
		t.Error("expected race chart container height 340px")
	}
	// Chart.js options: maintainAspectRatio must be false.
	if !strings.Contains(body, "maintainAspectRatio: false") {
		t.Error("expected maintainAspectRatio: false in race chart options")
	}
	// Chart.js options: cutout must be 65%.
	if !strings.Contains(body, `cutout: "65%"`) && !strings.Contains(body, `cutout:'65%'`) {
		t.Error(`expected cutout: "65%" in race chart options`)
	}
	// Legend must be at bottom, not right.
	if strings.Contains(body, `position: "right"`) || strings.Contains(body, `position:"right"`) {
		t.Error("race chart legend should not be position right")
	}
	if !strings.Contains(body, `position: "bottom"`) && !strings.Contains(body, `position:"bottom"`) {
		t.Error(`expected legend position: "bottom"`)
	}
	// Legend must be centered.
	if !strings.Contains(body, `align: "center"`) && !strings.Contains(body, `align:"center"`) {
		t.Error(`expected legend align: "center"`)
	}
	// Legend must use circular point style markers, not stretched rectangles.
	if !strings.Contains(body, `usePointStyle: true`) {
		t.Error("expected usePointStyle: true in race chart legend")
	}
	if !strings.Contains(body, `pointStyle: "circle"`) {
		t.Error(`expected pointStyle: "circle" in race chart legend`)
	}
	// pointStyleWidth forces a fixed 10px marker that stretches differently
	// from the font-derived height; it must be absent.
	if strings.Contains(body, `pointStyleWidth`) {
		t.Error("race chart legend must not contain pointStyleWidth")
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

func TestDashboardHandler_ExpansionSortOrder(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID: 5001, Name: "TestChar", World: "Balmung", Datacenter: "Crystal", Region: "NA",
		Race: "Hyur", FirstSeenAt: recent, LatestAchievementAt: &recent,
	}, nil)

	_ = rig.ach.SyncMilestones(context.Background(), census.DefaultMilestones())
	_ = rig.ach.UpsertCharacterMilestones(context.Background(), 5001, []contract.CharacterMilestone{
		{CharacterID: 5001, AchievementID: 1129, AchievedAt: recent},
		{CharacterID: 5001, AchievementID: 3496, AchievedAt: recent},
	})

	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.Dashboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	arIdx := strings.Index(body, "A Realm Reborn")
	dtIdx := strings.Index(body, "Dawntrail")
	if arIdx < 0 {
		t.Fatal("expected 'A Realm Reborn' in body")
	}
	if dtIdx < 0 {
		t.Fatal("expected 'Dawntrail' in body")
	}
	if arIdx > dtIdx {
		t.Errorf("expansion sort order wrong: A Realm Reborn (idx %d) should appear before Dawntrail (idx %d)", arIdx, dtIdx)
	}
}
