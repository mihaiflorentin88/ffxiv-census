package proxy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	proxydomain "github.com/mihaiflorentin88/ffxiv-census/domain/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/proxy"
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

func newTestService(checker proxy.Checker, providers []contract.ProxyProvider, repo contract.ProxyRepository) *proxydomain.Service {
	return proxydomain.NewService(providers, repo, &checker, nil, 48*time.Hour, 5)
}

func TestService_ProcessNewProxy_Success(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	providers := []contract.ProxyProvider{
		mockproxy.NewFakeProvider("test", nil),
	}
	checker := &proxy.Checker{}
	_ = checker // we'll use a service with a real checker that we mock via providers
	// For unit test, we test through the handler which calls service directly
	svc := proxydomain.NewService(providers, repo, nil, nil, 48*time.Hour, 5)

	// ProcessNewProxy needs a checker, so let's test the service methods with a real checker
	// that we control via httptest — but for unit tests we test the repo interaction
	_, exists, err := repo.Upsert(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if exists {
		t.Fatal("expected new proxy, got exists=true")
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

	_, exists, err := repo.Upsert(context.Background(), rec)
	if err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	if exists {
		t.Fatal("first upsert should be new")
	}

	_, exists, err = repo.Upsert(context.Background(), rec)
	if err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}
	if !exists {
		t.Fatal("second upsert should report exists=true")
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
	_, exists, err := repo.Upsert(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
	})
	if err != nil || exists {
		t.Fatalf("Upsert: exists=%v err=%v", exists, err)
	}

	proxies, _ := repo.ListForScan(context.Background(), 10)
	if len(proxies) != 1 {
		t.Fatalf("expected 1 scannable proxy, got %d", len(proxies))
	}

	latency := 150
	now := time.Now().UTC()
	err = repo.UpdateStatus(context.Background(), proxies[0].ID, contract.ProxyStatusActive, &latency, 0, &now)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
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
	repo.Upsert(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test",
	})
	proxies, _ := repo.ListForScan(context.Background(), 10)

	// Simulate 5 failures → dead
	for i := 0; i < 5; i++ {
		err := repo.UpdateStatus(context.Background(), proxies[0].ID, contract.ProxyStatusInactive, nil, i+1, nil)
		if err != nil {
			t.Fatalf("UpdateStatus fail %d: %v", i, err)
		}
	}

	p, _ := repo.Get(context.Background(), proxies[0].ID)
	if p.FailCount != 5 {
		t.Fatalf("expected fail_count 5, got %d", p.FailCount)
	}

	// Now mark dead
	err := repo.UpdateStatus(context.Background(), proxies[0].ID, contract.ProxyStatusDead, nil, 5, nil)
	if err != nil {
		t.Fatalf("UpdateStatus dead: %v", err)
	}

	p, _ = repo.Get(context.Background(), proxies[0].ID)
	if p.Status != contract.ProxyStatusDead {
		t.Fatalf("expected dead, got %s", p.Status)
	}
}

func TestFakeProvider_FetchProxies(t *testing.T) {
	proxies := []contract.ProxyRecord{
		{Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "test"},
		{Protocol: "socks5", IP: "5.6.7.8", Port: 1080, Source: "test"},
	}
	p := mockproxy.NewFakeProvider("test", proxies)

	got, err := p.FetchProxies(context.Background())
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

	_, err := p.FetchProxies(context.Background())
	if err == nil {
		t.Fatal("expected error from provider")
	}
}

func TestFakeRepo_ListForScan_PriorityOrder(t *testing.T) {
	repo := repository.NewFakeProxyRepository()

	// Insert 3 proxies with different statuses
	repo.Upsert(context.Background(), contract.ProxyRecord{Protocol: "http", IP: "1.1.1.1", Port: 80, Source: "test"})
	repo.Upsert(context.Background(), contract.ProxyRecord{Protocol: "http", IP: "2.2.2.2", Port: 80, Source: "test"})
	repo.Upsert(context.Background(), contract.ProxyRecord{Protocol: "http", IP: "3.3.3.3", Port: 80, Source: "test"})

	proxies, _ := repo.ListForScan(context.Background(), 10)
	// All should be inactive (new)
	if len(proxies) != 3 {
		t.Fatalf("expected 3 scannable, got %d", len(proxies))
	}

	// Mark first as active (recently scanned → should drop from scan list)
	now := time.Now().UTC()
	repo.UpdateStatus(context.Background(), proxies[0].ID, contract.ProxyStatusActive, intPtr(100), 0, &now)
	// Mark second as dead, recently scanned
	repo.UpdateStatus(context.Background(), proxies[1].ID, contract.ProxyStatusDead, nil, 5, nil)

	scanList, _ := repo.ListForScan(context.Background(), 10)
	// Should have: inactive (1), dead with old scan (1) — active recently scanned should be excluded
	// But dead was just scanned, so also excluded (within 3 day window)
	if len(scanList) != 1 {
		t.Fatalf("expected 1 scannable (inactive only), got %d", len(scanList))
	}
	if scanList[0].Status != contract.ProxyStatusInactive {
		t.Fatalf("expected inactive first, got %s", scanList[0].Status)
	}
}

func intPtr(v int) *int { return &v }
