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

func TestRacesHandler(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:                  2001,
		Name:                "Miqo'te Adventurer",
		World:               "Balmung",
		Datacenter:          "Crystal",
		Region:              "NA",
		Race:                "Miqo'te",
		Tribe:               "Seeker of the Sun",
		FirstSeenAt:         recent,
		LatestAchievementAt: &recent,
	}, nil)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:          2002,
		Name:        "Lalafell Merchant",
		World:       "Ragnarok",
		Datacenter:  "Chaos",
		Region:      "EU",
		Race:        "Lalafell",
		Tribe:       "Dunesfolk",
		FirstSeenAt: recent,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/ui/races", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.Races(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Miqo&#39;te") && !strings.Contains(body, "Miqo'te") {
		t.Errorf("expected body to contain Miqo'te, got:\n%s", body)
	}
	if !strings.Contains(body, "Lalafell") {
		t.Errorf("expected body to contain Lalafell, got:\n%s", body)
	}
	if !strings.Contains(body, "Race Distribution") {
		t.Errorf("expected body to contain 'Race Distribution', got:\n%s", body)
	}

	// Filtered request: Datacenter Chaos (should exclude Miqo'te on Balmung)
	reqFiltered := httptest.NewRequest(http.MethodGet, "/ui/races?dc=Chaos", nil)
	recFiltered := httptest.NewRecorder()
	rig.ctrl.Races(recFiltered, reqFiltered)

	if recFiltered.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recFiltered.Code, recFiltered.Body.String())
	}
	bodyFiltered := recFiltered.Body.String()
	if !strings.Contains(bodyFiltered, "Lalafell") {
		t.Errorf("expected filtered body to contain Lalafell, got:\n%s", bodyFiltered)
	}
	if strings.Contains(bodyFiltered, "Miqo&#39;te") || strings.Contains(bodyFiltered, "Miqo'te") {
		t.Errorf("expected filtered body to exclude Miqo'te on Crystal DC")
	}
}
