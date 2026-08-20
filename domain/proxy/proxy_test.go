package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/mock/repository"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestProxy_CanUse_ActiveAndOwned(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	owner := "test-g1"
	now := time.Now().UTC()
	rec := contract.ProxyRecord{
		ID:       1,
		Protocol: "http",
		IP:       "1.2.3.4",
		Port:     8080,
		Status:   contract.ProxyStatusActive,
		LockedBy: &owner,
		LockedAt: &now,
	}
	repo.Upsert(context.Background(), rec)

	p := New(&rec, repo)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected CanUse to return true for active proxy owned by caller")
	}
}

func TestProxy_CanUse_ActiveAndStolen(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	owner := "test-g1"
	other := "test-g2"
	now := time.Now().UTC()
	rec := contract.ProxyRecord{
		ID:       1,
		Protocol: "http",
		IP:       "1.2.3.4",
		Port:     8080,
		Status:   contract.ProxyStatusActive,
		LockedBy: &other,
		LockedAt: &now,
	}
	repo.Upsert(context.Background(), rec)

	p := New(&rec, repo)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected CanUse to return false for proxy owned by another")
	}
}

func TestProxy_CanUse_Inactive(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	owner := "test-g1"
	rec := contract.ProxyRecord{
		ID:       1,
		Protocol: "http",
		IP:       "1.2.3.4",
		Port:     8080,
		Status:   contract.ProxyStatusInactive,
	}
	repo.Upsert(context.Background(), rec)

	p := New(&rec, repo)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected CanUse to return false for inactive proxy")
	}
}

func TestProxy_CanUse_Unlocked(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	owner := "test-g1"
	rec := contract.ProxyRecord{
		ID:       1,
		Protocol: "http",
		IP:       "1.2.3.4",
		Port:     8080,
		Status:   contract.ProxyStatusActive,
	}
	repo.Upsert(context.Background(), rec)

	p := New(&rec, repo)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected CanUse to return false for unlocked proxy")
	}
}

func TestProxy_CanUse_ExpiredLock(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	owner := "test-g1"
	// Lock was acquired 10 minutes ago — exceeds the 5-minute TTL.
	expiredTime := time.Now().UTC().Add(-10 * time.Minute)
	rec := contract.ProxyRecord{
		ID:       1,
		Protocol: "http",
		IP:       "1.2.3.4",
		Port:     8080,
		Status:   contract.ProxyStatusActive,
		LockedBy: &owner,
		LockedAt: &expiredTime,
	}
	repo.Upsert(context.Background(), rec)

	p := New(&rec, repo)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected CanUse to return false for expired lock")
	}
}

func TestProxy_CanUse_ExtendsLock(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	owner := "test-g1"
	// Lock was acquired 4 minutes ago — within the 5-minute TTL but close to expiry.
	oldTime := time.Now().UTC().Add(-4 * time.Minute)
	rec := contract.ProxyRecord{
		ID:       1,
		Protocol: "http",
		IP:       "1.2.3.4",
		Port:     8080,
		Status:   contract.ProxyStatusActive,
		LockedBy: &owner,
		LockedAt: &oldTime,
	}
	repo.Upsert(context.Background(), rec)

	p := New(&rec, repo)
	ok, err := p.CanUse(context.Background(), owner, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected CanUse to return true for proxy within TTL")
	}
	// Verify the lock time was extended — should be close to now, not 4 minutes ago.
	if p.LockedAt() == nil {
		t.Fatal("expected LockedAt to be set after CanUse")
	}
	if time.Since(*p.LockedAt()) > time.Second {
		t.Fatalf("expected lock time to be extended to now, got %v", p.LockedAt())
	}
}

func TestProxy_CanUse_WrongOwner(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	owner := "test-g1"
	other := "test-g2"
	now := time.Now().UTC()
	rec := contract.ProxyRecord{
		ID:       1,
		Protocol: "http",
		IP:       "1.2.3.4",
		Port:     8080,
		Status:   contract.ProxyStatusActive,
		LockedBy: &owner,
		LockedAt: &now,
	}
	repo.Upsert(context.Background(), rec)

	p := New(&rec, repo)
	ok, err := p.CanUse(context.Background(), other, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected CanUse to return false for wrong owner")
	}
}

func TestProxy_Release(t *testing.T) {
	repo := repository.NewFakeProxyRepository()
	owner := "test-g1"
	now := time.Now().UTC()
	rec := contract.ProxyRecord{
		ID:       1,
		Protocol: "http",
		IP:       "1.2.3.4",
		Port:     8080,
		Status:   contract.ProxyStatusActive,
		LockedBy: &owner,
		LockedAt: &now,
	}
	repo.Upsert(context.Background(), rec)

	p := New(&rec, repo)
	if err := p.Release(context.Background(), owner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.LockedBy() != nil {
		t.Fatal("expected LockedBy to be nil after release")
	}
	if p.LockedAt() != nil {
		t.Fatal("expected LockedAt to be nil after release")
	}
}

func TestProxy_Getters(t *testing.T) {
	latency := 100
	rec := contract.ProxyRecord{
		ID:        42,
		Protocol:  "socks5",
		IP:        "10.0.0.1",
		Port:      1080,
		LatencyMS: &latency,
		Status:    contract.ProxyStatusActive,
	}
	p := New(&rec, nil)

	if p.ID() != 42 {
		t.Fatalf("expected ID 42, got %d", p.ID())
	}
	if p.Protocol() != "socks5" {
		t.Fatalf("expected protocol socks5, got %s", p.Protocol())
	}
	if p.IP() != "10.0.0.1" {
		t.Fatalf("expected IP 10.0.0.1, got %s", p.IP())
	}
	if p.Port() != 1080 {
		t.Fatalf("expected port 1080, got %d", p.Port())
	}
	if p.Address() != "socks5://10.0.0.1:1080" {
		t.Fatalf("expected address socks5://10.0.0.1:1080, got %s", p.Address())
	}
	if p.LatencyMS() == nil || *p.LatencyMS() != 100 {
		t.Fatal("expected latency 100")
	}
	if !p.IsActive() {
		t.Fatal("expected IsActive true")
	}
}

func TestProxy_SetLockTime(t *testing.T) {
	rec := contract.ProxyRecord{ID: 1, Protocol: "http", IP: "1.2.3.4", Port: 80}
	p := New(&rec, nil)
	if p.LockedAt() != nil {
		t.Fatal("expected nil LockedAt initially")
	}
	now := time.Now().UTC()
	p.SetLockTime(now)
	if p.LockedAt() == nil || !p.LockedAt().Equal(now) {
		t.Fatal("expected LockedAt to be set")
	}
}
