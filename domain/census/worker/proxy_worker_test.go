package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xivapi/godestone/v2"

	censushandler "github.com/mihaiflorentin88/ffxiv-census/domain/census/handler"
	proxydomain "github.com/mihaiflorentin88/ffxiv-census/domain/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/mock"
	"github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// fakeProxyQueue is a minimal queue for proxy rotation tests.
type fakeProxyQueue struct {
	mu   sync.Mutex
	jobs []contract.QueueJob
}

func (q *fakeProxyQueue) Publish(_ context.Context, job contract.QueueJob) error {
	q.mu.Lock()
	q.jobs = append(q.jobs, job)
	q.mu.Unlock()
	return nil
}

func (q *fakeProxyQueue) Consume(ctx context.Context, _ []string, _ int, fn func(context.Context, contract.QueueJob) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		q.mu.Lock()
		if len(q.jobs) == 0 {
			q.mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			continue
		}
		job := q.jobs[0]
		q.jobs = q.jobs[1:]
		q.mu.Unlock()

		if err := fn(ctx, job); err != nil {
			return err
		}
	}
}

func (q *fakeProxyQueue) ConsumeFailed(context.Context, []string, int) error { return nil }
func (q *fakeProxyQueue) Close() error                                       { return nil }

// noopCensusHandler handles census events without doing real work.
type noopCensusHandler struct{}

func (h *noopCensusHandler) Handle(_ context.Context, _ []byte) ([]contract.QueueJob, error) {
	return nil, nil
}

// countingCensusHandler counts invocations.
type countingCensusHandler struct {
	count *int32
}

func (h *countingCensusHandler) Handle(_ context.Context, _ []byte) ([]contract.QueueJob, error) {
	atomic.AddInt32(h.count, 1)
	return nil, nil
}

// fakeLodestoneClient implements contract.LodestoneClient for tests.
type fakeLodestoneClient struct{}

func (c *fakeLodestoneClient) FetchCharacter(context.Context, uint32) (*godestone.Character, error) {
	return nil, nil
}

func (c *fakeLodestoneClient) FetchAchievements(context.Context, uint32) ([]*godestone.AchievementInfo, *godestone.AllAchievementInfo, error) {
	return nil, nil, nil
}

func (c *fakeLodestoneClient) FetchFreeCompany(context.Context, string) (*godestone.FreeCompany, error) {
	return nil, nil
}

func (c *fakeLodestoneClient) FetchFreeCompanyMembers(context.Context, string) ([]uint32, error) {
	return nil, nil
}

// fakeTomestoneClient implements contract.TomestoneClient for tests.
type fakeTomestoneClient struct{}

func (c *fakeTomestoneClient) FetchCharacterProfile(context.Context, uint32, bool) (*contract.TomestoneCharacter, error) {
	return nil, nil
}

func (c *fakeTomestoneClient) FetchCharacterProfileByName(context.Context, string, string, bool) (*contract.TomestoneCharacter, error) {
	return nil, nil
}
func (c *fakeTomestoneClient) IsConfigured() bool { return false }

func intPtr(v int) *int { return &v }

func newTestCensusWorker(q contract.Queue, logger contract.Logger) *Worker {
	return New(q, nil, logger)
}

func newTestHandlers() func(contract.LodestoneClient, contract.TomestoneClient, contract.ProviderRateLimiter) *censushandler.Registry {
	return func(_ contract.LodestoneClient, _ contract.TomestoneClient, _ contract.ProviderRateLimiter) *censushandler.Registry {
		reg := censushandler.NewRegistry()
		reg.Register(censushandler.EventIDSweep, &noopCensusHandler{})
		return reg
	}
}

func TestProxyWorkerLoop_WaitsForProxy(t *testing.T) {
	// Start with no proxies. Worker should wait, then proceed when one appears.
	repo := repository.NewFakeProxyRepository()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := &fakeProxyQueue{}

	q.Publish(context.Background(), contract.QueueJob{
		Type:    censushandler.EventIDSweep,
		Payload: []byte(`{}`),
	})

	w := newTestCensusWorker(q, logger)
	proxyHub := proxydomain.NewProxyHub(repo, 5*time.Minute, nil)

	var started int32
	handlers := func(_ contract.LodestoneClient, _ contract.TomestoneClient, _ contract.ProviderRateLimiter) *censushandler.Registry {
		reg := censushandler.NewRegistry()
		reg.Register(censushandler.EventIDSweep, &countingCensusHandler{count: &started})
		return reg
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- w.RunEventsWithProxy(
			ctx,
			[]string{censushandler.EventIDSweep},
			1,
			"test",
			proxyHub,
			handlers,
			func(string, contract.ProviderRateLimiter) (contract.LodestoneClient, error) {
				return &fakeLodestoneClient{}, nil
			},
			func(string, contract.ProviderRateLimiter) (contract.TomestoneClient, error) {
				return &fakeTomestoneClient{}, nil
			},
			func() contract.ProviderRateLimiter { return nil },
		)
	}()

	// Worker should be waiting — no proxy yet.
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&started) > 0 {
		t.Fatal("worker started before proxy was available")
	}

	// Insert a proxy and mark it active.
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
	})
	proxies, _ := repo.ListForScan(context.Background(), 10)
	if len(proxies) > 0 {
		now := time.Now().UTC()
		repo.UpdateStatus(context.Background(), proxies[0].ID, contract.ProxyStatusActive, intPtr(100), 0, &now)
	}

	// Wait for the worker to pick it up.
	deadline := time.After(5 * time.Second)
	for atomic.LoadInt32(&started) == 0 {
		select {
		case <-deadline:
			t.Fatal("worker did not start after proxy became available")
		case <-time.After(50 * time.Millisecond):
		}
	}

	cancel()
	<-done
}

func TestReplaceProxy_MarksBadBeforeReplacement(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.1.1.1", Port: 8080, Source: "test",
	})
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "2.2.2.2", Port: 8080, Source: "test",
	})

	proxies, _ := repo.ListForScan(context.Background(), 10)
	for _, p := range proxies {
		now := time.Now().UTC()
		repo.UpdateStatus(context.Background(), p.ID, contract.ProxyStatusActive, intPtr(100), 0, &now)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := &fakeProxyQueue{}
	w := New(q, nil, logger)
	proxyHub := proxydomain.NewProxyHub(repo, 5*time.Minute, nil)

	owner := "test-w0"
	p1, err := proxyHub.NewProxy(context.Background(), owner)
	if err != nil || p1 == nil {
		t.Fatalf("NewProxy: proxy=%v err=%v", p1, err)
	}

	p2, _, _, _, _, err := w.replaceProxy(
		context.Background(),
		p1,
		owner,
		true,
		proxyHub,
		func(string, contract.ProviderRateLimiter) (contract.LodestoneClient, error) {
			return &fakeLodestoneClient{}, nil
		},
		func(string, contract.ProviderRateLimiter) (contract.TomestoneClient, error) {
			return &fakeTomestoneClient{}, nil
		},
		func() contract.ProviderRateLimiter { return nil },
		newTestHandlers(),
	)
	if err != nil {
		t.Fatalf("replaceProxy: %v", err)
	}
	if p2 == nil {
		t.Fatal("expected replacement proxy")
	}

	// First proxy should be marked failed (inactive).
	rec, _ := repo.Get(context.Background(), p1.ID())
	if rec.Status != contract.ProxyStatusInactive {
		t.Errorf("expected first proxy inactive, got %s", rec.Status)
	}
	if rec.FailCount != 1 {
		t.Errorf("expected first proxy fail_count 1, got %d", rec.FailCount)
	}

	if p2.ID() == p1.ID() {
		t.Error("replacement proxy should be different from the original")
	}
}

func TestReplaceProxy_ReplacementTransportError(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.1.1.1", Port: 8080, Source: "test",
	})
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "2.2.2.2", Port: 8080, Source: "test",
	})

	proxies, _ := repo.ListForScan(context.Background(), 10)
	for _, p := range proxies {
		now := time.Now().UTC()
		repo.UpdateStatus(context.Background(), p.ID, contract.ProxyStatusActive, intPtr(100), 0, &now)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := &fakeProxyQueue{}
	w := New(q, nil, logger)
	proxyHub := proxydomain.NewProxyHub(repo, 5*time.Minute, nil)

	owner := "test-w0"
	p1, err := proxyHub.NewProxy(context.Background(), owner)
	if err != nil || p1 == nil {
		t.Fatalf("NewProxy: proxy=%v err=%v", p1, err)
	}

	p2, _, _, _, _, err := w.replaceProxy(
		context.Background(),
		p1,
		owner,
		true,
		proxyHub,
		func(string, contract.ProviderRateLimiter) (contract.LodestoneClient, error) {
			return &fakeLodestoneClient{}, nil
		},
		func(string, contract.ProviderRateLimiter) (contract.TomestoneClient, error) {
			return &fakeTomestoneClient{}, nil
		},
		func() contract.ProviderRateLimiter { return nil },
		newTestHandlers(),
	)
	if err != nil {
		t.Fatalf("replaceProxy: %v", err)
	}

	// Mark the replacement as failed (simulating transport error).
	if p2 != nil {
		p2.MarkFailed(context.Background(), owner)
		rec, _ := repo.Get(context.Background(), p2.ID())
		if rec.Status != contract.ProxyStatusInactive {
			t.Errorf("expected replacement proxy inactive, got %s", rec.Status)
		}
	}
}

func TestProxyWorkerLoop_CancellationWhileWaiting(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := &fakeProxyQueue{}
	w := newTestCensusWorker(q, logger)
	proxyHub := proxydomain.NewProxyHub(repo, 5*time.Minute, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := w.RunEventsWithProxy(
		ctx,
		[]string{censushandler.EventIDSweep},
		1,
		"test",
		proxyHub,
		newTestHandlers(),
		func(string, contract.ProviderRateLimiter) (contract.LodestoneClient, error) {
			return &fakeLodestoneClient{}, nil
		},
		func(string, contract.ProviderRateLimiter) (contract.TomestoneClient, error) {
			return &fakeTomestoneClient{}, nil
		},
		func() contract.ProviderRateLimiter { return nil },
	)

	elapsed := time.Since(start)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("unexpected error: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("worker took too long to exit: %v", elapsed)
	}
}

// trackingLimiter wraps a ProviderRateLimiter and records its identity.
type trackingLimiter struct {
	contract.ProviderRateLimiter
	id int
}

func TestRunEventsWithProxyCreatesIsolatedWorkerDependencies(t *testing.T) {
	// Set up two active proxies so two goroutines can each acquire one.
	repo := repository.NewFakeProxyRepository()
	for _, ip := range []string{"10.0.0.1", "10.0.0.2"} {
		repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
			Protocol: "http", IP: ip, Port: 8080, Source: "test",
		})
	}
	proxies, _ := repo.ListForScan(context.Background(), 10)
	for _, p := range proxies {
		now := time.Now().UTC()
		repo.UpdateStatus(context.Background(), p.ID, contract.ProxyStatusActive, intPtr(100), 0, &now)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := &fakeProxyQueue{}
	proxyHub := proxydomain.NewProxyHub(repo, 5*time.Minute, nil)

	// Publish two jobs so each goroutine gets one.
	for range 2 {
		q.Publish(context.Background(), contract.QueueJob{
			Type:    censushandler.EventIDSweep,
			Payload: []byte(`{}`),
		})
	}

	// Track limiter instances per goroutine.
	var mu sync.Mutex
	// goroutineKey → set of limiter IDs seen in factories.
	limiterPerGoroutine := make(map[string]map[int]bool)
	// All distinct limiter instances.
	allLimiters := make(map[int]contract.ProviderRateLimiter)
	limiterCounter := 0

	newRateLimiter := func() contract.ProviderRateLimiter {
		mu.Lock()
		defer mu.Unlock()
		limiterCounter++
		tl := &trackingLimiter{ProviderRateLimiter: mock.NewProviderRateLimiter(), id: limiterCounter}
		allLimiters[limiterCounter] = tl
		return tl
	}

	// Use the proxy URL as a goroutine key (each goroutine gets a unique proxy).
	newLodestoneClient := func(proxyURL string, limiter contract.ProviderRateLimiter) (contract.LodestoneClient, error) {
		mu.Lock()
		if tl, ok := limiter.(*trackingLimiter); ok {
			if limiterPerGoroutine[proxyURL] == nil {
				limiterPerGoroutine[proxyURL] = make(map[int]bool)
			}
			limiterPerGoroutine[proxyURL][tl.id] = true
		}
		mu.Unlock()
		return &fakeLodestoneClient{}, nil
	}
	newTomestoneClient := func(proxyURL string, limiter contract.ProviderRateLimiter) (contract.TomestoneClient, error) {
		mu.Lock()
		if tl, ok := limiter.(*trackingLimiter); ok {
			if limiterPerGoroutine[proxyURL] == nil {
				limiterPerGoroutine[proxyURL] = make(map[int]bool)
			}
			limiterPerGoroutine[proxyURL][tl.id] = true
		}
		mu.Unlock()
		return &fakeTomestoneClient{}, nil
	}

	var jobsProcessed int32
	newHandlers := func(_ contract.LodestoneClient, _ contract.TomestoneClient, limiter contract.ProviderRateLimiter) *censushandler.Registry {
		mu.Lock()
		if tl, ok := limiter.(*trackingLimiter); ok {
			allLimiters[tl.id] = tl
		}
		mu.Unlock()
		reg := censushandler.NewRegistry()
		reg.Register(censushandler.EventIDSweep, &countingCensusHandler{count: &jobsProcessed})
		return reg
	}

	w := newTestCensusWorker(q, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel once both jobs are processed.
	go func() {
		for {
			if atomic.LoadInt32(&jobsProcessed) >= 2 {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	err := w.RunEventsWithProxy(
		ctx,
		[]string{censushandler.EventIDSweep},
		2,
		"test",
		proxyHub,
		newHandlers,
		newLodestoneClient,
		newTomestoneClient,
		newRateLimiter,
	)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("RunEventsWithProxy: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Require exactly 2 distinct limiter instances (one per goroutine).
	if len(allLimiters) != 2 {
		t.Errorf("expected 2 distinct ProviderRateLimiter instances, got %d", len(allLimiters))
	}

	// Each goroutine must have used exactly one limiter consistently across all factories.
	for proxyURL, ids := range limiterPerGoroutine {
		if len(ids) != 1 {
			t.Errorf("goroutine for proxy %s used %d distinct limiters, want 1", proxyURL, len(ids))
		}
	}
}

func TestWaitForProxy_ExponentialBackoff(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := newTestCensusWorker(nil, logger)
	proxyHub := proxydomain.NewProxyHub(repo, 5*time.Minute, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()

	// Insert a proxy after a short delay to measure how long waitForProxy waited.
	go func() {
		time.Sleep(1 * time.Second)
		repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
			Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
		})
		proxies, _ := repo.ListForScan(context.Background(), 10)
		if len(proxies) > 0 {
			now := time.Now().UTC()
			repo.UpdateStatus(context.Background(), proxies[0].ID, contract.ProxyStatusActive, intPtr(100), 0, &now)
		}
	}()

	p, err := w.waitForProxy(ctx, "test-owner", proxyHub)
	if err != nil {
		t.Fatalf("waitForProxy: %v", err)
	}
	if p == nil {
		t.Fatal("expected proxy, got nil")
	}

	elapsed := time.Since(start)
	// With exponential backoff starting at 5s, the first retry is at 5s.
	// A proxy inserted at 1s won't be seen until the next backoff fires.
	if elapsed < 4*time.Second {
		t.Errorf("waitForProxy returned too quickly (%v), expected at least 4s due to backoff", elapsed)
	}
	if elapsed > 8*time.Second {
		t.Errorf("waitForProxy took too long (%v), expected < 8s", elapsed)
	}
}

func TestWaitForProxy_NotificationChannel(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := newTestCensusWorker(nil, logger)
	proxyHub := proxydomain.NewProxyHub(repo, 5*time.Minute, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Insert and activate a proxy before starting waitForProxy.
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
	})
	proxies, _ := repo.ListForScan(context.Background(), 10)
	if len(proxies) == 0 {
		t.Fatal("expected at least one proxy")
	}
	now := time.Now().UTC()
	repo.UpdateStatus(context.Background(), proxies[0].ID, contract.ProxyStatusActive, intPtr(100), 0, &now)

	// Lock all proxies so waitForProxy blocks.
	_, err := repo.ClaimProxy(context.Background(), "other-owner", 5*time.Minute)
	if err != nil {
		t.Fatalf("claim proxy: %v", err)
	}

	start := time.Now()

	type result struct {
		proxy *proxydomain.Proxy
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		p, err := w.waitForProxy(ctx, "test-owner", proxyHub)
		ch <- result{p, err}
	}()

	// Give waitForProxy time to enter the select.
	time.Sleep(100 * time.Millisecond)

	// Release the proxy and notify.
	err = repo.ReleaseProxy(context.Background(), proxies[0].ID, "other-owner")
	if err != nil {
		t.Fatalf("release proxy: %v", err)
	}
	proxyHub.NotifyAvailable()

	select {
	case r := <-ch:
		elapsed := time.Since(start)
		if r.err != nil {
			t.Fatalf("waitForProxy: %v", r.err)
		}
		if r.proxy == nil {
			t.Fatal("expected proxy, got nil")
		}
		if elapsed > 2*time.Second {
			t.Errorf("waitForProxy took %v after notification, expected < 2s", elapsed)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for proxy")
	}
}
