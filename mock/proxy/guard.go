package proxy

import (
	"context"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FakeEndpointGuard is an in-memory ProxyEndpointGuard for tests. Transitions
// are driven explicitly with SetHealthy instead of a Run loop; the
// generation follows the port's rule of incrementing only on each
// healthy→unhealthy transition.
type FakeEndpointGuard struct {
	mu    sync.Mutex
	state contract.GuardSnapshot
}

// Ensure FakeEndpointGuard implements contract.ProxyEndpointGuard at compile time.
var _ contract.ProxyEndpointGuard = (*FakeEndpointGuard)(nil)

func NewFakeEndpointGuard() *FakeEndpointGuard {
	return &FakeEndpointGuard{}
}

// Snapshot returns one consistent guard observation under one lock.
func (f *FakeEndpointGuard) Snapshot() contract.GuardSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// SetHealthy records the outcome of one control request: an unhealthy
// transition increments the generation, a recovery keeps it.
func (f *FakeEndpointGuard) SetHealthy(healthy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !healthy && f.state.Healthy {
		f.state.Generation++
	}
	f.state.Healthy = healthy
}

// Run maintains the guard until ctx is cancelled.
func (f *FakeEndpointGuard) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
