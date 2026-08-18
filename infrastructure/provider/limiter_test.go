package provider_test

import (
	"context"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/provider"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

func TestRateLimiter_PauseAndAvailability(t *testing.T) {
	limiter := provider.NewRateLimiter()

	if !limiter.IsAvailable(contract.ProviderLodestone) {
		t.Fatal("expected Lodestone to be available initially")
	}
	if !limiter.IsAvailable(contract.ProviderTomestone) {
		t.Fatal("expected Tomestone to be available initially")
	}

	// Pause Lodestone for 100ms
	limiter.Pause(contract.ProviderLodestone, 100*time.Millisecond, "rate limit 429")

	if limiter.IsAvailable(contract.ProviderLodestone) {
		t.Fatal("expected Lodestone to be paused")
	}
	if !limiter.IsAvailable(contract.ProviderTomestone) {
		t.Fatal("expected Tomestone to remain available when Lodestone is paused")
	}

	until, paused := limiter.PausedUntil(contract.ProviderLodestone)
	if !paused || until.IsZero() {
		t.Fatalf("expected PausedUntil to report paused, got %v, %v", until, paused)
	}

	// Wait for unpause
	time.Sleep(120 * time.Millisecond)

	if !limiter.IsAvailable(contract.ProviderLodestone) {
		t.Fatal("expected Lodestone to be available after duration elapsed")
	}
}

func TestRateLimiter_EarliestAvailable(t *testing.T) {
	limiter := provider.NewRateLimiter()

	if !limiter.EarliestAvailable().IsZero() {
		t.Fatal("expected EarliestAvailable to be zero when nothing paused")
	}

	now := time.Now()
	limiter.Pause(contract.ProviderLodestone, 200*time.Millisecond, "reason 1")
	limiter.Pause(contract.ProviderTomestone, 100*time.Millisecond, "reason 2")

	earliest := limiter.EarliestAvailable()
	if earliest.IsZero() {
		t.Fatal("expected non-zero earliest available")
	}

	// Earliest should be close to now + 100ms (Tomestone)
	diff := earliest.Sub(now)
	if diff < 80*time.Millisecond || diff > 150*time.Millisecond {
		t.Fatalf("unexpected earliest diff: %v", diff)
	}
}

func TestRateLimiter_WaitUntilAvailable(t *testing.T) {
	limiter := provider.NewRateLimiter()

	limiter.Pause(contract.ProviderLodestone, 50*time.Millisecond, "test")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := limiter.WaitUntilAvailable(ctx, contract.ProviderLodestone)
	if err != nil {
		t.Fatalf("unexpected error waiting: %v", err)
	}

	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Fatalf("returned too early: %v", elapsed)
	}
}
