package provider

import (
	"context"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// RateLimiter tracks rate-limiting and pause durations for external data providers.
type RateLimiter struct {
	mu          sync.RWMutex
	pausedUntil map[contract.Provider]time.Time
	reasons     map[contract.Provider]string
}

// NewRateLimiter creates a new RateLimiter instance.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		pausedUntil: make(map[contract.Provider]time.Time),
		reasons:     make(map[contract.Provider]string),
	}
}

// IsAvailable returns true if the specified provider is not currently paused.
func (r *RateLimiter) IsAvailable(p contract.Provider) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	until, exists := r.pausedUntil[p]
	if !exists {
		return true
	}
	return time.Now().After(until)
}

// Pause pauses the provider for the specified duration with a reason.
func (r *RateLimiter) Pause(p contract.Provider, d time.Duration, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	newUntil := time.Now().Add(d)
	// If already paused with a later unpause time, don't shorten it
	if cur, exists := r.pausedUntil[p]; exists && cur.After(newUntil) {
		return
	}

	r.pausedUntil[p] = newUntil
	r.reasons[p] = reason
}

// PausedUntil returns the time until the provider is paused and whether it is paused.
func (r *RateLimiter) PausedUntil(p contract.Provider) (time.Time, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	until, exists := r.pausedUntil[p]
	if !exists || time.Now().After(until) {
		return time.Time{}, false
	}
	return until, true
}

// WaitUntilAvailable blocks until the provider is available or ctx is cancelled.
func (r *RateLimiter) WaitUntilAvailable(ctx context.Context, p contract.Provider) error {
	for {
		r.mu.RLock()
		until, exists := r.pausedUntil[p]
		r.mu.RUnlock()

		if !exists || time.Now().After(until) {
			return nil
		}

		remaining := time.Until(until)
		if remaining <= 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(remaining):
		}
	}
}

// EarliestAvailable returns the earliest time any currently paused provider will become available.
// Returns zero time if no provider is paused.
func (r *RateLimiter) EarliestAvailable() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var earliest time.Time
	now := time.Now()

	for _, until := range r.pausedUntil {
		if until.After(now) {
			if earliest.IsZero() || until.Before(earliest) {
				earliest = until
			}
		}
	}
	return earliest
}

// Reset unpauses a provider.
func (r *RateLimiter) Reset(p contract.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.pausedUntil, p)
	delete(r.reasons, p)
}

var _ contract.ProviderRateLimiter = (*RateLimiter)(nil)
