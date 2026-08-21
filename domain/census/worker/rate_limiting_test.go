package worker_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/worker"
	mockqueue "github.com/mihaiflorentin88/ffxiv-census/mock/queue"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type simpleHandler struct {
	mu           sync.Mutex
	handledCount int
}

func (h *simpleHandler) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	h.mu.Lock()
	h.handledCount++
	h.mu.Unlock()
	return nil, nil
}

func (h *simpleHandler) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.handledCount
}

func TestWorker_LodestoneRateLimit_PausesLodestoneQueues_RunsDualSourceQueuesOnTomestone(t *testing.T) {
	q := mockqueue.NewFake()

	reg := handler.NewRegistry()
	hAchievement := &simpleHandler{}
	hCharacter := &simpleHandler{}
	hSweep := &simpleHandler{}

	reg.Register(handler.EventAchievementCensus, hAchievement)
	reg.Register(handler.EventCharacterCensus, hCharacter)
	reg.Register(handler.EventIDSweep, hSweep)

	w := worker.New(q, reg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Publish one job of each type.
	_ = q.Publish(ctx, contract.QueueJob{Type: handler.EventAchievementCensus, Payload: []byte(`{"character_id":1}`)})
	_ = q.Publish(ctx, contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(`{"character_id":2}`)})
	_ = q.Publish(ctx, contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(`{"from":10,"to":20}`)})

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{
			handler.EventAchievementCensus,
			handler.EventCharacterCensus,
			handler.EventIDSweep,
		}, 3)
	}()

	time.Sleep(100 * time.Millisecond)

	if hAchievement.Count() != 1 {
		t.Fatalf("expected achievement-census handled, got %d", hAchievement.Count())
	}
	if hCharacter.Count() != 1 {
		t.Fatalf("expected character-census handled, got %d", hCharacter.Count())
	}
	if hSweep.Count() != 1 {
		t.Fatalf("expected id-sweep handled, got %d", hSweep.Count())
	}

	cancel()
	_ = <-done
}

func TestWorker_AllProvidersPaused_SleepsUntilEarliestCooldown(t *testing.T) {
	q := mockqueue.NewFake()

	reg := handler.NewRegistry()
	hCharacter := &simpleHandler{}
	reg.Register(handler.EventCharacterCensus, hCharacter)

	w := worker.New(q, reg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = q.Publish(ctx, contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(`{"character_id":1}`)})

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{handler.EventCharacterCensus}, 1)
	}()

	time.Sleep(100 * time.Millisecond)

	if hCharacter.Count() != 1 {
		t.Fatalf("expected 1 job handled, got %d", hCharacter.Count())
	}

	cancel()
	_ = <-done
}

func TestWorker_MultiQueueDefaultAll(t *testing.T) {
	q := mockqueue.NewFake()
	reg := handler.NewRegistry()

	h1 := &simpleHandler{}
	h2 := &simpleHandler{}
	h3 := &simpleHandler{}

	reg.Register(handler.EventIDSweep, h1)
	reg.Register(handler.EventCharacterCensus, h2)
	reg.Register(handler.EventAchievementCensus, h3)

	w := worker.New(q, reg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = q.Publish(ctx, contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(`{}`)})
	_ = q.Publish(ctx, contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(`{}`)})
	_ = q.Publish(ctx, contract.QueueJob{Type: handler.EventAchievementCensus, Payload: []byte(`{}`)})

	done := make(chan error, 1)
	go func() {
		// Passing nil defaults to all registered event types.
		done <- w.RunEvents(ctx, nil, 2)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	_ = <-done

	if h1.Count() != 1 {
		t.Fatalf("expected id-sweep handled, got %d", h1.Count())
	}
	if h2.Count() != 1 {
		t.Fatalf("expected character-census handled, got %d", h2.Count())
	}
	if h3.Count() != 1 {
		t.Fatalf("expected achievement-census handled, got %d", h3.Count())
	}
}
