package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// The Fake must mirror the SQLite adapter's dedup semantics: (type, sha256(payload))
// is the dedup key and the hash is derived server-side, never supplied by callers.

func TestFakePublishDeduplicatesByTypeAndPayload(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	job := func(payload string) contract.QueueJob {
		return contract.QueueJob{Type: "id-sweep", Payload: []byte(payload)}
	}
	if n, err := f.Publish(ctx, job(`{"chunk":1}`), job(`{"chunk":1}`)); err != nil || n != 1 {
		t.Fatalf("publish: n=%d err=%v, want n=1", n, err)
	}
	depth, err := f.Depth(ctx)
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if depth[contract.QueueJobPending] != 1 {
		t.Errorf("pending = %d, want 1 (duplicate ignored)", depth[contract.QueueJobPending])
	}
	if n, err := f.Publish(ctx, job(`{"chunk":2}`)); err != nil || n != 1 {
		t.Fatalf("publish: n=%d err=%v, want n=1", n, err)
	}
	depth, _ = f.Depth(ctx)
	if depth[contract.QueueJobPending] != 2 {
		t.Errorf("pending = %d, want 2 (different payload of same type must not dedup)", depth[contract.QueueJobPending])
	}
}

func TestFakePublishComputesPayloadHash(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	if _, err := f.Publish(ctx, contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	claimed, err := f.Claim(ctx, "id-sweep", 1)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}
	sum := sha256.Sum256([]byte(`{"chunk":1}`))
	want := hex.EncodeToString(sum[:])
	if claimed[0].PayloadHash != want {
		t.Errorf("payload_hash = %q, want %q", claimed[0].PayloadHash, want)
	}
}

// The Fake must mirror the SQLite adapter's MaxAttempts default (5): a job
// published without MaxAttempts must survive its first Retry back to pending,
// not be marked failed (Attempts(1) >= MaxAttempts(0)).
func TestFakePublishDefaultsMaxAttempts(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	if _, err := f.Publish(ctx, contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	claimed, err := f.Claim(ctx, "id-sweep", 1)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}
	if claimed[0].MaxAttempts != 5 {
		t.Errorf("max_attempts = %d, want 5", claimed[0].MaxAttempts)
	}
	if err := f.Retry(ctx, claimed[0].ID, "test error"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	depth, err := f.Depth(ctx)
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if depth[contract.QueueJobPending] != 1 {
		t.Errorf("pending = %d, want 1 (first retry returns to pending)", depth[contract.QueueJobPending])
	}
	if depth[contract.QueueJobFailed] != 0 {
		t.Errorf("failed = %d, want 0", depth[contract.QueueJobFailed])
	}
}

func TestFake_ReclaimClaimed(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	if _, err := f.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"a":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"a":2}`)},
		contract.QueueJob{Type: "other", Payload: []byte(`{"a":3}`)},
	); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	claimed, err := f.Claim(ctx, "id-sweep", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim: n=%d err=%v", len(claimed), err)
	}
	n, err := f.ReclaimClaimed(ctx, "id-sweep")
	if err != nil || n != 1 {
		t.Fatalf("ReclaimClaimed: n=%d err=%v", n, err)
	}
	got, err := f.Claim(ctx, "id-sweep", 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("Claim after reclaim: n=%d err=%v", len(got), err)
	}
	other, err := f.Claim(ctx, "other", 1)
	if err != nil || len(other) != 1 {
		t.Fatalf("Claim other: n=%d err=%v", len(other), err)
	}
}

func TestFake_ListJobs_FilterAndPagination(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	_, _ = f.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":2}`)},
		contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":10}`)},
		contract.QueueJob{Type: "achievement-census", Payload: []byte(`{"id":10}`)},
	)

	// Claim and complete one character-census job
	claimed, err := f.Claim(ctx, "character-census", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim character-census: %v (len=%d)", err, len(claimed))
	}
	if err := f.Complete(ctx, claimed[0].ID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Claim and fail one achievement-census job
	claimedAch, err := f.Claim(ctx, "achievement-census", 1)
	if err != nil || len(claimedAch) != 1 {
		t.Fatalf("claim achievement-census: %v (len=%d)", err, len(claimedAch))
	}
	if err := f.Fail(ctx, claimedAch[0].ID, "test fail"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	// Total count without filter
	total, err := f.CountJobs(ctx, contract.QueueJobFilter{})
	if err != nil {
		t.Fatalf("CountJobs: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}

	// Filter by Type
	idSweepJobs, err := f.ListJobs(ctx, contract.QueueJobFilter{Type: "id-sweep"}, 10, 0)
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
	doneJobs, err := f.ListJobs(ctx, contract.QueueJobFilter{Status: contract.QueueJobDone}, 10, 0)
	if err != nil {
		t.Fatalf("ListJobs status done: %v", err)
	}
	if len(doneJobs) != 1 || doneJobs[0].Type != "character-census" {
		t.Errorf("done jobs = %v, want 1 character-census job", doneJobs)
	}

	// Pagination
	page1, err := f.ListJobs(ctx, contract.QueueJobFilter{}, 2, 0)
	if err != nil {
		t.Fatalf("ListJobs page 1: %v", err)
	}
	page2, err := f.ListJobs(ctx, contract.QueueJobFilter{}, 2, 2)
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

func TestFake_GetJob(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	_, _ = f.Publish(ctx, contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":5}`)})
	jobs, err := f.ListJobs(ctx, contract.QueueJobFilter{Type: "id-sweep"}, 1, 0)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListJobs: %v (len=%d)", err, len(jobs))
	}

	job, err := f.GetJob(ctx, jobs[0].ID)
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

	nonExistent, err := f.GetJob(ctx, 999999)
	if err != nil {
		t.Fatalf("GetJob non-existent: %v", err)
	}
	if nonExistent != nil {
		t.Errorf("expected nil for non-existent job, got %+v", nonExistent)
	}
}

func TestFake_StatsByType(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	_, _ = f.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":2}`)},
		contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":10}`)},
		contract.QueueJob{Type: "achievement-census", Payload: []byte(`{"id":10}`)},
	)

	// Complete character-census
	claimed, _ := f.Claim(ctx, "character-census", 1)
	_ = f.Complete(ctx, claimed[0].ID)

	// Fail achievement-census
	claimedAch, _ := f.Claim(ctx, "achievement-census", 1)
	_ = f.Fail(ctx, claimedAch[0].ID, "failed error")
	stats, err := f.StatsByType(ctx)
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

func TestFake_ReliabilityAndEventDetails(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	_, _ = f.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":2}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":3}`)},
	)

	// Claim one job and retry with error
	claimed, err := f.Claim(ctx, "id-sweep", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim: %v", err)
	}
	if err := f.Retry(ctx, claimed[0].ID, "lodestone 429"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	retried, _ := f.GetJob(ctx, claimed[0].ID)
	if retried == nil || retried.LastError == nil || *retried.LastError != "lodestone 429" {
		t.Fatalf("expected last error 'lodestone 429', got %+v", retried)
	}

	// Claim second job and fail
	claimed2, err := f.Claim(ctx, "id-sweep", 1)
	if err != nil || len(claimed2) != 1 {
		t.Fatalf("Claim 2: %v", err)
	}
	if err := f.Fail(ctx, claimed2[0].ID, "permanent error"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	failed, _ := f.GetJob(ctx, claimed2[0].ID)
	if failed == nil || failed.Status != contract.QueueJobFailed || failed.FailedAt == nil {
		t.Fatalf("expected failed job with failed_at, got %+v", failed)
	}

	// Check GetEventDetails
	details, err := f.GetEventDetails(ctx, 5)
	if err != nil {
		t.Fatalf("GetEventDetails: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("details length = %d, want 1", len(details))
	}
	if details[0].Pending != 2 || details[0].Failed != 1 {
		t.Errorf("expected pending=2, failed=1; got pending=%d, failed=%d", details[0].Pending, details[0].Failed)
	}
	if len(details[0].FailedJobs) != 1 || details[0].FailedJobs[0].ID != claimed2[0].ID {
		t.Errorf("expected 1 failed job in details: %+v", details[0].FailedJobs)
	}

	// Test RetryFailed
	n, err := f.RetryFailed(ctx, "id-sweep", 10)
	if err != nil || n != 1 {
		t.Fatalf("RetryFailed: n=%d err=%v", n, err)
	}
	replayed, _ := f.GetJob(ctx, claimed2[0].ID)
	if replayed == nil || replayed.Status != contract.QueueJobPending || replayed.FailedAt != nil {
		t.Fatalf("expected replayed job to be pending with nil failed_at: %+v", replayed)
	}

	// Test Complete sets CompletedAt
	claimed3, _ := f.Claim(ctx, "id-sweep", 1)
	if len(claimed3) == 1 {
		_ = f.Complete(ctx, claimed3[0].ID)
		completed, _ := f.GetJob(ctx, claimed3[0].ID)
		if completed == nil || completed.Status != contract.QueueJobDone || completed.CompletedAt == nil {
			t.Fatalf("expected completed job with completed_at: %+v", completed)
		}
	}

	// Test PurgeJobs
	purged, err := f.PurgeJobs(ctx, "id-sweep", contract.QueueJobDone, 0)
	if err != nil || purged != 1 {
		t.Fatalf("PurgeJobs: n=%d err=%v", purged, err)
	}
}

func TestFake_CreatedAtAndRunAtPopulatedOnPublish(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	_, err := f.Publish(ctx, contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"a":1}`)})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	jobs, err := f.ListJobs(ctx, contract.QueueJobFilter{Type: "id-sweep"}, 1, 0)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListJobs: %v (len=%d)", err, len(jobs))
	}
	if jobs[0].CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be non-zero on Publish")
	}
	if jobs[0].RunAt.IsZero() {
		t.Error("expected RunAt to be non-zero on Publish")
	}

	// PurgeJobs with olderThan 1h should NOT purge newly created pending jobs
	purged, err := f.PurgeJobs(ctx, "id-sweep", contract.QueueJobPending, time.Hour)
	if err != nil || purged != 0 {
		t.Errorf("PurgeJobs purged %d fresh jobs, want 0", purged)
	}
}

func TestFake_ClaimDeterministicOrderAndClaimedAt(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	now := time.Now().UTC()
	_, _ = f.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"id":2}`), RunAt: now.Add(2 * time.Second)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"id":1}`), RunAt: now.Add(1 * time.Second)},
	)

	// Wait for both to be eligible
	time.Sleep(2100 * time.Millisecond)
	// Claim 1 job - should pick earlier RunAt (payload {"id":1})
	claimed, err := f.Claim(ctx, "id-sweep", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim: %v (len=%d)", err, len(claimed))
	}
	if claimed[0].ClaimedAt == nil {
		t.Error("expected ClaimedAt to be set on Claim")
	}
	if string(claimed[0].Payload) != `{"id":1}` {
		t.Errorf("expected job with earlier RunAt to be claimed first, got: %s", string(claimed[0].Payload))
	}
}

func TestFake_RetryAppliesBackoff(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	_, _ = f.Publish(ctx, contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"id":1}`), MaxAttempts: 3})
	claimed, _ := f.Claim(ctx, "id-sweep", 1)
	_ = f.Retry(ctx, claimed[0].ID, "transient error")

	retried, _ := f.GetJob(ctx, claimed[0].ID)
	if retried.Status != contract.QueueJobPending {
		t.Fatalf("expected pending, got %s", retried.Status)
	}
	if !retried.RunAt.After(time.Now().UTC()) {
		t.Errorf("expected RunAt to be pushed into the future with backoff: %v", retried.RunAt)
	}
}

func TestFake_PurgeByEachStatus(t *testing.T) {
	statuses := []contract.QueueJobStatus{
		contract.QueueJobPending,
		contract.QueueJobClaimed,
		contract.QueueJobDone,
		contract.QueueJobFailed,
	}

	for _, targetStatus := range statuses {
		t.Run(string(targetStatus), func(t *testing.T) {
			f := NewFake()
			ctx := context.Background()

			_, _ = f.Publish(ctx,
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":100}`)},
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":101,"to":200}`)},
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":201,"to":300}`)},
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":301,"to":400}`)},
			)

			claimed, _ := f.Claim(ctx, "id-sweep", 3)
			_ = f.Complete(ctx, claimed[1].ID)
			_ = f.Fail(ctx, claimed[2].ID, "err")

			purged, err := f.PurgeJobs(ctx, "id-sweep", targetStatus, 0)
			if err != nil {
				t.Fatalf("PurgeJobs failed: %v", err)
			}
			if purged != 1 {
				t.Errorf("expected 1 job purged for %s, got %d", targetStatus, purged)
			}
			depthAfter, _ := f.Depth(ctx)
			if depthAfter[targetStatus] != 0 {
				t.Errorf("expected 0 %s jobs after purge, got %d", targetStatus, depthAfter[targetStatus])
			}
		})
	}
}
