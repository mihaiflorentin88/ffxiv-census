package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestFreeCompanyRepository_UpsertAndGet(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewFreeCompanyRepository(driver)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Millisecond)
	formed := now.Add(-1000 * time.Hour)
	rec := contract.FreeCompanyRecord{
		ID:          "fc-12345",
		Name:        "Scions of the Seventh Dawn",
		World:       "Louisoix",
		Datacenter:  "Chaos",
		MemberCount: 12,
		FormedAt:    &formed,
		LastSeenAt:  now,
	}

	if err := repo.Upsert(ctx, rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.Get(ctx, "fc-12345")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected free company, got nil")
	}
	if got.Name != rec.Name || got.MemberCount != 12 {
		t.Errorf("got name=%q members=%d", got.Name, got.MemberCount)
	}
}

func TestFreeCompanyRepository_ListAndCount(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewFreeCompanyRepository(driver)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := 1; i <= 3; i++ {
		_ = repo.Upsert(ctx, contract.FreeCompanyRecord{
			ID:          "fc-" + string(rune('0'+i)),
			Name:        "Guild",
			World:       "Louisoix",
			Datacenter:  "Chaos",
			MemberCount: uint32(i * 5),
			LastSeenAt:  now,
		})
	}

	count, err := repo.Count(ctx, contract.FreeCompanyFilter{World: "Louisoix"})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected count=3, got %d", count)
	}
}
