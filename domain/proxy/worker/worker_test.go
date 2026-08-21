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

// testEventSecondary is a test-local event type used to verify multi-event
// dispatch without depending on the removed EventScanProxy production constant.
const testEventSecondary = "test-secondary"

func TestWorker_ProcessesNewProxyJobs(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	var processed int32
	reg.Register(handler.EventNewProxy, &countingHandler{count: &processed})
	reg.Register(testEventSecondary, &noopHandler{})

	q.Publish(context.Background(), contract.QueueJob{Type: handler.EventNewProxy, Payload: []byte(`{"a":1}`)})
	q.Publish(context.Background(), contract.QueueJob{Type: handler.EventNewProxy, Payload: []byte(`{"a":2}`)})

	w := worker.New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = w.RunEvents(ctx, []string{handler.EventNewProxy, testEventSecondary}, 3) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&processed) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if got := atomic.LoadInt32(&processed); got != 2 {
		t.Fatalf("expected 2 processed, got %d", got)
	}
}

func TestWorker_ProcessesMultipleEventTypes(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	var newCount, secondaryCount int32
	reg.Register(handler.EventNewProxy, &countingHandler{count: &newCount})
	reg.Register(testEventSecondary, &countingHandler{count: &secondaryCount})

	for i := 0; i < 3; i++ {
		payload := fmt.Sprintf(`{"i":%d}`, i)
		q.Publish(context.Background(), contract.QueueJob{Type: handler.EventNewProxy, Payload: []byte(payload)})
		q.Publish(context.Background(), contract.QueueJob{Type: testEventSecondary, Payload: []byte(payload)})
	}

	w := worker.New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = w.RunEvents(ctx, []string{handler.EventNewProxy, testEventSecondary}, 3) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&newCount) >= 3 && atomic.LoadInt32(&secondaryCount) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	if got := atomic.LoadInt32(&newCount); got < 3 {
		t.Fatalf("expected >=3 new-proxy processed, got %d", got)
	}
	if got := atomic.LoadInt32(&secondaryCount); got < 3 {
		t.Fatalf("expected >=3 secondary processed, got %d", got)
	}
}

func TestWorker_PublishesDownstreamJobs(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()

	var chained int32
	reg.Register(handler.EventNewProxy, &chainingHandler{downstream: testEventSecondary})
	reg.Register(testEventSecondary, &countingHandler{count: &chained})

	q.Publish(context.Background(), contract.QueueJob{Type: handler.EventNewProxy, Payload: []byte(`{}`)})

	w := worker.New(q, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = w.RunEvents(ctx, []string{handler.EventNewProxy, testEventSecondary}, 2) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&chained) < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if got := atomic.LoadInt32(&chained); got < 1 {
		t.Fatalf("expected downstream job to be published and consumed, got %d", got)
	}
}

func TestWorker_NoHandlerRegistered(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()
	reg.Register(handler.EventNewProxy, &noopHandler{})
	// Missing testEventSecondary handler

	w := worker.New(q, reg, nil)
	err := w.RunEvents(context.Background(), []string{handler.EventNewProxy, testEventSecondary}, 3)
	if err == nil {
		t.Fatal("expected error for missing handler")
	}
}

// chainingHandler returns a downstream job of the given type.
type chainingHandler struct {
	downstream string
}

func (h *chainingHandler) Handle(_ context.Context, _ []byte) ([]contract.QueueJob, error) {
	return []contract.QueueJob{{Type: h.downstream, Payload: []byte(`{}`)}}, nil
}
