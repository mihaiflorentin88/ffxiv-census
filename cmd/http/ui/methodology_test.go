package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMethodologyHandler(t *testing.T) {
	rig := newTestRig(t)

	req := httptest.NewRequest(http.MethodGet, "/ui/methodology", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.Methodology(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := strings.Join(strings.Fields(rec.Body.String()), " ")
	if !strings.Contains(body, "Census Methodology &amp; Metrics") {
		t.Errorf("expected body to contain 'Census Methodology &amp; Metrics', got:\n%s", body)
	}
	if !strings.Contains(body, "My Little Chocobo") {
		t.Errorf("expected body to contain 'My Little Chocobo', got:\n%s", body)
	}
	if !strings.Contains(body, "Achievement ID 590") {
		t.Errorf("expected body to contain 'Achievement ID 590', got:\n%s", body)
	}
	if !strings.Contains(body, "Active Player Criteria") {
		t.Errorf("expected body to contain 'Active Player Criteria', got:\n%s", body)
	}
	if !strings.Contains(body, "conservative progression signal") {
		t.Errorf("expected body to describe the conservative progression signal, got:\n%s", body)
	}
	if !strings.Contains(body, "Main Scenario Quest (MSQ) Progression") {
		t.Errorf("expected body to contain 'Main Scenario Quest (MSQ) Progression', got:\n%s", body)
	}
	if !strings.Contains(body, "Dawntrail") || !strings.Contains(body, "Lv. 100") {
		t.Errorf("expected body to contain Dawntrail and Lv. 100, got:\n%s", body)
	}
	if !strings.Contains(body, "A Realm Reborn") || !strings.Contains(body, "Lv. 50") {
		t.Errorf("expected body to contain A Realm Reborn and Lv. 50, got:\n%s", body)
	}
}
