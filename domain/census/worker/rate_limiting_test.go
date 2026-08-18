package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	"github.com/mihaiflorentin88/ffxiv-census/domain/census/worker"
	"github.com/mihaiflorentin88/ffxiv-census/mock"
	mockqueue "github.com/mihaiflorentin88/ffxiv-census/mock/queue"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type simpleHandler struct {
	handledCount int
}

func (h *simpleHandler) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	h.handledCount++
	return nil, nil
}

func TestWorker_RateLimiterPausesAffectedQueuesOnly(t *testing.T) {
	q := mockqueue.NewFake()
	rateLimiter := mock.NewProviderRateLimiter()

	reg := handler.NewRegistry()
	hLodestone := &simpleHandler{}
	hTomestone := &simpleHandler{}

	reg.Register(handler.EventCharacterCensus, hLodestone)
	reg.Register(handler.EventIDSweep, hTomestone)

	w := worker.New(q, reg, nil, rateLimiter)
	w.SetPollInterval(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Publish one job of each type
	_, _ = q.Publish(ctx,
		contract.QueueJob{Type: handler.EventCharacterCensus, Payload: []byte(`{"id":1}`)},
		contract.QueueJob{Type: handler.EventIDSweep, Payload: []byte(`{"chunk":1}`)},
	)

	// Pause Lodestone for 200ms
	rateLimiter.Pause(contract.ProviderLodestone, 200*time.Millisecond, "rate limit 429")

	done := make(chan error, 1)
	go func() {
		done <- w.RunEvents(ctx, []string{handler.EventCharacterCensus, handler.EventIDSweep}, 2)
	}()

	// Wait 50ms - tomestone/id-sweep should be processed, character-census should still be pending
	time.Sleep(50 * time.Millisecond)

	depth, _ := q.Depth(ctx)
	if depth[contract.QueueJobDone] < 1 {
		t.Fatalf("expected at least 1 job done (id-sweep), got done=%d", depth[contract.QueueJobDone])
	}
	if hCharacterCount := hLodestone.handledCount; hCharacterCount > 0 {
		t.Fatalf("expected character-census to be paused, but handled=%d", hCharacterCount)
	}

	// Now wait for lodestone unpause
	time.Sleep(180 * time.Millisecond)

	depth, _ = q.Depth(ctx)
	if depth[contract.QueueJobDone] != 2 {
		t.Fatalf("expected 2 jobs done after unpause, got done=%d", depth[contract.QueueJobDone])
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

	_, _ = q.Publish(ctx,
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
