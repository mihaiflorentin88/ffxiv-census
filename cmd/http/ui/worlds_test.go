package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestWorldsHandler(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:                  3001,
		Name:                "Gilgamesh Knight",
		World:               "Gilgamesh",
		Datacenter:          "Aether",
		Region:              "NA",
		Race:                "Hyur",
		Tribe:               "Midlander",
		FirstSeenAt:         recent,
		LatestAchievementAt: &recent,
	}, nil)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:          3002,
		Name:        "Cerberus Mage",
		World:       "Cerberus",
		Datacenter:  "Chaos",
		Region:      "EU",
		Race:        "Elezen",
		Tribe:       "Duskwight",
		FirstSeenAt: recent,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/ui/worlds", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.Worlds(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Gilgamesh") {
		t.Errorf("expected body to contain 'Gilgamesh', got:\n%s", body)
	}
	if !strings.Contains(body, "Cerberus") {
		t.Errorf("expected body to contain 'Cerberus', got:\n%s", body)
	}
	if !strings.Contains(body, "Aether") {
		t.Errorf("expected body to contain 'Aether', got:\n%s", body)
	}
}

func TestWorldsCascadingFilters(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:          5001,
		Name:        "NA Player",
		World:       "Gilgamesh",
		Datacenter:  "Aether",
		Region:      "NA",
		Race:        "Hyur",
		Tribe:       "Midlander",
		FirstSeenAt: recent,
	}, nil)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:          5002,
		Name:        "EU Player",
		World:       "Cerberus",
		Datacenter:  "Chaos",
		Region:      "EU",
		Race:        "Elezen",
		Tribe:       "Wildwood",
		FirstSeenAt: recent,
	}, nil)

	// region=NA → DC dropdown should contain Aether, not Chaos/Light
	reqNA := httptest.NewRequest(http.MethodGet, "/ui/worlds?region=NA", nil)
	recNA := httptest.NewRecorder()
	rig.ctrl.Worlds(recNA, reqNA)

	if recNA.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recNA.Code)
	}
	bodyNA := recNA.Body.String()
	if !strings.Contains(bodyNA, `<option value="Aether"`) {
		t.Error("region=NA: expected DC dropdown to contain Aether")
	}
	if strings.Contains(bodyNA, `<option value="Chaos"`) {
		t.Error("region=NA: DC dropdown should NOT contain Chaos")
	}
	if strings.Contains(bodyNA, `<option value="Light"`) {
		t.Error("region=NA: DC dropdown should NOT contain Light")
	}
}
