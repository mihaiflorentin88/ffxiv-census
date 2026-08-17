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

func TestFreeCompanyListHandler(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	formed := now.Add(-500 * 24 * time.Hour)

	_ = rig.fcs.Upsert(context.Background(), contract.FreeCompanyRecord{
		ID:          "923456789",
		Name:        "Scions of Seventh Dawn",
		World:       "Balmung",
		Datacenter:  "Crystal",
		MemberCount: 42,
		FormedAt:    &formed,
		LastSeenAt:  now,
	})

	req := httptest.NewRequest(http.MethodGet, "/ui/free-companies", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.FreeCompanyList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Scions of Seventh Dawn") {
		t.Errorf("expected body to contain 'Scions of Seventh Dawn', got:\n%s", body)
	}
}

func TestFreeCompanyDetailHandler(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	formed := now.Add(-500 * 24 * time.Hour)
	fcID := "923456789"

	_ = rig.fcs.Upsert(context.Background(), contract.FreeCompanyRecord{
		ID:          fcID,
		Name:        "Scions of Seventh Dawn",
		World:       "Balmung",
		Datacenter:  "Crystal",
		MemberCount: 42,
		FormedAt:    &formed,
		LastSeenAt:  now,
	})

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:              12345,
		Name:            "Alphinaud Leveilleur",
		World:           "Balmung",
		Datacenter:      "Crystal",
		Region:          "NA",
		FreeCompanyID:   &fcID,
		FreeCompanyName: &fcID,
		FirstSeenAt:     now,
	}, nil)

	// Valid detail
	req := httptest.NewRequest(http.MethodGet, "/ui/free-companies/923456789", nil)
	req.SetPathValue("id", "923456789")
	rec := httptest.NewRecorder()
	rig.ctrl.FreeCompanyDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Scions of Seventh Dawn") {
		t.Errorf("expected body to contain 'Scions of Seventh Dawn', got:\n%s", body)
	}
	if !strings.Contains(body, "Alphinaud Leveilleur") {
		t.Errorf("expected body to contain member 'Alphinaud Leveilleur', got:\n%s", body)
	}

	// Not found
	reqNF := httptest.NewRequest(http.MethodGet, "/ui/free-companies/000000000", nil)
	reqNF.SetPathValue("id", "000000000")
	recNF := httptest.NewRecorder()
	rig.ctrl.FreeCompanyDetail(recNF, reqNF)

	if recNF.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", recNF.Code)
	}
}
