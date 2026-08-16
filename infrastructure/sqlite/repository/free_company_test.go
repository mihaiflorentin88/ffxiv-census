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
