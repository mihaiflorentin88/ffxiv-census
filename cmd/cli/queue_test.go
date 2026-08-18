package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func setupTestQueue(t *testing.T) contract.Queue {
	t.Helper()
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "queue_cli.db"))
	container.Load = container.NewServiceContainer()
	q := container.Load.Queue()
	if q == nil {
		t.Fatal("expected non-nil queue from container")
	}
	t.Cleanup(func() {
		_ = container.Load.SQLite().Close()
	})
	return q
}

func TestQueueCmd_FlagsRegistered(t *testing.T) {
	if queueCmd.Use != "queue" {
		t.Errorf("queueCmd.Use = %s, want queue", queueCmd.Use)
	}

	// stats flags
	if statsCmd.Flags().Lookup("event-type") == nil {
		t.Error("expected --event-type flag on queue stats")
	}
	if statsCmd.Flags().Lookup("sample-limit") == nil {
		t.Error("expected --sample-limit flag on queue stats")
	}

	// retry-failed flags
	if retryFailedCmd.Flags().Lookup("event-type") == nil {
		t.Error("expected --event-type flag on queue retry-failed")
	}
	if retryFailedCmd.Flags().Lookup("limit") == nil {
		t.Error("expected --limit flag on queue retry-failed")
	}

	// purge flags
	if purgeCmd.Flags().Lookup("event-type") == nil {
		t.Error("expected --event-type flag on queue purge")
	}
	if purgeCmd.Flags().Lookup("status") == nil {
		t.Error("expected --status flag on queue purge")
	}
	if purgeCmd.Flags().Lookup("older-than") == nil {
		t.Error("expected --older-than flag on queue purge")
	}
	if purgeCmd.Flags().Lookup("all") == nil {
		t.Error("expected --all flag on queue purge")
	}
}

func TestQueueStatsCmd_Run(t *testing.T) {
	q := setupTestQueue(t)
	ctx := context.Background()

	_, err := q.Publish(ctx,
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":100}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":101,"to":200}`)},
	)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	claimed, err := q.Claim(ctx, "id-sweep", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v", err)
	}
	if err := q.Fail(ctx, claimed[0].ID, "lodestone 429 rate limit"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"queue", "stats"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("queue stats execute: %v", err)
	}

	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("id-sweep")) {
		t.Errorf("expected output to contain 'id-sweep', got:\n%s", out)
	}
	if !bytes.Contains(buf.Bytes(), []byte("lodestone 429 rate limit")) {
		t.Errorf("expected output to contain error message, got:\n%s", out)
	}
}

func TestQueueRetryFailedCmd_Run(t *testing.T) {
	q := setupTestQueue(t)
	ctx := context.Background()

	_, err := q.Publish(ctx, contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":1}`)})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	claimed, err := q.Claim(ctx, "character-census", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v", err)
	}
	if err := q.Fail(ctx, claimed[0].ID, "not found"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"queue", "retry-failed", "--event-type", "character-census"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("queue retry-failed execute: %v", err)
	}

	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("Replayed 1 failed jobs")) {
		t.Errorf("unexpected output: %s", out)
	}

	depth, _ := q.Depth(ctx)
	if depth[contract.QueueJobPending] != 1 || depth[contract.QueueJobFailed] != 0 {
		t.Errorf("unexpected depth after retry-failed: %+v", depth)
	}
}

func TestQueuePurgeCmd_Run(t *testing.T) {
	q := setupTestQueue(t)
	ctx := context.Background()

	_, err := q.Publish(ctx, contract.QueueJob{Type: "character-census", Payload: []byte(`{"id":1}`)})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	claimed, err := q.Claim(ctx, "character-census", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v", err)
	}
	if err := q.Complete(ctx, claimed[0].ID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"queue", "purge", "--status", "done", "--older-than", "0"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("queue purge execute with --older-than 0: %v", err)
	}

	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("Purged 1 jobs (event: all, status: done)")) {
		t.Errorf("unexpected output: %s", out)
	}

	// Test --all flag on pending jobs
	_, err = q.Publish(ctx, contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":100}`)})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	buf.Reset()
	rootCmd.SetArgs([]string{"queue", "purge", "--status", "pending", "--all"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("queue purge execute with --all: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("Purged 1 jobs (event: all, status: pending)")) {
		t.Errorf("unexpected output: %s", buf.String())
	}
}

func TestQueuePurgeCmd_ByStatus(t *testing.T) {
	statuses := []struct {
		status string
		flag   string
	}{
		{"pending", "pending"},
		{"claimed", "claimed"},
		{"done", "done"},
		{"failed", "failed"},
	}

	for _, tc := range statuses {
		t.Run(tc.status, func(t *testing.T) {
			q := setupTestQueue(t)
			ctx := context.Background()

			_, err := q.Publish(ctx,
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":100}`)},
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":101,"to":200}`)},
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":201,"to":300}`)},
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":301,"to":400}`)},
			)
			if err != nil {
				t.Fatalf("publish: %v", err)
			}

			claimed, _ := q.Claim(ctx, "id-sweep", 3)
			_ = q.Complete(ctx, claimed[1].ID)
			_ = q.Fail(ctx, claimed[2].ID, "test err")

			var buf bytes.Buffer
			rootCmd.SetOut(&buf)
			rootCmd.SetErr(&buf)
			rootCmd.SetArgs([]string{"queue", "purge", "--status", tc.flag, "--all"})

			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("queue purge execute: %v", err)
			}

			expectedMsg := fmt.Sprintf("Purged 1 jobs (event: all, status: %s)", tc.status)
			if !bytes.Contains(buf.Bytes(), []byte(expectedMsg)) {
				t.Errorf("expected %q, got: %s", expectedMsg, buf.String())
			}
		})
	}
}
