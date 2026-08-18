package contract

import (
	"context"
	"time"
)

// Provider identifies an external data provider.
type Provider string

const (
	// ProviderLodestone represents the official FFXIV Lodestone.
	ProviderLodestone Provider = "lodestone"
	// ProviderTomestone represents Tomestone.gg.
	ProviderTomestone Provider = "tomestone"
)

// ProviderRateLimiter tracks and controls rate limiting per external provider.
type ProviderRateLimiter interface {
	// IsAvailable returns true if the provider is currently not paused.
	IsAvailable(p Provider) bool

	// Pause pauses the provider for the specified duration with a reason.
	Pause(p Provider, d time.Duration, reason string)

	// PausedUntil returns the time until the provider is paused and whether it is paused.
	PausedUntil(p Provider) (time.Time, bool)

	// WaitUntilAvailable blocks until the provider is available or ctx is cancelled.
	WaitUntilAvailable(ctx context.Context, p Provider) error

	// EarliestAvailable returns the earliest time any currently paused provider will become available.
	// Returns zero time if no provider is paused.
	EarliestAvailable() time.Time

	// Reset unpauses a provider.
	Reset(p Provider)
}
