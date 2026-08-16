package repository

import (
	"context"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestRunRepo(t *testing.T) contract.CensusRunRepository {
	t.Helper()
	driver, cleanup := newTestDriver(t)
	t.Cleanup(cleanup)
	return NewCensusRunRepository(driver)
}

func TestCensusRunRepository_StartAndFinish(t *testing.T) {
	repo := newTestRunRepo(t)
	id, err := repo.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id <= 0 {
		t.Fatalf("id = %d, want > 0", id)
	}
	if err := repo.Finish(context.Background(), id, 1000, 50); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}
