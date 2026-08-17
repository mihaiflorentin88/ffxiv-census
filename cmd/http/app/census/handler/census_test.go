package handler

import (
	"context"
	"encoding/json"
	"github.com/xivapi/godestone/v2"
	"github.com/xivapi/godestone/v2/data/gender"
	"github.com/xivapi/godestone/v2/provider/models"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	census "github.com/mihaiflorentin88/ffxiv-census/domain/census"
	mockqueue "github.com/mihaiflorentin88/ffxiv-census/mock/queue"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

// testRig wires the real domain service on top of in-memory fakes, exactly as
// routes.go does, so handler tests exercise the full service -> repo path.
type testRig struct {
	svc   *census.Service
	chars *mockrepo.CharacterRepository
	fcs   *mockrepo.FreeCompanyRepository
	ach   *mockrepo.AchievementRepository
	q     *mockqueue.Fake
	c     *CensusController
	qc    *QueueController
}

func newRig(t *testing.T) *testRig {
	t.Helper()
	chars := mockrepo.NewCharacterFake()
	fcs := mockrepo.NewFreeCompanyFake()
	ach := mockrepo.NewAchievementFake()
	ach.SetCharacterRepo(chars)
	svc := census.NewService(chars, fcs, ach, mockrepo.NewCensusRunFake())
	q := mockqueue.NewFake()
	return &testRig{
		svc: svc, chars: chars, fcs: fcs, ach: ach, q: q,
		c:  NewCensusController(svc),
		qc: NewQueueController(q),
	}
}

func (r *testRig) seed(t *testing.T, char *godestone.Character) {
	t.Helper()
	if err := r.svc.UpsertCharacter(context.Background(), char); err != nil {
		t.Fatalf("UpsertCharacter(%d): %v", char.ID, err)
	}
}

func doGET(t *testing.T, h http.HandlerFunc, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantMsg string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, wantStatus, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body %q is not JSON: %v", rec.Body.String(), err)
	}
	if msg, ok := body["error"]; !ok || !strings.Contains(msg, wantMsg) {
		t.Errorf("error body = %q, want it to contain %q", body["error"], wantMsg)
	}
}

func TestCensusController_Latest(t *testing.T) {
	rig := newRig(t)
	rig.seed(t, &godestone.Character{ID: 1, Name: "Tataru", World: "Ultros", DC: "Primal", Gender: gender.Female})
	rig.seed(t, &godestone.Character{ID: 2, Name: "Moen", World: "Ultros", DC: "Primal"})

	var body response.CensusSummary
	decodeJSON(t, doGET(t, rig.c.Latest, "/api/v1/census/latest"), &body)
	if body.TotalCharacters != 2 {
		t.Errorf("total_characters = %d, want 2", body.TotalCharacters)
	}
	if body.ActiveCharacters != 0 {
		t.Errorf("active_characters = %d, want 0 (no achievements ingested)", body.ActiveCharacters)
	}
	if body.ActiveRatio != 0 {
		t.Errorf("active_ratio = %v, want 0", body.ActiveRatio)
	}
}

func TestCensusController_Latest_NilService(t *testing.T) {
	c := NewCensusController(nil)
	assertError(t, doGET(t, c.Latest, "/api/v1/census/latest"), http.StatusInternalServerError, "census service unavailable")
}

func TestCensusController_List(t *testing.T) {
	rig := newRig(t)
	for _, id := range []uint32{1, 2, 3} {
		rig.seed(t, &godestone.Character{ID: id, Name: "Char", World: "Ultros", DC: "Primal"})
	}

	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantItems  int
		wantTotal  int64
		wantLimit  int
		wantOffset int
	}{
		{name: "defaults", query: "", wantStatus: 200, wantItems: 3, wantTotal: 3, wantLimit: 100, wantOffset: 0},
		{name: "page one", query: "limit=2&offset=0", wantStatus: 200, wantItems: 2, wantTotal: 3, wantLimit: 2, wantOffset: 0},
		{name: "page two", query: "limit=2&offset=2", wantStatus: 200, wantItems: 1, wantTotal: 3, wantLimit: 2, wantOffset: 2},
		{name: "limit clamped", query: "limit=1000", wantStatus: 200, wantItems: 3, wantTotal: 3, wantLimit: 500, wantOffset: 0},
		{name: "invalid limit", query: "limit=abc", wantStatus: 400},
		{name: "zero limit", query: "limit=0", wantStatus: 400},
		{name: "negative limit", query: "limit=-1", wantStatus: 400},
		{name: "negative offset", query: "offset=-5", wantStatus: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doGET(t, rig.c.List, "/api/v1/census/characters"+querySuffix(tt.query))
			if tt.wantStatus != http.StatusOK {
				wantMsg := "limit"
				if tt.name == "negative offset" {
					wantMsg = "offset"
				}
				assertError(t, rec, tt.wantStatus, wantMsg)
				return
			}
			var body response.PaginatedCharacters
			decodeJSON(t, rec, &body)
			if len(body.Items) != tt.wantItems {
				t.Errorf("items = %d, want %d", len(body.Items), tt.wantItems)
			}
			if body.Total != tt.wantTotal {
				t.Errorf("total = %d, want %d", body.Total, tt.wantTotal)
			}
			if body.Limit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", body.Limit, tt.wantLimit)
			}
			if body.Offset != tt.wantOffset {
				t.Errorf("offset = %d, want %d", body.Offset, tt.wantOffset)
			}
			// DTO field pass-through on the first item.
			if len(body.Items) > 0 {
				first := body.Items[0]
				if first.Name != "Char" || first.World != "Ultros" || first.Datacenter != "Primal" || first.Region != "NA" {
					t.Errorf("first item fields = %+v, want name Char/world Ultros/datacenter Primal/region NA", first)
				}
			}
		})
	}
}

func TestCensusController_List_Filters(t *testing.T) {
	rig := newRig(t)
	rig.seed(t, &godestone.Character{ID: 1, Name: "Feed How", World: "Louisoix", DC: "Chaos", Race: &models.GenderedEntity{Name: "Au Ra"}})
	rig.seed(t, &godestone.Character{ID: 2, Name: "Ninto Thegen", World: "Louisoix", DC: "Chaos", Race: &models.GenderedEntity{Name: "Miqo'te"}})
	rig.seed(t, &godestone.Character{ID: 3, Name: "Ahribella White", World: "Zodiark", DC: "Light", Race: &models.GenderedEntity{Name: "Miqo'te"}})
	rig.seed(t, &godestone.Character{ID: 4, Name: "Alpha Test", World: "Ultros", DC: "Primal", Race: &models.GenderedEntity{Name: "Hyur"}})

	tests := []struct {
		name      string
		query     string
		wantIDs   []uint32
		wantTotal int64
	}{
		{name: "filter by world", query: "world=Louisoix", wantIDs: []uint32{1, 2}, wantTotal: 2},
		{name: "filter by datacenter", query: "datacenter=Chaos", wantIDs: []uint32{1, 2}, wantTotal: 2},
		{name: "filter by region", query: "region=EU", wantIDs: []uint32{1, 2, 3}, wantTotal: 3},
		{name: "filter by race", query: "race=Miqo%27te", wantIDs: []uint32{2, 3}, wantTotal: 2},
		{name: "filter by name substring", query: "name=feed", wantIDs: []uint32{1}, wantTotal: 1},
		{name: "combined filters", query: "world=Louisoix&race=Miqo%27te", wantIDs: []uint32{2}, wantTotal: 1},
		{name: "no match", query: "world=Balmung", wantIDs: nil, wantTotal: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doGET(t, rig.c.List, "/api/v1/census/characters"+querySuffix(tt.query))
			var body response.PaginatedCharacters
			decodeJSON(t, rec, &body)
			if body.Total != tt.wantTotal {
				t.Errorf("total = %d, want %d", body.Total, tt.wantTotal)
			}
			var gotIDs []uint32
			for _, item := range body.Items {
				gotIDs = append(gotIDs, item.ID)
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("got ids %v, want %v", gotIDs, tt.wantIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != tt.wantIDs[i] {
					t.Fatalf("got ids %v, want %v", gotIDs, tt.wantIDs)
				}
			}
		})
	}
}

func TestCensusController_Get(t *testing.T) {
	rig := newRig(t)
	now := time.Now().UTC()
	rig.seed(t, &godestone.Character{
		ID:              1,
		Name:            "Tataru Taru",
		World:           "Ultros",
		DC:              "Primal",
		Gender:          gender.Female,
		Race:            &models.GenderedEntity{Name: "Lalafell"},
		FreeCompanyID:   "9234567890123456789",
		FreeCompanyName: "The Scions",
		ClassJobs: []*godestone.ClassJob{
			{JobID: 19, Name: "Paladin", Level: 90, ExpLevel: 12345},
		},
	})
	_ = rig.ach.UpsertCharacterMilestones(context.Background(), 1, []contract.CharacterMilestone{
		{CharacterID: 1, AchievementID: 590, AchievedAt: now},
	})
	_ = rig.fcs.Upsert(context.Background(), contract.FreeCompanyRecord{
		ID: "9234567890123456789", Name: "The Scions", World: "Ultros",
		Datacenter: "Primal", MemberCount: 42, LastSeenAt: now,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/census/characters/1", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	rig.c.Get(rec, req)

	var body response.CharacterDetail
	decodeJSON(t, rec, &body)
	if body.Character.ID != 1 || body.Character.Name != "Tataru Taru" {
		t.Errorf("character = %+v, want id 1 / Tataru Taru", body.Character)
	}
	if body.Character.FreeCompanyID == nil || *body.Character.FreeCompanyID != "9234567890123456789" {
		t.Errorf("free_company_id = %v, want passthrough", body.Character.FreeCompanyID)
	}
	if len(body.Jobs) != 1 || body.Jobs[0].Name != "Paladin" || body.Jobs[0].Level != 90 || body.Jobs[0].ExpLevel != 12345 {
		t.Errorf("jobs = %+v, want one Paladin 90", body.Jobs)
	}
	if len(body.Milestones) != 1 || body.Milestones[0].AchievementID != 590 {
		t.Errorf("milestones = %+v, want one 590", body.Milestones)
	}
	if body.FreeCompany == nil || body.FreeCompany.Name != "The Scions" || body.FreeCompany.MemberCount != 42 {
		t.Errorf("free_company = %+v, want The Scions (42)", body.FreeCompany)
	}
}

func TestCensusController_Get_NotFound(t *testing.T) {
	rig := newRig(t)
	rig.seed(t, &godestone.Character{ID: 1, Name: "Char", World: "Ultros", DC: "Primal"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/census/characters/999", nil)
	req.SetPathValue("id", "999")
	rec := httptest.NewRecorder()
	rig.c.Get(rec, req)
	assertError(t, rec, http.StatusNotFound, "character not found")
}

func TestCensusController_Get_InvalidID(t *testing.T) {
	rig := newRig(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/census/characters/abc", nil)
	req.SetPathValue("id", "abc")
	rec := httptest.NewRecorder()
	rig.c.Get(rec, req)
	assertError(t, rec, http.StatusBadRequest, "invalid character id")
}

func TestCensusController_Breakdown(t *testing.T) {
	rig := newRig(t)
	rig.seed(t, &godestone.Character{ID: 1, Name: "A", World: "Ultros", DC: "Primal"})
	rig.seed(t, &godestone.Character{ID: 2, Name: "B", World: "Ultros", DC: "Primal"})
	rig.seed(t, &godestone.Character{ID: 3, Name: "C", World: "Moogle", DC: "Chaos"})

	var groups []response.BreakdownGroup
	decodeJSON(t, doGET(t, rig.c.Breakdown, "/api/v1/stats/breakdown?by=world"), &groups)
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want 2 worlds", groups)
	}
	if groups[0].Key != "Ultros" || groups[0].Total != 2 {
		t.Errorf("groups[0] = %+v, want Ultros total 2", groups[0])
	}
	if groups[1].Key != "Moogle" || groups[1].Total != 1 {
		t.Errorf("groups[1] = %+v, want Moogle total 1", groups[1])
	}
}

func TestCensusController_Breakdown_MissingBy(t *testing.T) {
	rig := newRig(t)
	assertError(t, doGET(t, rig.c.Breakdown, "/api/v1/stats/breakdown"), http.StatusBadRequest, "by")
}

func TestCensusController_Breakdown_InvalidDimension(t *testing.T) {
	rig := newRig(t)
	assertError(t, doGET(t, rig.c.Breakdown, "/api/v1/stats/breakdown?by=bogus"), http.StatusBadRequest, "invalid breakdown dimension")
}

func TestCensusController_NewCharacters(t *testing.T) {
	rig := newRig(t)
	// Seed the fake directly with a fixed FirstSeenAt so the expected UTC day
	// is deterministic — UpsertCharacter would stamp time.Now() internally,
	// which flakes across a UTC-midnight crossing. The handler and service
	// path (NewCharacters -> NewPerDay) is still fully exercised.
	if err := rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID: 1, Name: "A", World: "Ultros", Datacenter: "Primal", Region: "NA",
		FirstSeenAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var days []response.NewCharactersDay
	decodeJSON(t, doGET(t, rig.c.NewCharacters, "/api/v1/stats/new-characters?since=2026-08-01&until=2026-09-01"), &days)
	if len(days) != 1 {
		t.Fatalf("days = %+v, want exactly one bucket", days)
	}
	if days[0].Day != "2026-08-10" || days[0].Count != 1 {
		t.Errorf("days[0] = %+v, want day 2026-08-10 count 1", days[0])
	}
}

func TestCensusController_NewCharacters_MissingSince(t *testing.T) {
	rig := newRig(t)
	assertError(t, doGET(t, rig.c.NewCharacters, "/api/v1/stats/new-characters"), http.StatusBadRequest, "since")
}

func TestCensusController_NewCharacters_InvalidSince(t *testing.T) {
	rig := newRig(t)
	assertError(t, doGET(t, rig.c.NewCharacters, "/api/v1/stats/new-characters?since=notadate"), http.StatusBadRequest, "since")
}

func TestCensusController_Expansion(t *testing.T) {
	rig := newRig(t)
	ctx := context.Background()
	if err := rig.svc.SyncMilestones(ctx); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	now := time.Now().UTC()
	_ = rig.ach.UpsertCharacterMilestones(ctx, 1, []contract.CharacterMilestone{
		{CharacterID: 1, AchievementID: 1139, AchievedAt: now}, // Heavensward
	})
	_ = rig.ach.UpsertCharacterMilestones(ctx, 2, []contract.CharacterMilestone{
		{CharacterID: 2, AchievementID: 1794, AchievedAt: now}, // Stormblood
	})

	var all []response.ExpansionStat
	decodeJSON(t, doGET(t, rig.c.Expansion, "/api/v1/stats/expansion"), &all)
	if len(all) != 2 {
		t.Fatalf("expansion = %+v, want 2 entries", all)
	}
	if all[0].Expansion != "Heavensward" || all[0].Count != 1 {
		t.Errorf("all[0] = %+v, want Heavensward 1", all[0])
	}
	if all[1].Expansion != "Stormblood" || all[1].Count != 1 {
		t.Errorf("all[1] = %+v, want Stormblood 1", all[1])
	}
}

func TestCensusController_Expansion_NameFilter(t *testing.T) {
	rig := newRig(t)
	ctx := context.Background()
	if err := rig.svc.SyncMilestones(ctx); err != nil {
		t.Fatalf("SyncMilestones: %v", err)
	}
	now := time.Now().UTC()
	_ = rig.ach.UpsertCharacterMilestones(ctx, 1, []contract.CharacterMilestone{
		{CharacterID: 1, AchievementID: 1139, AchievedAt: now}, // Heavensward
	})

	var filtered []response.ExpansionStat
	decodeJSON(t, doGET(t, rig.c.Expansion, "/api/v1/stats/expansion?name=Heavensward"), &filtered)
	if len(filtered) != 1 || filtered[0].Expansion != "Heavensward" {
		t.Errorf("filtered = %+v, want only Heavensward", filtered)
	}

	// A name with no matches returns an empty list, not 404.
	var none []response.ExpansionStat
	decodeJSON(t, doGET(t, rig.c.Expansion, "/api/v1/stats/expansion?name=DoesNotExist"), &none)
	if len(none) != 0 {
		t.Errorf("none = %+v, want empty list", none)
	}
}

func TestQueueController_Depth(t *testing.T) {
	rig := newRig(t)
	if _, err := rig.q.Publish(context.Background(), contract.QueueJob{Type: "id-sweep", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var overview response.QueueOverviewResponse
	decodeJSON(t, doGET(t, rig.qc.Depth, "/api/v1/queue"), &overview)
	if overview.Summary.Pending != 1 || overview.Summary.Total != 1 {
		t.Errorf("summary = %+v, want pending=1, total=1", overview.Summary)
	}
	if len(overview.Events) != 4 {
		t.Errorf("events length = %d, want 4", len(overview.Events))
	}
}

func TestQueueController_Depth_NilQueue(t *testing.T) {
	qc := NewQueueController(nil)
	assertError(t, doGET(t, qc.Depth, "/api/v1/queue"), http.StatusInternalServerError, "queue")
}

func TestQueueController_Events(t *testing.T) {
	rig := newRig(t)
	ctx := context.Background()

	// Even with empty queue, canonical events are returned with 0 counts
	var events []response.QueueEventTypeSummary
	decodeJSON(t, doGET(t, rig.qc.Events, "/api/v1/queue/events"), &events)
	if len(events) != 4 {
		t.Fatalf("events count = %d, want 4", len(events))
	}
	if events[0].Type != "id-sweep" || events[0].Description == "" {
		t.Errorf("unexpected event 0: %+v", events[0])
	}
	if events[0].NextJobs == nil || events[0].ActiveJobs == nil || events[0].FailedJobs == nil {
		t.Errorf("expected initialized job slices, got %+v", events[0])
	}

	// Publish and transition jobs
	_, _ = rig.q.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":10}`)},
		contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":10}`)},
		contract.QueueJob{Type: "fc-census", Payload: []byte(`{"id":"failed"}`)},
	)
	claimed, _ := rig.q.Claim(ctx, "character-census", 1)
	_ = rig.q.Complete(ctx, claimed[0].ID)

	claimedFC, _ := rig.q.Claim(ctx, "fc-census", 1)
	_ = rig.q.Fail(ctx, claimedFC[0].ID, "lodestone parse error")

	events = nil
	decodeJSON(t, doGET(t, rig.qc.Events, "/api/v1/queue/events?sample_limit=10"), &events)
	if len(events) != 4 {
		t.Fatalf("events count = %d, want 4", len(events))
	}

	// id-sweep has 1 pending and 1 next job
	if events[0].Type != "id-sweep" || events[0].Pending != 1 || events[0].Total != 1 || len(events[0].NextJobs) != 1 {
		t.Errorf("id-sweep event stats: %+v", events[0])
	}
	// character-census has 1 done
	if events[1].Type != "character-census" || events[1].Done != 1 || events[1].Total != 1 {
		t.Errorf("character-census event stats: %+v", events[1])
	}
	// fc-census has 1 failed and 1 failed job with last_error
	if events[3].Type != "fc-census" || events[3].Failed != 1 || len(events[3].FailedJobs) != 1 {
		t.Errorf("fc-census event stats: %+v", events[3])
	}
	if events[3].FailedJobs[0].LastError == nil || *events[3].FailedJobs[0].LastError != "lodestone parse error" {
		t.Errorf("expected last_error on failed job: %+v", events[3].FailedJobs[0])
	}
}

func TestQueueController_ListJobs_FiltersAndPagination(t *testing.T) {
	rig := newRig(t)
	ctx := context.Background()

	_, _ = rig.q.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":2}`)},
		contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":10}`)},
		contract.QueueJob{Type: "achievement-census", Payload: []byte(`{"id":10}`)},
	)
	claimed, _ := rig.q.Claim(ctx, "character-census", 1)
	_ = rig.q.Complete(ctx, claimed[0].ID)

	claimedAch, _ := rig.q.Claim(ctx, "achievement-census", 1)
	_ = rig.q.Fail(ctx, claimedAch[0].ID, "permanent error")
	// List all jobs
	var res response.PaginatedQueueJobs
	decodeJSON(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs"), &res)
	if res.Total != 4 || len(res.Items) != 4 {
		t.Fatalf("all jobs total = %d, items = %d, want 4, 4", res.Total, len(res.Items))
	}

	// Filter by type
	res = response.PaginatedQueueJobs{}
	decodeJSON(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs?type=id-sweep"), &res)
	if res.Total != 2 || len(res.Items) != 2 {
		t.Fatalf("id-sweep jobs total = %d, items = %d, want 2, 2", res.Total, len(res.Items))
	}
	for _, item := range res.Items {
		if item.Type != "id-sweep" {
			t.Errorf("item type = %s, want id-sweep", item.Type)
		}
	}

	// Filter by status
	res = response.PaginatedQueueJobs{}
	decodeJSON(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs?status=done"), &res)
	if res.Total != 1 || len(res.Items) != 1 || res.Items[0].Type != "character-census" {
		t.Fatalf("done jobs = %+v, want 1 character-census", res)
	}

	// Filter by type and status
	res = response.PaginatedQueueJobs{}
	decodeJSON(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs?type=id-sweep&status=pending"), &res)
	if res.Total != 2 || len(res.Items) != 2 {
		t.Fatalf("id-sweep pending jobs = %+v, want 2", res)
	}

	// Pagination
	res = response.PaginatedQueueJobs{}
	decodeJSON(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs?limit=2&offset=0"), &res)
	if res.Total != 4 || len(res.Items) != 2 || res.Limit != 2 || res.Offset != 0 {
		t.Fatalf("page 1 = %+v, want 2 items, limit 2, offset 0", res)
	}
}

func TestQueueController_ListJobs_InvalidParams(t *testing.T) {
	rig := newRig(t)

	assertError(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs?limit=-1"), http.StatusBadRequest, "invalid limit")
	assertError(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs?limit=0"), http.StatusBadRequest, "invalid limit")
	assertError(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs?limit=abc"), http.StatusBadRequest, "invalid limit")
	assertError(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs?offset=-1"), http.StatusBadRequest, "invalid offset")
	assertError(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs?offset=abc"), http.StatusBadRequest, "invalid offset")
	assertError(t, doGET(t, rig.qc.ListJobs, "/api/v1/queue/jobs?status=invalid_status"), http.StatusBadRequest, "invalid status")
}

func TestQueueController_GetJob_Found_NotFound_InvalidID(t *testing.T) {
	rig := newRig(t)
	ctx := context.Background()

	_, _ = rig.q.Publish(ctx, contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":100}`)})
	jobs, _ := rig.q.ListJobs(ctx, contract.QueueJobFilter{Type: "id-sweep"}, 1, 0)
	if len(jobs) != 1 {
		t.Fatalf("jobs len = %d, want 1", len(jobs))
	}
	jobID := jobs[0].ID

	// Valid GET
	req := httptest.NewRequest(http.MethodGet, "/api/v1/queue/jobs/1", nil)
	req.SetPathValue("id", strconv.FormatInt(jobID, 10))
	rec := httptest.NewRecorder()
	rig.qc.GetJob(rec, req)

	var item response.QueueJobItem
	decodeJSON(t, rec, &item)
	if item.ID != jobID || item.Type != "id-sweep" || string(item.Payload) != `{"from":1,"to":100}` {
		t.Errorf("unexpected job item: %+v", item)
	}
	if item.Status != "pending" {
		t.Errorf("status = %s, want pending", item.Status)
	}

	// Not Found
	reqNF := httptest.NewRequest(http.MethodGet, "/api/v1/queue/jobs/999999", nil)
	reqNF.SetPathValue("id", "999999")
	recNF := httptest.NewRecorder()
	rig.qc.GetJob(recNF, reqNF)
	assertError(t, recNF, http.StatusNotFound, "job not found")

	// Invalid ID
	reqInv := httptest.NewRequest(http.MethodGet, "/api/v1/queue/jobs/bad", nil)
	reqInv.SetPathValue("id", "bad")
	recInv := httptest.NewRecorder()
	rig.qc.GetJob(recInv, reqInv)
	assertError(t, recInv, http.StatusBadRequest, "invalid job id")

	// Negative ID
	reqNeg := httptest.NewRequest(http.MethodGet, "/api/v1/queue/jobs/-5", nil)
	reqNeg.SetPathValue("id", "-5")
	recNeg := httptest.NewRecorder()
	rig.qc.GetJob(recNeg, reqNeg)
	assertError(t, recNeg, http.StatusBadRequest, "invalid job id")
}

func TestQueueController_NilQueue(t *testing.T) {
	qc := NewQueueController(nil)

	assertError(t, doGET(t, qc.Events, "/api/v1/queue/events"), http.StatusInternalServerError, "queue service unavailable")
	assertError(t, doGET(t, qc.ListJobs, "/api/v1/queue/jobs"), http.StatusInternalServerError, "queue service unavailable")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/queue/jobs/1", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	qc.GetJob(rec, req)
	assertError(t, rec, http.StatusInternalServerError, "queue service unavailable")
}

func TestQueueController_RetryFailed(t *testing.T) {
	rig := newRig(t)
	ctx := context.Background()

	_, _ = rig.q.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":2}`)},
	)
	claimed, _ := rig.q.Claim(ctx, "id-sweep", 2)
	_ = rig.q.Fail(ctx, claimed[0].ID, "error 1")
	_ = rig.q.Fail(ctx, claimed[1].ID, "error 2")

	// POST /api/v1/queue/retry-failed via JSON body
	body := strings.NewReader(`{"type":"id-sweep","limit":10}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/queue/retry-failed", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	rig.qc.RetryFailed(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	var resp response.QueueRetryFailedResponse
	decodeJSON(t, rec, &resp)
	if resp.Retried != 2 {
		t.Fatalf("expected retried = 2, got %d", resp.Retried)
	}
}

func TestQueueController_Purge(t *testing.T) {
	rig := newRig(t)
	ctx := context.Background()

	_, _ = rig.q.Publish(ctx, contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":1}`)})
	claimed, _ := rig.q.Claim(ctx, "character-census", 1)
	_ = rig.q.Complete(ctx, claimed[0].ID)

	// POST /api/v1/queue/purge
	body := strings.NewReader(`{"status":"done","older_than":"0s"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/queue/purge", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	rig.qc.Purge(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	var resp response.QueuePurgeResponse
	decodeJSON(t, rec, &resp)
	if resp.Purged != 1 || resp.Status != "done" {
		t.Fatalf("unexpected purge response: %+v", resp)
	}

	// Invalid status
	reqInv := httptest.NewRequest(http.MethodPost, "/api/v1/queue/purge", strings.NewReader(`{"status":"invalid-status"}`))
	reqInv.Header.Set("Content-Type", "application/json")
	recInv := httptest.NewRecorder()
	rig.qc.Purge(recInv, reqInv)
	assertError(t, recInv, http.StatusBadRequest, "invalid status")
}

func querySuffix(q string) string {
	if q == "" {
		return ""
	}
	return "?" + q
}
