package queue

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/config"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite"
	sqlitemigration "github.com/mihaiflorentin88/ffxiv-census/infrastructure/sqlite/migration"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func testQueue(t *testing.T) contract.Queue {
	t.Helper()
	driver, err := sqlite.NewDriver(&config.SQLiteConfig{
		Path:         filepath.Join(t.TempDir(), "queue.db"),
		MaxOpenConns: 2,
		MaxIdleConns: 2,
		BusyTimeout:  "2s",
		JournalMode:  "WAL",
	}, sqlitemigration.FS())
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	t.Cleanup(func() { driver.Close() })
	q, err := NewQueue(driver, &config.QueueConfig{
		ClaimBatchSize:     4,
		MaxAttempts:        2,
		BackoffBaseSeconds: 5,
	}, nil)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	return q
}

func job(t, payload string) contract.QueueJob {
	return contract.QueueJob{Type: t, Payload: []byte(payload), MaxAttempts: 2}
}

func TestPublishAndClaimRoundtrip(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	if err := q.Publish(ctx, job("character-census", `{"id":1}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	claimed, err := q.Claim(ctx, "character-census", 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}
	if claimed[0].Type != "character-census" {
		t.Errorf("type = %q", claimed[0].Type)
	}
	if string(claimed[0].Payload) != `{"id":1}` {
		t.Errorf("payload = %q", claimed[0].Payload)
	}
	if claimed[0].Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (incremented on claim)", claimed[0].Attempts)
	}
	if claimed[0].Status != contract.QueueJobClaimed {
		t.Errorf("status = %q, want claimed", claimed[0].Status)
	}
}

func TestPublishDeduplicatesByTypeAndHash(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	if err := q.Publish(ctx, job("id-sweep", `{"chunk":1}`), job("id-sweep", `{"chunk":1}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	depth, err := q.Depth(ctx)
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if depth[contract.QueueJobPending] != 1 {
		t.Errorf("pending = %d, want 1 (duplicate ignored)", depth[contract.QueueJobPending])
	}
	if err := q.Publish(ctx, job("id-sweep", `{"chunk":2}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	depth, _ = q.Depth(ctx)
	if depth[contract.QueueJobPending] != 2 {
		t.Errorf("pending = %d, want 2", depth[contract.QueueJobPending])
	}
}

func TestClaimSkipsFutureAndRespectsLimit(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	future := job("fc-census", `{"fc":"A"}`)
	future.RunAt = time.Now().Add(time.Hour)
	if err := q.Publish(ctx, future, job("fc-census", `{"fc":"B"}`), job("fc-census", `{"fc":"C"}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	claimed, err := q.Claim(ctx, "fc-census", 2)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed = %d, want 2 (limit, future job skipped)", len(claimed))
	}
	for _, c := range claimed {
		if string(c.Payload) == `{"fc":"A"}` {
			t.Error("future-dated job was claimed")
		}
	}
}

func TestCompleteMarksDoneAndPublishesNext(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	if err := q.Publish(ctx, job("character-census", `{"id":7}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	claimed, err := q.Claim(ctx, "character-census", 1)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	next := job("achievement-census", `{"id":7}`)
	if err := q.Complete(ctx, claimed[0].ID, next); err != nil {
		t.Fatalf("complete: %v", err)
	}
	depth, _ := q.Depth(ctx)
	if depth[contract.QueueJobDone] != 1 {
		t.Errorf("done = %d, want 1", depth[contract.QueueJobDone])
	}
	claimedNext, err := q.Claim(ctx, "achievement-census", 1)
	if err != nil {
		t.Fatalf("claim next: %v", err)
	}
	if len(claimedNext) != 1 {
		t.Errorf("downstream job not published by Complete")
	}
}

func TestRetryBackoffThenFail(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	if err := q.Publish(ctx, job("character-census", `{"id":3}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	c1, _ := q.Claim(ctx, "character-census", 1)
	if err := q.Retry(ctx, c1[0].ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	depth, _ := q.Depth(ctx)
	if depth[contract.QueueJobPending] != 1 {
		t.Fatalf("after retry: pending = %d, want 1", depth[contract.QueueJobPending])
	}
	if claimed, _ := q.Claim(ctx, "character-census", 1); len(claimed) != 0 {
		t.Fatal("job claimed before backoff elapsed")
	}
	inner := q.(*Queue)
	inner.now = func() time.Time { return time.Now().Add(5 * time.Second) }
	c2, err := q.Claim(ctx, "character-census", 1)
	if err != nil {
		t.Fatalf("claim after backoff: %v", err)
	}
	if len(c2) != 1 {
		t.Fatalf("claimed after backoff = %d, want 1", len(c2))
	}
	if err := q.Retry(ctx, c2[0].ID); err != nil {
		t.Fatalf("retry 2: %v", err)
	}
	depth, _ = q.Depth(ctx)
	if depth[contract.QueueJobFailed] != 1 {
		t.Errorf("failed = %d, want 1 (attempts >= max_attempts)", depth[contract.QueueJobFailed])
	}
}

func TestConcurrentClaimNoDoubleDelivery(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	for i := range 40 {
		if err := q.Publish(ctx, job("id-sweep", fmt.Sprintf(`{"chunk":%d}`, i))); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := q.Claim(ctx, "id-sweep", 10)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			mu.Lock()
			total += len(claimed)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 40 {
		t.Errorf("claimed total = %d, want 40 (no double delivery)", total)
	}
}
