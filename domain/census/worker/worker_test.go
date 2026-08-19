package worker

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	mockqueue "github.com/mihaiflorentin88/ffxiv-census/mock/queue"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type recordingHandler struct {
	mu    sync.Mutex
	calls int
	next  []contract.QueueJob
}

func (h *recordingHandler) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	return h.next, nil
}

type panicHandler struct{}

func (h *panicHandler) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	panic("unexpected nil pointer in handler")
}

func waitForCalls(t *testing.T, h *recordingHandler, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		n := h.calls
		h.mu.Unlock()
		if n >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	t.Fatalf("handler calls = %d, want %d (timeout)", h.calls, want)
}

func TestWorker_ProcessesClaimedJobs(t *testing.T) {
	q := mockqueue.NewFake()
	if _, err := q.Publish(
		context.Background(),
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":2,"to":2}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":3,"to":3}`)},
	); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	reg := handler.NewRegistry()
	rh := &recordingHandler{}
	reg.Register("id-sweep", rh)

	w := New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, "id-sweep", 2) }()

	waitForCalls(t, rh, 3)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("worker: %v", err)
	}
	if rh.calls != 3 {
		t.Errorf("handler calls = %d, want 3", rh.calls)
	}
	depth, _ := q.Depth(context.Background())
	if depth[contract.QueueJobDone] != 3 {
		t.Errorf("done jobs = %d, want 3", depth[contract.QueueJobDone])
	}
}

func TestWorker_PublishesChainedJobs(t *testing.T) {
	q := mockqueue.NewFake()
	if _, err := q.Publish(
		context.Background(),
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":1}`)},
	); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	reg := handler.NewRegistry()
	rh := &recordingHandler{
		next: []contract.QueueJob{{Type: "achievement-census", Payload: []byte(`{"character_id":1}`)}},
	}
	reg.Register("id-sweep", rh)

	w := New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, "id-sweep", 1) }()

	waitForCalls(t, rh, 1)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("worker: %v", err)
	}
	// The chained achievement-census job must now be pending.
	depth, _ := q.Depth(context.Background())
	if depth[contract.QueueJobPending] != 1 {
		t.Errorf("pending jobs = %d, want 1 (the chained job)", depth[contract.QueueJobPending])
	}
	if depth[contract.QueueJobDone] != 1 {
		t.Errorf("done jobs = %d, want 1 (the id-sweep job)", depth[contract.QueueJobDone])
	}
}

func TestWorker_UnknownEventErrors(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	w := New(q, reg, nil)
	if err := w.Run(context.Background(), "no-such-event", 1); err == nil {
		t.Fatal("expected error for unregistered event")
	}
}

func TestWorker_LogsJobLifecycle(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	q := mockqueue.NewFake()
	if _, err := q.Publish(
		context.Background(),
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":1}`)},
	); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	reg := handler.NewRegistry()
	rh := &recordingHandler{}
	reg.Register("id-sweep", rh)

	w := New(q, reg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, "id-sweep", 1) }()

	waitForCalls(t, rh, 1)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("worker: %v", err)
	}

	logs := buf.String()
	for _, want := range []string{"worker.job_start", "worker.job_done", "job_id"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %q:\n%s", want, logs)
		}
	}
}

func TestWorker_ReclaimsClaimedOnStart(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	rh := &recordingHandler{}
	reg.Register("id-sweep", rh)
	// Publish one job and claim it manually so it is 'claimed' (simulating a
	if _, err := q.Publish(context.Background(), contract.QueueJob{Type: "id-sweep", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := q.Claim(context.Background(), "id-sweep", 1, contract.ClaimModeAny); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	w := New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Run for a short window; the startup reclaim must return the claimed job
	// to pending, after which the loop claims and processes it.
	go func() { _ = w.Run(ctx, "id-sweep", 1) }()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rh.mu.Lock()
		calls := rh.calls
		rh.mu.Unlock()
		if calls > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	rh.mu.Lock()
	defer rh.mu.Unlock()
	if rh.calls == 0 {
		t.Fatal("claimed job was not reclaimed and processed after restart")
	}
}

func TestWorker_LogsQueueStatusWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	rh := &recordingHandler{}
	reg.Register("id-sweep", rh)

	w := New(q, reg, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, "id-sweep", 1) }()

	// Wait briefly for worker to start and check queue status
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("worker: %v", err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "worker.queue_status") {
		t.Errorf("logs missing worker.queue_status:\n%s", logs)
	}
	if !strings.Contains(logs, "pending_jobs=0") {
		t.Errorf("logs missing pending_jobs=0:\n%s", logs)
	}
}

func TestWorker_PanicIsolationAndErrorCapture(t *testing.T) {
	q := mockqueue.NewFake()
	h := handler.NewRegistry()
	h.Register("panic-job", &panicHandler{})
	w := New(q, h, nil)
	w.pollInterval = 10 * time.Millisecond

	_, err := q.Publish(context.Background(), contract.QueueJob{Type: "panic-job", Payload: []byte("{}")})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = w.Run(ctx, "panic-job", 1)

	// Worker should not crash, and job must be retried with panic trace
	jobs, err := q.ListJobs(context.Background(), contract.QueueJobFilter{Type: "panic-job"}, 10, 0)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListJobs: %v (len=%d)", err, len(jobs))
	}
	if jobs[0].LastError == nil || !strings.Contains(*jobs[0].LastError, "panic") {
		t.Fatalf("expected panic error captured on job, got %+v", jobs[0])
	}
}

func TestWorker_RetryGoroutine_ProcessesAllJobs(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	rh := &recordingHandler{}
	reg.Register("id-sweep", rh)

	// Publish 10 new jobs (unique payloads to avoid dedup).
	for i := range 10 {
		payload := fmt.Sprintf(`{"from":%d,"to":%d}`, i, i)
		if _, err := q.Publish(context.Background(), contract.QueueJob{Type: "id-sweep", Payload: []byte(payload)}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	// Publish 5 retry jobs with Attempts pre-set to 2.
	for i := range 5 {
		payload := fmt.Sprintf(`{"from":%d,"to":%d}`, 100+i, 100+i)
		if _, err := q.Publish(context.Background(), contract.QueueJob{Type: "id-sweep", Payload: []byte(payload), Attempts: 2}); err != nil {
			t.Fatalf("Publish retry: %v", err)
		}
	}

	w := New(q, reg, nil)
	w.pollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, "id-sweep", 2) }()

	waitForCalls(t, rh, 15)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("worker: %v", err)
	}

	if rh.calls != 15 {
		t.Errorf("handler calls = %d, want 15 (10 new + 5 retry)", rh.calls)
	}

	// Verify all jobs are done.
	depth, _ := q.Depth(context.Background())
	if depth[contract.QueueJobDone] != 15 {
		t.Errorf("done jobs = %d, want 15", depth[contract.QueueJobDone])
	}
}

func TestWorker_ConcurrencyClampedToMinimum(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	rh := &recordingHandler{}
	reg.Register("id-sweep", rh)

	w := New(q, reg, logger)
	w.pollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Request concurrency=1, but minimum for 1 event type is 2 (1 retry + 1 new).
	_ = w.Run(ctx, "id-sweep", 1)

	logs := buf.String()
	if !strings.Contains(logs, "worker.concurrency_clamped") {
		t.Errorf("logs missing concurrency_clamped warning:\n%s", logs)
	}
}

func TestWorker_RetryGoroutine_LogsRetriesOnlyMode(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	rh := &recordingHandler{}
	reg.Register("id-sweep", rh)

	w := New(q, reg, logger)
	w.pollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = w.Run(ctx, "id-sweep", 2)

	logs := buf.String()
	if !strings.Contains(logs, "retries_only") {
		t.Errorf("logs missing retries_only mode:\n%s", logs)
	}
	if !strings.Contains(logs, "new_only") {
		t.Errorf("logs missing new_only mode:\n%s", logs)
	}
}

func TestWorker_GracefulShutdown_StopsClaimingFinishesInFlight(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	q := mockqueue.NewFake()
	if _, err := q.Publish(
		context.Background(),
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":2}`)},
		contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":3}`)},
	); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	reg := handler.NewRegistry()
	// Slow handler: each job takes 100ms so workers are mid-flight when we cancel.
	slowHandler := &slowRecordingHandler{delay: 100 * time.Millisecond}
	reg.Register("id-sweep", slowHandler)

	w := New(q, reg, logger)
	w.pollInterval = 10 * time.Millisecond

	parentCtx, parentCancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- w.Run(parentCtx, "id-sweep", 2) }()

	// Wait for both workers to be busy processing (2 workers, 3 jobs, 100ms each).
	// After ~120ms at least 2 jobs are in-flight.
	time.Sleep(120 * time.Millisecond)

	// Signal shutdown: stopClaiming fires, but childCtx (processCtx) stays alive.
	parentCancel()

	// Wait for worker to drain and exit.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit within 5s")
	}

	// All in-flight jobs must have completed (handler ran to completion).
	slowHandler.mu.Lock()
	completed := slowHandler.calls
	slowHandler.mu.Unlock()

	if completed < 2 {
		t.Errorf("completed handler calls = %d, want at least 2 (in-flight jobs finished)", completed)
	}

	// Draining log must be present.
	logs := buf.String()
	if !strings.Contains(logs, "worker.draining") {
		t.Errorf("logs missing worker.draining:\n%s", logs)
	}

	// Verify completed jobs were marked done in the queue.
	depth, _ := q.Depth(context.Background())
	if depth[contract.QueueJobDone] < 2 {
		t.Errorf("done jobs = %d, want at least 2", depth[contract.QueueJobDone])
	}
}

// slowRecordingHandler sleeps for the configured delay on each call,
// used to simulate in-flight work during graceful shutdown tests.
type slowRecordingHandler struct {
	mu    sync.Mutex
	calls int
	delay time.Duration
}

func (h *slowRecordingHandler) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	time.Sleep(h.delay)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	return nil, nil
}
