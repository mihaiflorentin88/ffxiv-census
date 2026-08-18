package mock

import (
	"context"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// ProviderRateLimiter is an in-memory mock for contract.ProviderRateLimiter.
type ProviderRateLimiter struct {
	mu          sync.RWMutex
	pausedUntil map[contract.Provider]time.Time
}

// NewProviderRateLimiter creates a new mock ProviderRateLimiter.
func NewProviderRateLimiter() *ProviderRateLimiter {
	return &ProviderRateLimiter{
		pausedUntil: make(map[contract.Provider]time.Time),
	}
}

// IsAvailable returns true if provider is not paused.
func (m *ProviderRateLimiter) IsAvailable(p contract.Provider) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	until, exists := m.pausedUntil[p]
	if !exists {
		return true
	}
	return time.Now().After(until)
}

// Pause pauses the provider for duration d.
func (m *ProviderRateLimiter) Pause(p contract.Provider, d time.Duration, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pausedUntil[p] = time.Now().Add(d)
}

// PausedUntil returns paused until time and whether paused.
func (m *ProviderRateLimiter) PausedUntil(p contract.Provider) (time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	until, exists := m.pausedUntil[p]
	if !exists || time.Now().After(until) {
		return time.Time{}, false
	}
	return until, true
}

// WaitUntilAvailable blocks until provider is available or context cancelled.
func (m *ProviderRateLimiter) WaitUntilAvailable(ctx context.Context, p contract.Provider) error {
	for {
		m.mu.RLock()
		until, exists := m.pausedUntil[p]
		m.mu.RUnlock()

		if !exists || time.Now().After(until) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Until(until)):
		}
	}
}

// EarliestAvailable returns the earliest unpause time.
func (m *ProviderRateLimiter) EarliestAvailable() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var earliest time.Time
	now := time.Now()

	for _, until := range m.pausedUntil {
		if until.After(now) {
			if earliest.IsZero() || until.Before(earliest) {
				earliest = until
			}
		}
	}
	return earliest
}

// Reset resets provider pause state.
func (m *ProviderRateLimiter) Reset(p contract.Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.pausedUntil, p)
}

var _ contract.ProviderRateLimiter = (*ProviderRateLimiter)(nil)
