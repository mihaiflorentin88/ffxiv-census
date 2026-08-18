package repository_test

import (
	"context"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/postgres/repository"
)

func TestCensusRunRepository_StartAndFinish(t *testing.T) {
	driver := newTestDriver(t)
	repo := repository.NewCensusRunRepository(driver)
	ctx := context.Background()

	id, err := repo.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	if err := repo.Finish(ctx, id, 100, 10); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}
