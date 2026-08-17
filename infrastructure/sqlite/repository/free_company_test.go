package repository

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestFCRepo(t *testing.T) contract.FreeCompanyRepository {
	t.Helper()
	driver, cleanup := newTestDriver(t)
	t.Cleanup(cleanup)
	return NewFreeCompanyRepository(driver)
}

func TestFreeCompanyRepository_UpsertAndGet(t *testing.T) {
	repo := newTestFCRepo(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	formed := now.Add(-24 * 30 * time.Hour)
	rec := contract.FreeCompanyRecord{
		ID: "9234567890123456789", Name: "The Scions", World: "Ultros",
		Datacenter: "Primal", MemberCount: 42, FormedAt: &formed, LastSeenAt: now,
	}
	if err := repo.Upsert(context.Background(), rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := repo.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.Name != "The Scions" || got.MemberCount != 42 {
		t.Errorf("got %+v", got)
	}
}

func TestFreeCompanyRepository_GetNotFound(t *testing.T) {
	repo := newTestFCRepo(t)
	got, err := repo.Get(context.Background(), "0000000000000000000")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestFreeCompanyRepository_ListAndCount(t *testing.T) {
	repo := newTestFCRepo(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	formed1 := now.Add(-100 * 24 * time.Hour)
	formed2 := now.Add(-50 * 24 * time.Hour)

	fcs := []contract.FreeCompanyRecord{
		{
			ID:          "fc1",
			Name:        "Alpha Wolves",
			World:       "Cerberus",
			Datacenter:  "Chaos",
			MemberCount: 150,
			FormedAt:    &formed1,
			LastSeenAt:  now,
		},
		{
			ID:          "fc2",
			Name:        "Beta Bears",
			World:       "Ragnarok",
			Datacenter:  "Chaos",
			MemberCount: 50,
			FormedAt:    &formed2,
			LastSeenAt:  now,
		},
		{
			ID:          "fc3",
			Name:        "Alpha Cats",
			World:       "Cerberus",
			Datacenter:  "Chaos",
			MemberCount: 200,
			FormedAt:    nil,
			LastSeenAt:  now,
		},
	}

	for _, fc := range fcs {
		if err := repo.Upsert(ctx, fc); err != nil {
			t.Fatalf("Upsert(%s): %v", fc.ID, err)
		}
	}

	// Count total
	total, err := repo.Count(ctx, contract.FreeCompanyFilter{})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected count 3, got %d", total)
	}

	// Filter by Name substring
	list, err := repo.List(ctx, contract.FreeCompanyFilter{Name: "Alpha"}, 10, 0)
	if err != nil {
		t.Fatalf("List name filter: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 FCs with 'Alpha', got %d", len(list))
	}

	// Filter by World
	count, err := repo.Count(ctx, contract.FreeCompanyFilter{World: "Ragnarok"})
	if err != nil {
		t.Fatalf("Count world: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 FC on Ragnarok, got %d", count)
	}

	// Sort by member_count ASC
	list, err = repo.List(ctx, contract.FreeCompanyFilter{SortBy: "member_count", SortOrder: "asc"}, 10, 0)
	if err != nil {
		t.Fatalf("List sorted by member_count: %v", err)
	}
	if len(list) != 3 || list[0].ID != "fc2" || list[1].ID != "fc1" || list[2].ID != "fc3" {
		t.Errorf("unexpected sorting: %+v", list)
	}

	// Sort by name DESC
	list, err = repo.List(ctx, contract.FreeCompanyFilter{SortBy: "name", SortOrder: "desc"}, 10, 0)
	if err != nil {
		t.Fatalf("List sorted by name desc: %v", err)
	}
	if len(list) != 3 || list[0].ID != "fc2" || list[1].ID != "fc1" || list[2].ID != "fc3" {
		t.Errorf("unexpected sorting by name desc: %+v", list)
	}
}
