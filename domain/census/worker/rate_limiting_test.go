package worker_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/worker"
	"github.com/mihaiflorentin88/ffxiv-census/mock"
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
	rateLimiter := mock.NewProviderRateLimiter()

	reg := handler.NewRegistry()
	hAchievement := &simpleHandler{}
	hFC := &simpleHandler{}
	hCharacter := &simpleHandler{}
	hSweep := &simpleHandler{}

	reg.Register(handler.EventAchievementCensus, hAchievement)
	reg.Register(handler.EventFreeCompanyCensus, hFC)
	reg.Register(handler.EventCharacterCensus, hCharacter)
	reg.Register(handler.EventIDSweep, hSweep)

	w := worker.New(q, reg, nil, rateLimiter)
	w.SetPollInterval(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Publish one job of each type
	_, _ = q.Publish(
		ctx,
		contract.QueueJob{Type: handler.EventAchievementCensus, Payload: []byte(`{"character_id":1}`)},
		contract.QueueJob{Type: handler.EventFreeCompanyCensus, Payload: []byte(`{"fc_id":"fc1"}`)},
		contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(`{"character_id":2}`)},
		contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(`{"from":10,"to":20}`)},
	)

	// Pause Lodestone for 200ms (Tomestone remains available)
	rateLimiter.Pause(contract.ProviderLodestone, 200*time.Millisecond, "lodestone 429 rate limit")

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{
			handler.EventAchievementCensus,
			handler.EventFreeCompanyCensus,
			handler.EventCharacterCensus,
			handler.EventIDSweep,
		}, 4)
	}()

	// Wait 50ms - dual-source queues (character-census, id-sweep) should be processed,
	// while Lodestone-only queues (achievement-census, fc-census) should still be paused
	time.Sleep(60 * time.Millisecond)

	depth, _ := q.Depth(ctx)
	if depth[contract.QueueJobDone] != 2 {
		t.Fatalf("expected 2 dual-source jobs done (character + id-sweep), got done=%d", depth[contract.QueueJobDone])
	}
	if hAchievement.Count() > 0 {
		t.Fatalf("expected achievement-census to be paused, but handled=%d", hAchievement.Count())
	}
	if hFC.Count() > 0 {
		t.Fatalf("expected fc-census to be paused, but handled=%d", hFC.Count())
	}
	if hCharacter.Count() != 1 {
		t.Fatalf("expected character-census to be processed via Tomestone, got %d", hCharacter.Count())
	}
	if hSweep.Count() != 1 {
		t.Fatalf("expected id-sweep to be processed via Tomestone, got %d", hSweep.Count())
	}

	// Now wait for lodestone unpause
	time.Sleep(180 * time.Millisecond)

	depth, _ = q.Depth(ctx)
	if depth[contract.QueueJobDone] != 4 {
		t.Fatalf("expected 4 jobs done after unpause, got done=%d", depth[contract.QueueJobDone])
	}
	if hAchievement.Count() != 1 {
		t.Errorf("expected achievement-census to be processed after unpause, got %d", hAchievement.Count())
	}
	if hFC.Count() != 1 {
		t.Errorf("expected fc-census to be processed after unpause, got %d", hFC.Count())
	}

	cancel()
	_ = <-done
}

func TestWorker_AllProvidersPaused_SleepsUntilEarliestCooldown(t *testing.T) {
	q := mockqueue.NewFake()
	rateLimiter := mock.NewProviderRateLimiter()

	reg := handler.NewRegistry()
	hCharacter := &simpleHandler{}
	reg.Register(handler.EventCharacterCensus, hCharacter)

	w := worker.New(q, reg, nil, rateLimiter)
	w.SetPollInterval(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, _ = q.Publish(
		ctx,
		contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(`{"character_id":1}`)},
	)

	// Pause both providers for 150ms
	rateLimiter.Pause(contract.ProviderLodestone, 150*time.Millisecond, "lodestone rate limited")
	rateLimiter.Pause(contract.ProviderTomestone, 150*time.Millisecond, "tomestone rate limited")

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{handler.EventCharacterCensus}, 1)
	}()

	// Wait 50ms - should be sleeping without claiming
	time.Sleep(50 * time.Millisecond)
	if hCharacter.Count() > 0 {
		t.Fatalf("expected 0 jobs handled while both providers paused, got %d", hCharacter.Count())
	}

	// Wait for cooldown to expire (another 130ms)
	time.Sleep(130 * time.Millisecond)

	if hCharacter.Count() != 1 {
		t.Fatalf("expected 1 job handled after cooldown expired, got %d", hCharacter.Count())
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
	h4 := &simpleHandler{}

	reg.Register(handler.EventIDSweep, h1)
	reg.Register(handler.EventCharacterCensus, h2)
	reg.Register(handler.EventAchievementCensus, h3)
	reg.Register(handler.EventFreeCompanyCensus, h4)

	w := worker.New(q, reg, nil)
	w.SetPollInterval(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, _ = q.Publish(
		ctx,
		contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(`{}`)},
		contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(`{}`)},
		contract.QueueJob{Type: handler.EventAchievementCensus, Payload: []byte(`{}`)},
		contract.QueueJob{Type: handler.EventFreeCompanyCensus, Payload: []byte(`{}`)},
	)

	done := make(chan error, 1)
	go func() {
		// Passing empty slice defaults to all 4 event types
		done <- w.RunEvents(ctx, nil, 2)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	_ = <-done

	depth, _ := q.Depth(context.Background())
	if depth[contract.QueueJobDone] != 4 {
		t.Fatalf("expected all 4 jobs done, got %d", depth[contract.QueueJobDone])
	}
}
