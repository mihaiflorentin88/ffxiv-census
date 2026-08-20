package provider

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestProxyRateLimiter_IsAvailable(t *testing.T) {
	r := NewProxyRateLimiter()
	if !r.IsAvailable(contract.ProviderLodestone) {
		t.Fatal("expected provider to be available initially")
	}
}

func TestProxyRateLimiter_Pause(t *testing.T) {
	r := NewProxyRateLimiter()
	r.Pause(contract.ProviderLodestone, 100*time.Millisecond, "test")
	if r.IsAvailable(contract.ProviderLodestone) {
		t.Fatal("expected provider to be paused")
	}
	time.Sleep(150 * time.Millisecond)
	if !r.IsAvailable(contract.ProviderLodestone) {
		t.Fatal("expected provider to be available after pause expires")
	}
}

func TestProxyRateLimiter_WaitUntilAvailable(t *testing.T) {
	r := NewProxyRateLimiter()
	r.Pause(contract.ProviderLodestone, 50*time.Millisecond, "test")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err := r.WaitUntilAvailable(ctx, contract.ProviderLodestone)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Fatal("expected to wait at least 40ms")
	}
}

func TestProxyRateLimiter_WaitUntilAvailable_ContextCancelled(t *testing.T) {
	r := NewProxyRateLimiter()
	r.Pause(contract.ProviderLodestone, 10*time.Second, "test")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := r.WaitUntilAvailable(ctx, contract.ProviderLodestone)
	if err == nil {
		t.Fatal("expected context cancelled error")
	}
}

func TestProxyRateLimiter_Reset(t *testing.T) {
	r := NewProxyRateLimiter()
	r.Pause(contract.ProviderLodestone, 10*time.Second, "test")
	if r.IsAvailable(contract.ProviderLodestone) {
		t.Fatal("expected provider to be paused")
	}
	r.Reset(contract.ProviderLodestone)
	if !r.IsAvailable(contract.ProviderLodestone) {
		t.Fatal("expected provider to be available after reset")
	}
}

func TestProxyRateLimiter_PausedUntil(t *testing.T) {
	r := NewProxyRateLimiter()
	_, paused := r.PausedUntil(contract.ProviderLodestone)
	if paused {
		t.Fatal("expected provider not to be paused initially")
	}

	r.Pause(contract.ProviderLodestone, 1*time.Second, "test")
	until, paused := r.PausedUntil(contract.ProviderLodestone)
	if !paused {
		t.Fatal("expected provider to be paused")
	}
	if until.Before(time.Now()) {
		t.Fatal("expected pause until to be in the future")
	}
}

func TestProxyRateLimiter_EarliestAvailable(t *testing.T) {
	r := NewProxyRateLimiter()
	if !r.EarliestAvailable().IsZero() {
		t.Fatal("expected zero time when no providers paused")
	}

	r.Pause(contract.ProviderLodestone, 1*time.Second, "test")
	r.Pause(contract.ProviderTomestone, 2*time.Second, "test")
	earliest := r.EarliestAvailable()
	if earliest.IsZero() {
		t.Fatal("expected non-zero earliest available")
	}
}
