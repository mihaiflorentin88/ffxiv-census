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

func TestService_ProcessNewProxy_InsertsDiscoveredProxy(t *testing.T) {
	repo := repository.NewFakeProxyRepository(contract.ProxyScanPolicy{})
	providers := []contract.ProxyProvider{
		mockproxy.NewFakeProvider("test", nil),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := proxydomain.NewService(providers, repo, logger)

	err := svc.ProcessNewProxy(context.Background(), "http", "1.2.3.4", 8080, nil, nil, "test", nil)
	if err != nil {
		t.Fatalf("ProcessNewProxy: %v", err)
	}

	p, err := repo.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p == nil {
		t.Fatal("expected the discovered proxy to be inserted")
	}
	count, err := repo.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 proxy, got %d", count)
	}
}

func TestService_ProcessNewProxy_Duplicate(t *testing.T) {
	repo := repository.NewFakeProxyRepository(contract.ProxyScanPolicy{})
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

// TestService_ProcessNewProxy_InsertionOnly pins the ingestion contract: a
// newly discovered proxy is deduplicated and inserted with no fresh evidence
// and no independent health I/O — the service has no checker dependency at
// all. Verification belongs to the guarded, lease-bound background scan
// worker.
func TestService_ProcessNewProxy_InsertionOnly(t *testing.T) {
	repo := repository.NewFakeProxyRepository(contract.ProxyScanPolicy{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := proxydomain.NewService(nil, repo, logger)

	err := svc.ProcessNewProxy(context.Background(), "http", "1.2.3.4", 8080, nil, nil, "test", nil)
	if err != nil {
		t.Fatalf("ProcessNewProxy: %v", err)
	}

	p, err := repo.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p == nil {
		t.Fatal("expected the discovered proxy to be inserted")
	}
	if p.GeneralHealthy || p.LastVerifiedAt != nil {
		t.Fatalf("newly inserted proxy has fresh evidence: healthy=%v verified_at=%v", p.GeneralHealthy, p.LastVerifiedAt)
	}

	// The consumer claim path must never hand out an unverified row.
	rec, err := repo.ClaimProxy(context.Background(), "owner", time.Minute)
	if err != nil {
		t.Fatalf("ClaimProxy: %v", err)
	}
	if rec != nil {
		t.Fatalf("unverified new proxy was handed to a consumer (id=%d)", rec.ID)
	}

	// Instead it enters background scanning.
	leases, err := repo.ClaimScans(context.Background(), contract.ScanBackground, 10)
	if err != nil {
		t.Fatalf("ClaimScans: %v", err)
	}
	if len(leases) != 1 || leases[0].Record.ID != p.ID {
		t.Fatalf("expected the new proxy to enter the background scan queue, got %d leases", len(leases))
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

func TestService_ProcessNewProxy_SkipsExistingWithoutWrite(t *testing.T) {
	repo := repository.NewFakeProxyRepository(contract.ProxyScanPolicy{})
	// Seed one tuple via InsertIfAbsent.
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol: "http", IP: "1.2.3.4", Port: 8080, Source: "seed",
	})
	// Reset counters after seeding.
	repo.ExistsCalls = 0
	repo.InsertCalls = 0

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := proxydomain.NewService(nil, repo, logger)

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
	repo := repository.NewFakeProxyRepository(contract.ProxyScanPolicy{})
	repo.ExistsErr = errors.New("db connection refused")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := proxydomain.NewService(nil, repo, logger)

	err := svc.ProcessNewProxy(context.Background(), "http", "1.2.3.4", 8080, nil, nil, "test", nil)
	if err == nil {
		t.Fatal("expected error when Exists fails")
	}
	// Exists failed, so InsertIfAbsent must never have been called.
	if repo.InsertCalls != 0 {
		t.Errorf("InsertCalls = %d, want 0", repo.InsertCalls)
	}
}
