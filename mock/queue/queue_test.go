package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

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
	if err := f.Publish(ctx, job(`{"chunk":1}`), job(`{"chunk":1}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	depth, err := f.Depth(ctx)
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if depth[contract.QueueJobPending] != 1 {
		t.Errorf("pending = %d, want 1 (duplicate ignored)", depth[contract.QueueJobPending])
	}
	if err := f.Publish(ctx, job(`{"chunk":2}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	depth, _ = f.Depth(ctx)
	if depth[contract.QueueJobPending] != 2 {
		t.Errorf("pending = %d, want 2 (different payload of same type must not dedup)", depth[contract.QueueJobPending])
	}
}

func TestFakePublishComputesPayloadHash(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	if err := f.Publish(ctx, contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)}); err != nil {
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
	if err := f.Publish(ctx, contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"chunk":1}`)}); err != nil {
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
	if err := f.Retry(ctx, claimed[0].ID); err != nil {
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
