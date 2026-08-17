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

func TestWorldDetailHandler(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:                  3001,
		Name:                "Louisoix Warrior",
		World:               "Louisoix",
		Datacenter:          "Chaos",
		Region:              "EU",
		Race:                "Hyur",
		FirstSeenAt:         recent,
		LatestAchievementAt: &recent,
	}, nil)

	rig.ach.ChocoboCountResponse = 1
	rig.ach.ExpansionsResponse = []contract.ExpansionCount{{Expansion: "Dawntrail", Count: 1}}
	rig.ach.NewCharactersResponse = []contract.DailyCount{{Day: "2026-08-17", Count: 1}}

	req := httptest.NewRequest(http.MethodGet, "/ui/worlds/Louisoix", nil)
	req.SetPathValue("world", "Louisoix")
	rec := httptest.NewRecorder()
	rig.ctrl.WorldDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Louisoix") {
		t.Errorf("expected body to contain Louisoix, got:\n%s", body)
	}
	if !strings.Contains(body, "Chaos") {
		t.Errorf("expected body to contain Chaos datacenter, got:\n%s", body)
	}
	if !strings.Contains(body, "Dawntrail") {
		t.Errorf("expected body to contain Dawntrail expansion, got:\n%s", body)
	}
}

func TestWorldDetailHandler_EmptyWorldRedirect(t *testing.T) {
	rig := newTestRig(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/worlds/", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.WorldDetail(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected status 302 redirect, got %d", rec.Code)
	}
}
