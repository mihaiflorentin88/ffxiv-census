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
	if n, err := q.Publish(ctx, job("character-census", `{"id":1}`)); err != nil || n != 1 {
		t.Fatalf("publish: n=%d err=%v, want n=1", n, err)
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
	if n, err := q.Publish(ctx, job("id-sweep", `{"chunk":1}`), job("id-sweep", `{"chunk":1}`)); err != nil || n != 1 {
		t.Fatalf("publish: n=%d err=%v, want n=1", n, err)
	}
	depth, err := q.Depth(ctx)
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if depth[contract.QueueJobPending] != 1 {
		t.Errorf("pending = %d, want 1 (duplicate ignored)", depth[contract.QueueJobPending])
	}
	if n, err := q.Publish(ctx, job("id-sweep", `{"chunk":2}`)); err != nil || n != 1 {
		t.Fatalf("publish: n=%d err=%v, want n=1", n, err)
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
	if n, err := q.Publish(ctx, future, job("fc-census", `{"fc":"B"}`), job("fc-census", `{"fc":"C"}`)); err != nil || n != 3 {
		t.Fatalf("publish: n=%d err=%v, want n=3", n, err)
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
	if _, err := q.Publish(ctx, job("character-census", `{"id":7}`)); err != nil {
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
	if _, err := q.Publish(ctx, job("character-census", `{"id":3}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	c1, _ := q.Claim(ctx, "character-census", 1)
	if err := q.Retry(ctx, c1[0].ID, "temporary error"); err != nil {
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
	if err := q.Retry(ctx, c2[0].ID, "second error"); err != nil {
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
		if _, err := q.Publish(ctx, job("id-sweep", fmt.Sprintf(`{"chunk":%d}`, i))); err != nil {
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

func TestQueue_ReclaimClaimed(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	// Publish two jobs of the same type and one of a different type.
	if _, err := q.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"a":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"a":2}`)},
		contract.QueueJob{Type: "other", Payload: []byte(`{"a":3}`)},
	); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Claim one id-sweep job -> now 'claimed'.
	claimed, err := q.Claim(ctx, "id-sweep", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim: n=%d err=%v", len(claimed), err)
	}
	// Reclaim: exactly the one claimed id-sweep job returns to pending.
	n, err := q.ReclaimClaimed(ctx, "id-sweep")
	if err != nil || n != 1 {
		t.Fatalf("ReclaimClaimed: n=%d err=%v", n, err)
	}
	// Both id-sweep jobs are now claimable again; the 'other' job is untouched.
	got, err := q.Claim(ctx, "id-sweep", 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("Claim after reclaim: n=%d err=%v", len(got), err)
	}
	other, err := q.Claim(ctx, "other", 1)
	if err != nil || len(other) != 1 {
		t.Fatalf("Claim other: n=%d err=%v", len(other), err)
	}
}

func TestQueue_ListJobs_FilterAndPagination(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	_, _ = q.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":2}`)},
		contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":10}`)},
		contract.QueueJob{Type: "achievement-census", Payload: []byte(`{"id":10}`)},
	)

	// Claim and complete one character-census job
	claimed, err := q.Claim(ctx, "character-census", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim character-census: %v (len=%d)", err, len(claimed))
	}
	if err := q.Complete(ctx, claimed[0].ID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Claim and fail one achievement-census job
	claimedAch, err := q.Claim(ctx, "achievement-census", 1)
	if err != nil || len(claimedAch) != 1 {
		t.Fatalf("claim achievement-census: %v (len=%d)", err, len(claimedAch))
	}
	if err := q.Fail(ctx, claimedAch[0].ID, "failed permanently"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	// Total count without filter
	total, err := q.CountJobs(ctx, contract.QueueJobFilter{})
	if err != nil {
		t.Fatalf("CountJobs: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}

	// Filter by Type
	idSweepJobs, err := q.ListJobs(ctx, contract.QueueJobFilter{Type: "id-sweep"}, 10, 0)
	if err != nil {
		t.Fatalf("ListJobs type id-sweep: %v", err)
	}
	if len(idSweepJobs) != 2 {
		t.Errorf("id-sweep jobs count = %d, want 2", len(idSweepJobs))
	}
	// ID desc order check
	if len(idSweepJobs) == 2 && idSweepJobs[0].ID <= idSweepJobs[1].ID {
		t.Errorf("expected ID descending order: %d <= %d", idSweepJobs[0].ID, idSweepJobs[1].ID)
	}

	// Filter by Status
	doneJobs, err := q.ListJobs(ctx, contract.QueueJobFilter{Status: contract.QueueJobDone}, 10, 0)
	if err != nil {
		t.Fatalf("ListJobs status done: %v", err)
	}
	if len(doneJobs) != 1 || doneJobs[0].Type != "character-census" {
		t.Errorf("done jobs = %v, want 1 character-census job", doneJobs)
	}

	// Pagination
	page1, err := q.ListJobs(ctx, contract.QueueJobFilter{}, 2, 0)
	if err != nil {
		t.Fatalf("ListJobs page 1: %v", err)
	}
	page2, err := q.ListJobs(ctx, contract.QueueJobFilter{}, 2, 2)
	if err != nil {
		t.Fatalf("ListJobs page 2: %v", err)
	}
	if len(page1) != 2 || len(page2) != 2 {
		t.Fatalf("page sizes = %d, %d, want 2, 2", len(page1), len(page2))
	}
	if page1[1].ID <= page2[0].ID {
		t.Errorf("page1 lowest ID (%d) should be > page2 highest ID (%d)", page1[1].ID, page2[0].ID)
	}
}

func TestQueue_GetJob(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	_, _ = q.Publish(ctx, contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":5}`)})
	jobs, err := q.ListJobs(ctx, contract.QueueJobFilter{Type: "id-sweep"}, 1, 0)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListJobs: %v (len=%d)", err, len(jobs))
	}

	job, err := q.GetJob(ctx, jobs[0].ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job == nil {
		t.Fatal("expected job to be found, got nil")
	}
	if job.ID != jobs[0].ID || job.Type != "id-sweep" || string(job.Payload) != `{"from":1,"to":5}` {
		t.Errorf("unexpected job: %+v", job)
	}
	if job.PayloadHash == "" {
		t.Error("expected non-empty PayloadHash")
	}

	nonExistent, err := q.GetJob(ctx, 999999)
	if err != nil {
		t.Fatalf("GetJob non-existent: %v", err)
	}
	if nonExistent != nil {
		t.Errorf("expected nil for non-existent job, got %+v", nonExistent)
	}
}

func TestQueue_StatsByType(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	_, _ = q.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":2}`)},
		contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":10}`)},
		contract.QueueJob{Type: "achievement-census", Payload: []byte(`{"id":10}`)},
	)

	// Complete character-census
	claimed, _ := q.Claim(ctx, "character-census", 1)
	_ = q.Complete(ctx, claimed[0].ID)

	// Fail achievement-census
	claimedAch, _ := q.Claim(ctx, "achievement-census", 1)
	_ = q.Fail(ctx, claimedAch[0].ID, "failed")

	stats, err := q.StatsByType(ctx)
	if err != nil {
		t.Fatalf("StatsByType: %v", err)
	}
	if len(stats) != 3 {
		t.Fatalf("stats count = %d, want 3", len(stats))
	}

	// Check alphabetical ordering
	if stats[0].Type != "achievement-census" || stats[1].Type != "character-census" || stats[2].Type != "id-sweep" {
		t.Errorf("unexpected stats order: %+v", stats)
	}

	// Verify id-sweep: 2 pending, total 2
	if stats[2].Pending != 2 || stats[2].Total != 2 {
		t.Errorf("id-sweep stats = %+v, want pending=2, total=2", stats[2])
	}
	// Verify character-census: 1 done, total 1
	if stats[1].Done != 1 || stats[1].Total != 1 {
		t.Errorf("character-census stats = %+v, want done=1, total=1", stats[1])
	}
	// Verify achievement-census: 1 failed, total 1
	if stats[0].Failed != 1 || stats[0].Total != 1 {
		t.Errorf("achievement-census stats = %+v, want failed=1, total=1", stats[0])
	}
}

func TestQueue_LastErrorAndEventDetails(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	job := contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":100}`)}
	_, err := q.Publish(ctx, job)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	claimed, err := q.Claim(ctx, "id-sweep", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim: %v", err)
	}

	// Retry with error string
	err = q.Retry(ctx, claimed[0].ID, "lodestone 429 rate limit")
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}

	retried, err := q.GetJob(ctx, claimed[0].ID)
	if err != nil || retried.LastError == nil || *retried.LastError != "lodestone 429 rate limit" {
		t.Fatalf("expected last error stored, got %+v", retried)
	}

	// Event details inspection
	details, err := q.GetEventDetails(ctx, 5)
	if err != nil {
		t.Fatalf("GetEventDetails: %v", err)
	}
	if len(details) == 0 || details[0].Pending != 1 || len(details[0].NextJobs) != 1 {
		t.Fatalf("unexpected details: %+v", details)
	}
	if details[0].NextJobs[0].LastError == nil || *details[0].NextJobs[0].LastError != "lodestone 429 rate limit" {
		t.Fatalf("expected NextJobs to have last_error: %+v", details[0].NextJobs[0])
	}
}

func TestQueue_RetryFailedAndPurgeJobs(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	_, err := q.Publish(ctx,
		contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":1}`)},
		contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":2}`)},
	)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	claimed, err := q.Claim(ctx, "character-census", 2)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("Claim: %v", err)
	}

	// Complete job 1
	if err := q.Complete(ctx, claimed[0].ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	doneJob, _ := q.GetJob(ctx, claimed[0].ID)
	if doneJob.CompletedAt == nil {
		t.Fatalf("expected CompletedAt to be set on complete")
	}

	// Fail job 2
	if err := q.Fail(ctx, claimed[1].ID, "lodestone 404 not found"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	failedJob, _ := q.GetJob(ctx, claimed[1].ID)
	if failedJob.FailedAt == nil || failedJob.LastError == nil || *failedJob.LastError != "lodestone 404 not found" {
		t.Fatalf("expected FailedAt and LastError to be set on fail, got %+v", failedJob)
	}

	// Replay failed jobs with RetryFailed
	retriedCount, err := q.RetryFailed(ctx, "character-census", 10)
	if err != nil || retriedCount != 1 {
		t.Fatalf("RetryFailed: count=%d err=%v", retriedCount, err)
	}
	replayedJob, _ := q.GetJob(ctx, claimed[1].ID)
	if replayedJob.Status != contract.QueueJobPending || replayedJob.FailedAt != nil || replayedJob.Attempts != 0 {
		t.Fatalf("expected replayed job to be pending with attempts=0 and nil FailedAt, got %+v", replayedJob)
	}

	// Purge done jobs older than 0
	purgedDone, err := q.PurgeJobs(ctx, contract.QueueJobDone, 0)
	if err != nil || purgedDone != 1 {
		t.Fatalf("PurgeJobs done: count=%d err=%v", purgedDone, err)
	}
	purgedCheck, _ := q.GetJob(ctx, claimed[0].ID)
	if purgedCheck != nil {
		t.Fatalf("expected purged job to be deleted")
	}
}
