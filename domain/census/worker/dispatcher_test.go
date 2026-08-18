package worker_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/worker"
	mockqueue "github.com/mihaiflorentin88/ffxiv-census/mock/queue"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type concurrencyTrackingHandler struct {
	inFlight     int32
	peakInFlight int32
	totalHandled int32
	holdDuration time.Duration
}

func (h *concurrencyTrackingHandler) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	current := atomic.AddInt32(&h.inFlight, 1)
	defer atomic.AddInt32(&h.inFlight, -1)

	for {
		peak := atomic.LoadInt32(&h.peakInFlight)
		if current <= peak || atomic.CompareAndSwapInt32(&h.peakInFlight, peak, current) {
			break
		}
	}

	atomic.AddInt32(&h.totalHandled, 1)
	if h.holdDuration > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(h.holdDuration):
		}
	}
	return nil, nil
}

func TestWorker_DynamicDispatcher_CeilingEnforcement(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()

	hSweep := &concurrencyTrackingHandler{holdDuration: 50 * time.Millisecond}
	hChar := &concurrencyTrackingHandler{holdDuration: 50 * time.Millisecond}
	hFC := &concurrencyTrackingHandler{holdDuration: 50 * time.Millisecond}

	reg.Register(handler.EventIDSweep, hSweep)
	reg.Register(handler.EventCharacterCensus, hChar)
	reg.Register(handler.EventFreeCompanyCensus, hFC)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Enqueue 20 jobs of each type with unique payloads
	for i := 0; i < 20; i++ {
		_, _ = q.Publish(ctx,
			contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(fmt.Sprintf(`{"sweep":%d}`, i))},
			contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(fmt.Sprintf(`{"char":%d}`, i))},
			contract.QueueJob{Type: handler.EventFreeCompanyCensus, Payload: []byte(fmt.Sprintf(`{"fc":%d}`, i))},
		)
	}

	// Concurrency = 20:
	// - Updates ceiling = 25% of 20 = 5
	// - Secondary ceiling = 25% of 20 = 5
	// - Primary ceiling = 100% of 20 = 20
	w := worker.New(q, reg, nil)
	w.SetPollInterval(10 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{
			handler.EventIDSweep,
			handler.EventCharacterCensus,
			handler.EventFreeCompanyCensus,
		}, 20)
	}()

	// Allow workers to process jobs concurrently
	time.Sleep(300 * time.Millisecond)
	cancel()
	_ = <-done

	peakChar := atomic.LoadInt32(&hChar.peakInFlight)
	peakFC := atomic.LoadInt32(&hFC.peakInFlight)

	if peakChar > 5 {
		t.Errorf("character-census exceeded 25%% ceiling: peak=%d, want <= 5", peakChar)
	}
	if peakFC > 5 {
		t.Errorf("free-company-census exceeded 25%% ceiling: peak=%d, want <= 5", peakFC)
	}
	if atomic.LoadInt32(&hSweep.totalHandled) == 0 {
		t.Errorf("expected id-sweep jobs to be processed, got 0")
	}
}

func TestWorker_DynamicDispatcher_FullCapacityWhenSingleCategory(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()

	hSweep := &concurrencyTrackingHandler{holdDuration: 40 * time.Millisecond}
	hChar := &concurrencyTrackingHandler{holdDuration: 40 * time.Millisecond}

	reg.Register(handler.EventIDSweep, hSweep)
	reg.Register(handler.EventCharacterCensus, hChar)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Enqueue ONLY id-sweep jobs with unique payloads
	for i := 0; i < 30; i++ {
		_, _ = q.Publish(ctx, contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(fmt.Sprintf(`{"id":%d}`, i))})
	}

	w := worker.New(q, reg, nil)
	w.SetPollInterval(5 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{handler.EventIDSweep, handler.EventCharacterCensus}, 8)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()
	_ = <-done

	peakSweep := atomic.LoadInt32(&hSweep.peakInFlight)
	if peakSweep < 6 {
		t.Errorf("expected id-sweep to utilize full worker capacity, got peak=%d, want >= 6", peakSweep)
	}
}

func TestWorker_DynamicDispatcher_LowConcurrencyRoundRobin(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()

	var orderMu sync.Mutex
	var processOrder []string

	hSweep := &orderRecordingHandler{orderMu: &orderMu, order: &processOrder, eventType: "id-sweep"}
	hChar := &orderRecordingHandler{orderMu: &orderMu, order: &processOrder, eventType: "character-census"}

	reg.Register(handler.EventIDSweep, hSweep)
	reg.Register(handler.EventCharacterCensus, hChar)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Enqueue 5 jobs of each type with unique payloads
	for i := 0; i < 5; i++ {
		_, _ = q.Publish(ctx,
			contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(fmt.Sprintf(`{"sweep":%d}`, i))},
			contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(fmt.Sprintf(`{"char":%d}`, i))},
		)
	}

	w := worker.New(q, reg, nil)
	w.SetPollInterval(5 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{handler.EventIDSweep, handler.EventCharacterCensus}, 1)
	}()

	// Wait until all 10 jobs are processed
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		orderMu.Lock()
		count := len(processOrder)
		orderMu.Unlock()
		if count >= 10 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	_ = <-done

	orderMu.Lock()
	defer orderMu.Unlock()

	if len(processOrder) < 10 {
		t.Fatalf("expected 10 jobs processed with concurrency=1, got %d", len(processOrder))
	}

	sweepCount, charCount := 0, 0
	for _, evt := range processOrder {
		if evt == "id-sweep" {
			sweepCount++
		} else if evt == "character-census" {
			charCount++
		}
	}

	if sweepCount != 5 || charCount != 5 {
		t.Errorf("expected 5 sweep and 5 char, got sweep=%d char=%d", sweepCount, charCount)
	}
}

func TestWorker_DynamicDispatcher_RetriesCeiling(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()

	hSweep := &concurrencyTrackingHandler{holdDuration: 40 * time.Millisecond}
	reg.Register(handler.EventIDSweep, hSweep)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Enqueue 10 jobs that were already attempted (retries) and 10 new jobs
	for i := 0; i < 10; i++ {
		_, _ = q.Publish(ctx,
			contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(fmt.Sprintf(`{"retry":%d}`, i))},
			contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(fmt.Sprintf(`{"new":%d}`, i))},
		)
	}

	// Mark the first 10 as retries by claiming and failing them once
	claimed, _ := q.Claim(ctx, handler.EventIDSweep, 10, contract.ClaimModeAny)
	for _, j := range claimed {
		_ = q.Retry(ctx, j.ID, "simulated error")
	}

	// Concurrency = 12 -> Retries ceiling = 25% of 12 = 3
	w := worker.New(q, reg, nil)
	w.SetPollInterval(5 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{handler.EventIDSweep}, 12)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()
	_ = <-done

	total := atomic.LoadInt32(&hSweep.totalHandled)
	if total < 10 {
		t.Errorf("expected at least 10 jobs handled, got %d", total)
	}
}

type orderRecordingHandler struct {
	orderMu   *sync.Mutex
	order     *[]string
	eventType string
}

func (h *orderRecordingHandler) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	h.orderMu.Lock()
	*h.order = append(*h.order, h.eventType)
	h.orderMu.Unlock()
	return nil, nil
}
