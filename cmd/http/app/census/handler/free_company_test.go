package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	census "github.com/mihaiflorentin88/ffxiv-census/domain/census"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

type fcTestRig struct {
	svc  *census.Service
	fcs  *mockrepo.FreeCompanyRepository
	ctrl *FreeCompanyController
}

func newFCRig(t *testing.T) *fcTestRig {
	t.Helper()
	chars := mockrepo.NewCharacterFake()
	fcs := mockrepo.NewFreeCompanyFake()
	ach := mockrepo.NewAchievementFake()
	svc := census.NewService(chars, fcs, ach, mockrepo.NewCensusRunFake())
	return &fcTestRig{
		svc:  svc,
		fcs:  fcs,
		ctrl: NewFreeCompanyController(svc),
	}
}

func TestFreeCompanyController_List(t *testing.T) {
	rig := newFCRig(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	formed := now.Add(-100 * 24 * time.Hour)

	_ = rig.fcs.Upsert(ctx, contract.FreeCompanyRecord{
		ID:          "fc1",
		Name:        "Scions of Seventh Dawn",
		World:       "Ultros",
		Datacenter:  "Primal",
		MemberCount: 15,
		FormedAt:    &formed,
		LastSeenAt:  now,
	})
	_ = rig.fcs.Upsert(ctx, contract.FreeCompanyRecord{
		ID:          "fc2",
		Name:        "Garlean Empire",
		World:       "Ragnarok",
		Datacenter:  "Chaos",
		MemberCount: 100,
		LastSeenAt:  now,
	})

	// List all
	var res response.PaginatedFreeCompanies
	decodeJSON(t, doGET(t, rig.ctrl.List, "/api/v1/census/free-companies"), &res)
	if res.Total != 2 || len(res.Items) != 2 {
		t.Fatalf("expected total 2, items 2; got total=%d items=%d", res.Total, len(res.Items))
	}

	// Filter by World
	var filtered response.PaginatedFreeCompanies
	decodeJSON(t, doGET(t, rig.ctrl.List, "/api/v1/census/free-companies?world=Ultros"), &filtered)
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].ID != "fc1" {
		t.Fatalf("expected 1 item on Ultros, got %+v", filtered)
	}

	// Invalid limit
	rec := doGET(t, rig.ctrl.List, "/api/v1/census/free-companies?limit=abc")
	assertError(t, rec, http.StatusBadRequest, "invalid limit")

	// Invalid offset
	rec = doGET(t, rig.ctrl.List, "/api/v1/census/free-companies?offset=-1")
	assertError(t, rec, http.StatusBadRequest, "invalid offset")
}

func TestFreeCompanyController_Get(t *testing.T) {
	rig := newFCRig(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	_ = rig.fcs.Upsert(ctx, contract.FreeCompanyRecord{
		ID:          "fc123",
		Name:        "Crystal Braves",
		World:       "Cerberus",
		Datacenter:  "Chaos",
		MemberCount: 45,
		LastSeenAt:  now,
	})

	// Found
	req := httptest.NewRequest(http.MethodGet, "/api/v1/census/free-companies/fc123", nil)
	req.SetPathValue("id", "fc123")
	rec := httptest.NewRecorder()
	rig.ctrl.Get(rec, req)

	var detail response.FreeCompanyDetail
	decodeJSON(t, rec, &detail)
	if detail.ID != "fc123" || detail.Name != "Crystal Braves" || detail.MemberCount != 45 {
		t.Errorf("unexpected detail: %+v", detail)
	}

	// Not found
	reqNF := httptest.NewRequest(http.MethodGet, "/api/v1/census/free-companies/unknown", nil)
	reqNF.SetPathValue("id", "unknown")
	recNF := httptest.NewRecorder()
	rig.ctrl.Get(recNF, reqNF)
	assertError(t, recNF, http.StatusNotFound, "free company not found")
}
