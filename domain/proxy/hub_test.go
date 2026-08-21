package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestProxyHub_NewProxy_Success(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	latency := 50
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol:  "http",
		IP:        "1.2.3.4",
		Port:      8080,
		LatencyMS: &latency,
	})
	// Mark as active.
	repo.UpdateStatus(context.Background(), 1, contract.ProxyStatusActive, &latency, 0, nil)

	hub := NewProxyHub(repo, 5*time.Minute, nil)
	p, err := hub.NewProxy(context.Background(), "test-g1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected proxy, got nil")
	}
	if p.Address() != "http://1.2.3.4:8080" {
		t.Fatalf("expected address http://1.2.3.4:8080, got %s", p.Address())
	}
	if p.LockedBy() == nil || *p.LockedBy() != "test-g1" {
		t.Fatal("expected proxy to be locked by test-g1")
	}
}

func TestProxyHub_NewProxy_NoAvailable(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	hub := NewProxyHub(repo, 5*time.Minute, nil)
	p, err := hub.NewProxy(context.Background(), "test-g1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Fatal("expected nil proxy when none available")
	}
}

func TestProxyHub_NewProxy_AllLocked(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	latency := 50
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol:  "http",
		IP:        "1.2.3.4",
		Port:      8080,
		LatencyMS: &latency,
	})
	// Activate the proxy.
	repo.UpdateStatus(context.Background(), 1, contract.ProxyStatusActive, &latency, 0, nil)
	// Lock it via ClaimProxy with a different owner.
	claimed, err := repo.ClaimProxy(context.Background(), "other-g1", 5*time.Minute)
	if err != nil {
		t.Fatalf("claim setup: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected proxy to be claimed by other-g1")
	}

	hub := NewProxyHub(repo, 5*time.Minute, nil)
	p, err := hub.NewProxy(context.Background(), "test-g1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Fatal("expected nil proxy when all locked")
	}
}

func TestProxyHub_LockTTL(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	ttl := 10 * time.Minute
	hub := NewProxyHub(repo, ttl, nil)
	if hub.LockTTL() != ttl {
		t.Fatalf("expected lock TTL %v, got %v", ttl, hub.LockTTL())
	}
}

func TestProxyHub_RandomActive_ReturnsProxy(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	latency := 50
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol:  "http",
		IP:        "1.2.3.4",
		Port:      8080,
		LatencyMS: &latency,
	})
	repo.UpdateStatus(context.Background(), 1, contract.ProxyStatusActive, &latency, 0, nil)

	hub := NewProxyHub(repo, 5*time.Minute, nil)
	p, err := hub.RandomActive(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected proxy, got nil")
	}
	// RandomActive must NOT lock the proxy.
	if p.LockedBy() != nil {
		t.Fatal("RandomActive must not lock the proxy")
	}
}

func TestProxyHub_RandomActive_NoAvailable(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	hub := NewProxyHub(repo, 5*time.Minute, nil)
	p, err := hub.RandomActive(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Fatal("expected nil when no active proxies")
	}
}

func TestProxyHub_RandomActive_SkipsInactiveAndLocked(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	latency := 50
	// Insert active proxy (eligible).
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol:  "http",
		IP:        "1.2.3.4",
		Port:      8080,
		LatencyMS: &latency,
	})
	repo.UpdateStatus(context.Background(), 1, contract.ProxyStatusActive, &latency, 0, nil)
	// Insert inactive proxy (not eligible).
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol:  "http",
		IP:        "5.6.7.8",
		Port:      8080,
		LatencyMS: &latency,
	})
	// Proxy 2 is inserted as inactive by default.

	hub := NewProxyHub(repo, 5*time.Minute, nil)
	p, err := hub.RandomActive(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected active proxy, got nil")
	}
	if p.Record().ID != 1 {
		t.Fatalf("expected proxy ID 1, got %d", p.Record().ID)
	}
}

func TestProxyHub_SwapActive_DifferentProxy(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	latency := 50
	// Insert two active proxies.
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol:  "http",
		IP:        "1.2.3.4",
		Port:      8080,
		LatencyMS: &latency,
	})
	repo.UpdateStatus(context.Background(), 1, contract.ProxyStatusActive, &latency, 0, nil)
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol:  "http",
		IP:        "5.6.7.8",
		Port:      8080,
		LatencyMS: &latency,
	})
	repo.UpdateStatus(context.Background(), 2, contract.ProxyStatusActive, &latency, 0, nil)

	hub := NewProxyHub(repo, 5*time.Minute, nil)
	// Get initial proxy.
	initial, err := hub.RandomActive(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if initial == nil {
		t.Fatal("expected initial proxy")
	}
	// SwapActive should return a different proxy.
	swapped, err := hub.SwapActive(context.Background(), initial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if swapped == nil {
		t.Fatal("expected swapped proxy")
	}
	if swapped.Record().ID == initial.Record().ID {
		t.Fatalf("expected different proxy, got same ID %d", swapped.Record().ID)
	}
}

func TestProxyHub_SwapActive_NilCurrent(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	latency := 50
	repo.InsertIfAbsent(context.Background(), contract.ProxyRecord{
		Protocol:  "http",
		IP:        "1.2.3.4",
		Port:      8080,
		LatencyMS: &latency,
	})
	repo.UpdateStatus(context.Background(), 1, contract.ProxyStatusActive, &latency, 0, nil)

	hub := NewProxyHub(repo, 5*time.Minute, nil)
	p, err := hub.SwapActive(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected proxy when current is nil")
	}
}
