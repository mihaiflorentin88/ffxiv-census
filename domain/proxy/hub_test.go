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
	repo.Upsert(context.Background(), contract.ProxyRecord{
		Protocol:  "http",
		IP:        "1.2.3.4",
		Port:      8080,
		LatencyMS: &latency,
	})
	// Mark as active.
	repo.UpdateStatus(context.Background(), 1, contract.ProxyStatusActive, &latency, 0, nil)

	hub := NewProxyHub(repo, 5*time.Minute)
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
	hub := NewProxyHub(repo, 5*time.Minute)
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
	repo.Upsert(context.Background(), contract.ProxyRecord{
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

	hub := NewProxyHub(repo, 5*time.Minute)
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
	hub := NewProxyHub(repo, ttl)
	if hub.LockTTL() != ttl {
		t.Fatalf("expected lock TTL %v, got %v", ttl, hub.LockTTL())
	}
}
