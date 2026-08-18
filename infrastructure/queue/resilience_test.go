package queue_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/queue"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite"
	sqlitemigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/migration"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func newTestQueue(t *testing.T) contract.Queue {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "queue_test.db")
	driver, err := sqlite.NewDriver(&config.SQLiteConfig{Path: dbPath}, sqlitemigration.FS())
	if err != nil {
		t.Fatalf("sqlite driver: %v", err)
	}
	cfg := &config.QueueConfig{
		ClaimBatchSize:     5,
		BackoffBaseSeconds: 1,
	}
	q, err := queue.NewQueue(driver, cfg, logging.Logger)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	return q
}

func TestQueue_ClaimMultiple(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	jobs := []contract.QueueJob{
		{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)},
		{Type: "character-census", Payload: []byte(`{"id":100}`)},
		{Type: "achievement-census", Payload: []byte(`{"id":100}`)},
		{Type: "fc-census", Payload: []byte(`{"id":"123"}`)},
	}

	n, err := q.Publish(ctx, jobs...)
	if err != nil || n != 4 {
		t.Fatalf("publish: n=%d err=%v", n, err)
	}

	// Claim only from id-sweep and fc-census
	claimed, err := q.ClaimMultiple(ctx, []string{"id-sweep", "fc-census"}, 10, contract.ClaimModeAny)
	if err != nil {
		t.Fatalf("claim multiple: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("expected 2 claimed jobs, got %d", len(claimed))
	}
	for _, j := range claimed {
		if j.Type != "id-sweep" && j.Type != "fc-census" {
			t.Fatalf("unexpected job type claimed: %s", j.Type)
		}
	}
}

func TestQueue_InfiniteRetries(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	// MaxAttempts = 0 => infinite retry
	job := contract.QueueJob{
		Type:        "id-sweep",
		Payload:     []byte(`{"chunk":999}`),
		MaxAttempts: 0,
	}
	if _, err := q.Publish(ctx, job); err != nil {
		t.Fatalf("publish: %v", err)
	}

	for i := 1; i <= 10; i++ {
		claimed, err := q.Claim(ctx, "id-sweep", 1, contract.ClaimModeAny)
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if len(claimed) != 1 {
			t.Fatalf("expected 1 job claimed at iteration %d, got %d", i, len(claimed))
		}
		if claimed[0].Attempts != i {
			t.Fatalf("expected attempts=%d, got %d", i, claimed[0].Attempts)
		}

		err = q.Retry(ctx, claimed[0].ID, fmt.Sprintf("retry error %d", i))
		if err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}

		depth, _ := q.Depth(ctx)
		if depth[contract.QueueJobFailed] != 0 {
			t.Fatalf("job marked failed at iteration %d with max_attempts=0", i)
		}
		if depth[contract.QueueJobPending] != 1 {
			t.Fatalf("job not pending at iteration %d", i)
		}

		// Advance clock so the backoff is satisfied for the next claim iteration
		if qConcrete, ok := q.(*queue.Queue); ok {
			offset := time.Duration(i*10) * time.Minute
			qConcrete.SetNowFunc(func() time.Time {
				return time.Now().Add(offset)
			})
		}
	}
}
