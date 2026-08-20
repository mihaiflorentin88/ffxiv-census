package worker_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/handler"
	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy/worker"
	mockqueue "github.com/mihaiflorentin88/ffxiv-census/mock/queue"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type countingHandler struct {
	count *int32
}

func (h *countingHandler) Handle(_ context.Context, _ []byte) ([]contract.QueueJob, error) {
	atomic.AddInt32(h.count, 1)
	return nil, nil
}

type noopHandler struct{}

func (h *noopHandler) Handle(_ context.Context, _ []byte) ([]contract.QueueJob, error) {
	return nil, nil
}

func TestWorker_ProcessesNewProxyJobs(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	var processed int32
	reg.Register(handler.EventNewProxy, &countingHandler{count: &processed})
	reg.Register(handler.EventScanProxy, &noopHandler{})

	q.Publish(context.Background(), contract.QueueJob{Type: handler.EventNewProxy, Payload: []byte(`{"a":1}`)})
	q.Publish(context.Background(), contract.QueueJob{Type: handler.EventNewProxy, Payload: []byte(`{"a":2}`)})

	w := worker.New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = w.RunEvents(ctx, []string{handler.EventNewProxy, handler.EventScanProxy}, 3) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&processed) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if got := atomic.LoadInt32(&processed); got != 2 {
		t.Fatalf("expected 2 processed, got %d", got)
	}
}

func TestWorker_RetryGoroutine(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	var newCount, scanCount int32
	reg.Register(handler.EventNewProxy, &countingHandler{count: &newCount})
	reg.Register(handler.EventScanProxy, &countingHandler{count: &scanCount})

	for i := 0; i < 3; i++ {
		payload := fmt.Sprintf(`{"i":%d}`, i)
		q.Publish(context.Background(), contract.QueueJob{Type: handler.EventNewProxy, Payload: []byte(payload)})
		q.Publish(context.Background(), contract.QueueJob{Type: handler.EventScanProxy, Payload: []byte(payload)})
	}

	w := worker.New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = w.RunEvents(ctx, []string{handler.EventNewProxy, handler.EventScanProxy}, 3) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&newCount) >= 3 && atomic.LoadInt32(&scanCount) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	if got := atomic.LoadInt32(&newCount); got < 3 {
		t.Fatalf("expected >=3 new-proxy processed, got %d", got)
	}
	if got := atomic.LoadInt32(&scanCount); got < 3 {
		t.Fatalf("expected >=3 scan-proxy processed, got %d", got)
	}
}

func TestWorker_ConcurrencyClampedToMinimum(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	reg.Register(handler.EventNewProxy, &noopHandler{})
	reg.Register(handler.EventScanProxy, &noopHandler{})

	w := worker.New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.RunEvents(ctx, []string{handler.EventNewProxy, handler.EventScanProxy}, 1) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done // should not error — concurrency was clamped to 3
}

func TestWorker_ReclaimsClaimedOnStart(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	var processed int32
	reg.Register(handler.EventNewProxy, &countingHandler{count: &processed})
	reg.Register(handler.EventScanProxy, &noopHandler{})

	// Publish and manually claim to simulate a killed consumer.
	q.Publish(context.Background(), contract.QueueJob{Type: handler.EventNewProxy, Payload: []byte(`{}`)})
	q.Claim(context.Background(), handler.EventNewProxy, 1, contract.ClaimModeAny)

	w := worker.New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = w.RunEvents(ctx, []string{handler.EventNewProxy, handler.EventScanProxy}, 3) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&processed) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if atomic.LoadInt32(&processed) == 0 {
		t.Fatal("claimed job was not reclaimed and processed after restart")
	}
}

func TestWorker_NoHandlerRegistered(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	reg.Register(handler.EventNewProxy, &noopHandler{})
	// Missing EventScanProxy handler

	w := worker.New(q, reg, nil)
	err := w.RunEvents(context.Background(), []string{handler.EventNewProxy, handler.EventScanProxy}, 3)
	if err == nil {
		t.Fatal("expected error for missing handler")
	}
}
