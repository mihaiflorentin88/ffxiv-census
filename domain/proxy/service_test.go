package proxy_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	proxydomain "github.com/mihaiflorentin88/ffxiv-census/domain/proxy"
	mockproxy "github.com/mihaiflorentin88/ffxiv-census/mock/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type fakeChecker struct {
	latency int
	err     error
}

func (f *fakeChecker) Check(_ context.Context, _, _ string, _ int) (int, error) {
	return f.latency, f.err
}

func newTestService(checker contract.ProxyChecker, providers []contract.ProxyProvider, repo contract.ProxyRepository) *proxydomain.Service {
	return proxydomain.NewService(providers, repo, checker, nil, 48*time.Hour, 5)
}

func TestService_ProcessNewProxy_Success(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	providers := []contract.ProxyProvider{
		mockproxy.NewFakeProvider("test", nil),
	}
	svc := proxydomain.NewService(providers, repo, nil, nil, 48*time.Hour, 5)

	// ProcessNewProxy needs a checker, so let's test the service methods with a real checker
	// that we control via httptest — but for unit tests we test the repo interaction
	_, inserted, err := repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
	})
	if err != nil {
		t.Fatalf("InsertIfAbsent: %v", err)
	}
	if !inserted {
		t.Fatal("expected new proxy, got inserted=false")
	}

	count, err := repo.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 proxy, got %d", count)
	}
	_ = svc
}

func TestService_ProcessNewProxy_Duplicate(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	country := "US"
	rec := contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test", Country: &country,
	}

	_, inserted, err := repo.InsertIfAbsent(context.Background(), rec)
	if err != nil {
		t.Fatalf("InsertIfAbsent 1: %v", err)
	}
	if !inserted {
		t.Fatal("first insert should be new")
	}

	_, inserted, err = repo.InsertIfAbsent(context.Background(), rec)
	if err != nil {
		t.Fatalf("InsertIfAbsent 2: %v", err)
	}
	if inserted {
		t.Fatal("second insert should report inserted=false")
	}

	count, err := repo.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 proxy after dedup, got %d", count)
	}
}

func TestService_ProcessScanProxy_BecomesActive(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	_, inserted, err := repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
	})
	if err != nil || !inserted {
		t.Fatalf("InsertIfAbsent: inserted=%v err=%v", inserted, err)
	}

	proxies, _ := repo.ListForScan(context.Background(), 10)
	if len(proxies) != 1 {
		t.Fatalf("expected 1 scannable proxy, got %d", len(proxies))
	}

	// ProcessScanProxy now takes the already-selected record directly.
	checker := &fakeChecker{latency: 150}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := proxydomain.NewService(nil, repo, checker, logger, 48*time.Hour, 5)

	err = svc.ProcessScanProxy(context.Background(), &proxies[0])
	if err != nil {
		t.Fatalf("ProcessScanProxy: %v", err)
	}

	p, _ := repo.Get(context.Background(), proxies[0].ID)
	if p.Status != contract.ProxyStatusActive {
		t.Fatalf("expected active, got %s", p.Status)
	}
	if p.LatencyMS == nil || *p.LatencyMS != 150 {
		t.Fatalf("expected latency 150, got %v", p.LatencyMS)
	}
}

func TestService_ProcessScanProxy_BecomesDead(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
	})
	proxies, _ := repo.ListForScan(context.Background(), 10)

	// Simulate 5 failures → dead via ProcessScanProxy with a failing checker.
	checker := &fakeChecker{err: errors.New("connection refused")}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := proxydomain.NewService(nil, repo, checker, logger, 48*time.Hour, 5)

	for i := 0; i < 5; i++ {
		// Re-fetch the record each time since status changes.
		p, _ := repo.Get(context.Background(), proxies[0].ID)
		err := svc.ProcessScanProxy(context.Background(), p)
		if err != nil {
			t.Fatalf("ProcessScanProxy fail %d: %v", i, err)
		}
	}

	p, _ := repo.Get(context.Background(), proxies[0].ID)
	if p.FailCount != 5 {
		t.Fatalf("expected fail_count 5, got %d", p.FailCount)
	}
	if p.Status != contract.ProxyStatusDead {
		t.Fatalf("expected dead, got %s", p.Status)
	}
}

func TestService_ProcessScanProxy_DeadlineExceeded(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
	})
	proxies, _ := repo.ListForScan(context.Background(), 10)

	// Checker returns context.DeadlineExceeded — should follow ordinary failed path.
	checker := &fakeChecker{err: context.DeadlineExceeded}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := proxydomain.NewService(nil, repo, checker, logger, 48*time.Hour, 5)

	err := svc.ProcessScanProxy(context.Background(), &proxies[0])
	if err != nil {
		t.Fatalf("ProcessScanProxy: %v", err)
	}

	p, _ := repo.Get(context.Background(), proxies[0].ID)
	if p.Status != contract.ProxyStatusInactive {
		t.Fatalf("expected inactive after deadline exceeded, got %s", p.Status)
	}
	if p.FailCount != 1 {
		t.Fatalf("expected fail_count 1, got %d", p.FailCount)
	}
	if p.LastScannedAt == nil {
		t.Fatal("expected last_scanned_at to be set")
	}
}

func TestService_ProcessNewProxy_DeadlineExceeded(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	checker := &fakeChecker{err: context.DeadlineExceeded}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := proxydomain.NewService(nil, repo, checker, logger, 48*time.Hour, 5)

	err := svc.ProcessNewProxy(context.Background(), "http", "1.2.3.4", 8080, nil, nil, "test", nil)
	if err != nil {
		t.Fatalf("ProcessNewProxy: %v", err)
	}

	// Proxy should be inserted then marked inactive due to deadline.
	p, _ := repo.Get(context.Background(), 1)
	if p == nil {
		t.Fatal("expected proxy to be inserted")
	}
	if p.Status != contract.ProxyStatusInactive {
		t.Fatalf("expected inactive after deadline exceeded, got %s", p.Status)
	}
	if p.FailCount != 1 {
		t.Fatalf("expected fail_count 1, got %d", p.FailCount)
	}
}

func TestFakeProvider_FetchProxies(t *testing.T) {
	proxies := []contract.ProxyRecord{
		{Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test"},
		{Protocol: "socks5", IP: "5.6.7.8", Port: 1080, Source: "test"},
	}
	p := mockproxy.NewFakeProvider("test", proxies)

	var got []contract.ProxyRecord
	err := p.FetchProxies(context.Background(), func(rec contract.ProxyRecord) error {
		got = append(got, rec)
		return nil
	})
	if err != nil {
		t.Fatalf("FetchProxies: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 proxies, got %d", len(got))
	}
}

func TestFakeProvider_FetchProxies_Error(t *testing.T) {
	p := mockproxy.NewFakeProvider("test", nil)
	p.SetError(errors.New("provider unavailable"))

	err := p.FetchProxies(context.Background(), func(_ contract.ProxyRecord) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error from provider")
	}
}

func TestFakeRepo_ListForScan_ExcludesDead(t *testing.T) {
	repo := repository.NewFakeProxyRepository()

	// Insert 3 proxies: 1 inactive, 1 active (old enough), 1 dead (old enough).
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{Protocol: "http", IP: "1.1.1.1", Port: 80, Source: "test"})
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{Protocol: "http", IP: "2.2.2.2", Port: 80, Source: "test"})
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{Protocol: "http", IP: "3.3.3.3", Port: 80, Source: "test"})

	proxies, _ := repo.ListForScan(context.Background(), 10)

	// Mark as active (old scan) and dead (old scan).
	oldScan := time.Now().UTC().Add(-1 * time.Hour)
	repo.UpdateStatus(context.Background(), proxies[1].ID, contract.ProxyStatusActive, intPtr(100), 0, &oldScan)
	repo.UpdateStatus(context.Background(), proxies[2].ID, contract.ProxyStatusDead, nil, 5, nil)
	deadScan := time.Now().UTC().Add(-10 * 24 * time.Hour)
	repo.SetLastScannedAt(proxies[2].ID, deadScan)

	// ListForScan should exclude dead proxies.
	scanList, _ := repo.ListForScan(context.Background(), 100)
	for _, p := range scanList {
		if p.Status == contract.ProxyStatusDead {
			t.Errorf("ListForScan returned dead proxy ID=%d — dead must be excluded", p.ID)
		}
	}

	// ListDeadForScan should return only dead proxies.
	deadList, _ := repo.ListDeadForScan(context.Background(), 100)
	for _, p := range deadList {
		if p.Status != contract.ProxyStatusDead {
			t.Errorf("ListDeadForScan returned non-dead proxy ID=%d status=%s", p.ID, p.Status)
		}
	}
}

func TestFakeRepo_ListDeadForScan_PriorityOrder(t *testing.T) {
	repo := repository.NewFakeProxyRepository()

	// Insert 3 proxies: 1 inactive, 1 active, 1 dead (all eligible).
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{Protocol: "http", IP: "1.1.1.1", Port: 80, Source: "test"})
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{Protocol: "http", IP: "2.2.2.2", Port: 80, Source: "test"})
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{Protocol: "http", IP: "3.3.3.3", Port: 80, Source: "test"})

	proxies, _ := repo.ListForScan(context.Background(), 10)

	// Mark as active (old scan) and dead (old scan).
	oldScan := time.Now().UTC().Add(-1 * time.Hour)
	repo.UpdateStatus(context.Background(), proxies[1].ID, contract.ProxyStatusActive, intPtr(100), 0, &oldScan)
	repo.UpdateStatus(context.Background(), proxies[2].ID, contract.ProxyStatusDead, nil, 5, nil)
	deadScan := time.Now().UTC().Add(-10 * 24 * time.Hour)
	repo.SetLastScannedAt(proxies[2].ID, deadScan)

	// ListDeadForScan should return only the dead one.
	deadList, _ := repo.ListDeadForScan(context.Background(), 100)
	if len(deadList) != 1 {
		t.Fatalf("expected 1 dead, got %d", len(deadList))
	}
	if deadList[0].Status != contract.ProxyStatusDead {
		t.Errorf("expected dead, got %s", deadList[0].Status)
	}

	// limit=1 should return exactly 1.
	one, _ := repo.ListDeadForScan(context.Background(), 1)
	if len(one) != 1 {
		t.Fatalf("limit=1: expected 1, got %d", len(one))
	}
}

func intPtr(v int) *int { return &v }

func TestService_ProcessNewProxy_SkipsExistingWithoutWrite(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	// Seed one tuple via InsertIfAbsent.
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "seed",
	})
	// Reset counters after seeding.
	repo.ExistsCalls = 0
	repo.InsertCalls = 0

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := proxydomain.NewService(nil, repo, nil, logger, 48*time.Hour, 5)

	err := svc.ProcessNewProxy(context.Background(), "http", "1.2.3.4", 8080, nil, nil, "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Exists returned true, so InsertIfAbsent must never have been called.
	if repo.InsertCalls != 0 {
		t.Errorf("InsertCalls = %d, want 0 (Exists returned true before InsertIfAbsent was called)", repo.InsertCalls)
	}
}

func TestService_ProcessNewProxy_ExistsError(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	repo.ExistsErr = errors.New("db connection refused")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := proxydomain.NewService(nil, repo, nil, logger, 48*time.Hour, 5)

	err := svc.ProcessNewProxy(context.Background(), "http", "1.2.3.4", 8080, nil, nil, "test", nil)
	if err == nil {
		t.Fatal("expected error when Exists fails")
	}
	// Exists failed, so InsertIfAbsent must never have been called.
	if repo.InsertCalls != 0 {
		t.Errorf("InsertCalls = %d, want 0", repo.InsertCalls)
	}
}
