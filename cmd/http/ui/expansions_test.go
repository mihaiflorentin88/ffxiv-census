package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	census "github.com/mihaiflorentin88/ffxiv-census/domain/census"
	mockqueue "github.com/mihaiflorentin88/ffxiv-census/mock/queue"
	mockrepo "github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestExpansionsHandler(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID:                  4001,
		Name:                "Warrior of Light",
		World:               "Balmung",
		Datacenter:          "Crystal",
		Region:              "NA",
		Race:                "Hyur",
		Tribe:               "Midlander",
		FirstSeenAt:         recent,
		LatestAchievementAt: &recent,
	}, nil)

	// Sync milestones to fake repo
	_ = rig.ach.SyncMilestones(context.Background(), []contract.MilestoneAchievement{
		{AchievementID: 1139, Kind: contract.MilestoneKindExpansion, Expansion: stringPtr("Heavensward"), Detail: "Looking Up"},
		{AchievementID: 1794, Kind: contract.MilestoneKindExpansion, Expansion: stringPtr("Stormblood"), Detail: "The Measure of His Reach"},
		{AchievementID: 2298, Kind: contract.MilestoneKindExpansion, Expansion: stringPtr("Shadowbringers"), Detail: "Shadowbringers"},
		{AchievementID: 2958, Kind: contract.MilestoneKindExpansion, Expansion: stringPtr("Endwalker"), Detail: "That Its Chorus Might Ring for All"},
		{AchievementID: 3496, Kind: contract.MilestoneKindExpansion, Expansion: stringPtr("Dawntrail"), Detail: "In the Glow of a New Dawn"},
	})

	_ = rig.ach.UpsertCharacterMilestones(context.Background(), 4001, []contract.CharacterMilestone{
		{CharacterID: 4001, AchievementID: 1139, AchievedAt: recent},
		{CharacterID: 4001, AchievementID: 1794, AchievedAt: recent},
		{CharacterID: 4001, AchievementID: 2298, AchievedAt: recent},
		{CharacterID: 4001, AchievementID: 2958, AchievedAt: recent},
		{CharacterID: 4001, AchievementID: 3496, AchievedAt: recent},
	})

	req := httptest.NewRequest(http.MethodGet, "/ui/expansions", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.Expansions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Expansion Progression Funnel") {
		t.Errorf("expected body to contain 'Expansion Progression Funnel', got:\n%s", body)
	}
	if !strings.Contains(body, "Dawntrail") {
		t.Errorf("expected body to contain Dawntrail, got:\n%s", body)
	}
	if !strings.Contains(body, "Endwalker") {
		t.Errorf("expected body to contain Endwalker, got:\n%s", body)
	}
}

// barrierExpansionsCharRepo gates Count calls behind a barrier.
type barrierExpansionsCharRepo struct {
	*mockrepo.CharacterRepository
	countBarrier *barrier
}

func (b *barrierExpansionsCharRepo) Count(ctx context.Context, filter contract.CharacterFilter) (int64, error) {
	b.countBarrier.enter()
	return b.CharacterRepository.Count(ctx, filter)
}

// barrier is a synchronisation primitive for proving concurrent execution.
type barrier struct {
	mu      sync.Mutex
	entered int
	expect  int
	ready   chan struct{}
	release chan struct{}
}

func newBarrier(expect int) *barrier {
	return &barrier{
		expect:  expect,
		ready:   make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *barrier) enter() {
	b.mu.Lock()
	b.entered++
	if b.entered >= b.expect {
		close(b.ready)
	}
	b.mu.Unlock()
	<-b.release
}

func (b *barrier) releaseAll() {
	<-b.ready
	close(b.release)
}

func TestExpansionsHandlerQueriesRunConcurrently(t *testing.T) {
	chars := mockrepo.NewCharacterFake()
	ach := mockrepo.NewAchievementFake()
	runs := mockrepo.NewCensusRunFake()

	// Expansions handler calls Summary (which calls Count, CountActive, Count)
	// and ExpansionCompletions. We barrier Count to prove Summary's internal
	// queries and ExpansionCompletions don't block each other.
	b := newBarrier(2) // Summary's first Count + ExpansionCompletions
	wrappedChars := &barrierExpansionsCharRepo{
		CharacterRepository: chars,
		countBarrier:        b,
	}
	svc := census.NewService(wrappedChars, ach, runs)
	q := mockqueue.NewFake()
	ctrl := NewUIController(svc, q)

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "/ui/expansions", nil)
		rec := httptest.NewRecorder()
		ctrl.Expansions(rec, req)
	}()

	b.releaseAll()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Expansions handler did not complete: queries are serial, not concurrent")
	}
}

func stringPtr(s string) *string {
	return &s
}
