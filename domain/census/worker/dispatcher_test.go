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

func TestWorker_DynamicDispatcher_DedicatedPoolsConcurrentProcessing(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()

	hSweep := &concurrencyTrackingHandler{holdDuration: 50 * time.Millisecond}
	hChar := &concurrencyTrackingHandler{holdDuration: 50 * time.Millisecond}

	reg.Register(handler.EventIDSweep, hSweep)
	reg.Register(handler.EventCharacterCensus, hChar)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Enqueue 20 jobs of each type with unique payloads
	for i := range 20 {
		if err := q.Publish(ctx, contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(fmt.Sprintf(`{"sweep":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
		if err := q.Publish(ctx, contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(fmt.Sprintf(`{"char":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
	}

	w := worker.New(q, reg, nil)

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{
			handler.EventIDSweep,
			handler.EventCharacterCensus,
		}, 20)
	}()

	// Allow workers to process jobs concurrently
	time.Sleep(300 * time.Millisecond)
	cancel()
	_ = <-done

	peakChar := atomic.LoadInt32(&hChar.peakInFlight)
	peakSweep := atomic.LoadInt32(&hSweep.peakInFlight)

	if peakChar == 0 {
		t.Errorf("expected character-census to be processed concurrently, got peak=0")
	}
	if peakSweep == 0 {
		t.Errorf("expected id-sweep to be processed concurrently, got peak=0")
	}
	if atomic.LoadInt32(&hChar.totalHandled) == 0 {
		t.Errorf("expected character-census jobs to be handled")
	}
	if atomic.LoadInt32(&hSweep.totalHandled) == 0 {
		t.Errorf("expected id-sweep jobs to be handled")
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
	for i := range 5 {
		if err := q.Publish(ctx, contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(fmt.Sprintf(`{"sweep":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
		if err := q.Publish(ctx, contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(fmt.Sprintf(`{"char":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
	}

	w := worker.New(q, reg, nil)

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

func TestWorker_DynamicDispatcher_NoStarvationWhenPrimaryAlreadyRunning(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()

	// Primary (id-sweep) jobs take longer (e.g. 80ms)
	hSweep := &concurrencyTrackingHandler{holdDuration: 80 * time.Millisecond}
	// Updates and secondary jobs are fast (e.g. 5ms)
	hChar := &concurrencyTrackingHandler{holdDuration: 5 * time.Millisecond}
	hAch := &concurrencyTrackingHandler{holdDuration: 5 * time.Millisecond}

	reg.Register(handler.EventIDSweep, hSweep)
	reg.Register(handler.EventCharacterCensus, hChar)
	reg.Register(handler.EventAchievementCensus, hAch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initially enqueue 100 id-sweep jobs (fill the entire queue with primary)
	for i := range 100 {
		if err := q.Publish(ctx, contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(fmt.Sprintf(`{"id":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
	}

	w := worker.New(q, reg, nil)

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{
			handler.EventIDSweep,
			handler.EventCharacterCensus,
			handler.EventAchievementCensus,
		}, 20)
	}()

	// 2. Wait for workers to saturate with id-sweep
	time.Sleep(50 * time.Millisecond)

	// 3. Now enqueue character-census and achievement-census jobs
	for i := range 20 {
		if err := q.Publish(ctx, contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(fmt.Sprintf(`{"char":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
		if err := q.Publish(ctx, contract.QueueJob{Type: handler.EventAchievementCensus, Payload: []byte(fmt.Sprintf(`{"ach":%d}`, i))}); err != nil {
			t.Fatal(err)
		}
	}

	// 4. Wait 200ms
	time.Sleep(200 * time.Millisecond)
	cancel()
	_ = <-done

	charHandled := atomic.LoadInt32(&hChar.totalHandled)
	achHandled := atomic.LoadInt32(&hAch.totalHandled)

	if charHandled == 0 {
		t.Errorf("character-census was starved by id-sweep (handled 0 jobs)")
	}
	if achHandled == 0 {
		t.Errorf("achievement-census was starved by id-sweep (handled 0 jobs)")
	}
}
