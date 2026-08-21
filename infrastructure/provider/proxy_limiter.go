package provider

import (
	"context"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// ProxyRateLimiter is a goroutine-local rate limiter for proxy-mode consumers.
// Each goroutine gets its own limiter instance, independent of the global RateLimiter.
type ProxyRateLimiter struct {
	mu          sync.RWMutex
	pausedUntil map[contract.Provider]time.Time
	reasons     map[contract.Provider]string
}

// NewProxyRateLimiter creates a new goroutine-local rate limiter.
func NewProxyRateLimiter() *ProxyRateLimiter {
	return &ProxyRateLimiter{
		pausedUntil: make(map[contract.Provider]time.Time),
		reasons:     make(map[contract.Provider]string),
	}
}

// IsAvailable returns true if the specified provider is not currently paused.
func (r *ProxyRateLimiter) IsAvailable(p contract.Provider) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	until, exists := r.pausedUntil[p]
	if !exists {
		return true
	}
	return time.Now().After(until)
}

// Pause pauses the provider for the specified duration with a reason.
// If the provider is already paused for longer, the existing pause is preserved.
func (r *ProxyRateLimiter) Pause(p contract.Provider, d time.Duration, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	newUntil := time.Now().Add(d)
	if current := r.pausedUntil[p]; current.After(newUntil) {
		return
	}
	r.pausedUntil[p] = newUntil
	r.reasons[p] = reason
}

// PausedUntil returns the time until the provider is paused and whether it is paused.
func (r *ProxyRateLimiter) PausedUntil(p contract.Provider) (time.Time, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	until, exists := r.pausedUntil[p]
	if !exists {
		return time.Time{}, false
	}
	if time.Now().After(until) {
		return time.Time{}, false
	}
	return until, true
}

// WaitUntilAvailable blocks until the provider is available or ctx is cancelled.
func (r *ProxyRateLimiter) WaitUntilAvailable(ctx context.Context, p contract.Provider) error {
	for {
		if r.IsAvailable(p) {
			return nil
		}
		r.mu.RLock()
		until := r.pausedUntil[p]
		r.mu.RUnlock()
		waitDuration := time.Until(until)
		if waitDuration <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDuration):
		}
	}
}

// EarliestAvailable returns the earliest time any currently paused provider will become available.
// Returns zero time if no provider is paused.
func (r *ProxyRateLimiter) EarliestAvailable() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var earliest time.Time
	now := time.Now()
	for _, until := range r.pausedUntil {
		if until.After(now) && (earliest.IsZero() || until.Before(earliest)) {
			earliest = until
		}
	}
	return earliest
}

// Reset unpauses a provider.
func (r *ProxyRateLimiter) Reset(p contract.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pausedUntil, p)
	delete(r.reasons, p)
}

// Compile-time check that ProxyRateLimiter satisfies ProviderRateLimiter.
var _ contract.ProviderRateLimiter = (*ProxyRateLimiter)(nil)
