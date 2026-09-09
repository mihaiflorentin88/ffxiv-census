package proxy

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// EndpointGuard is the local circuit breaker for scan classification. It
// observes this replica's own egress with a control callback — the bound
// direct checker — so an endpoint outage is attributed to the replica and
// never to proxies. The guard holds no repository reference: a healthy
// control result never changes any proxy state, it only resumes claims.
type EndpointGuard struct {
	check    func(context.Context) error
	interval time.Duration
	logger   contract.Logger

	mu    sync.Mutex
	state contract.GuardSnapshot
}

// Ensure EndpointGuard implements contract.ProxyEndpointGuard at compile time.
var _ contract.ProxyEndpointGuard = (*EndpointGuard)(nil)

// NewEndpointGuard creates a guard that validates local egress with check.
// interval is the pause between control requests; each wait is jittered by
// ±10%.
func NewEndpointGuard(check func(context.Context) error, interval time.Duration, logger contract.Logger) *EndpointGuard {
	if logger == nil {
		logger = discardLogger()
	}
	return &EndpointGuard{
		check:    check,
		interval: interval,
		logger:   logger,
	}
}

// Snapshot returns one consistent guard observation under one lock: health
// and generation are never read independently.
func (g *EndpointGuard) Snapshot() contract.GuardSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// Run issues one control request immediately, then repeats after the
// jittered interval until ctx is cancelled. Controls are serial: at most one
// is in flight and they never overlap. A failed control pauses claims
// (unhealthy) until a later success; the generation increments only on each
// healthy→unhealthy transition, and a later success never resets it. Run
// returns nil once ctx is cancelled.
func (g *EndpointGuard) Run(ctx context.Context) error {
	g.control(ctx)
	for {
		timer := time.NewTimer(jitteredInterval(g.interval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
			g.control(ctx)
		}
	}
}

// control runs one control request and records the outcome under the same
// lock that guards Snapshot. The callback owns request validation and its
// own timeout; the guard only observes error versus nil.
func (g *EndpointGuard) control(ctx context.Context) {
	err := g.check(ctx)
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case err != nil && g.state.Healthy:
		g.state.Generation++
		g.logger.InfoContext(ctx, "endpoint control failed; pausing scan claims",
			"generation", g.state.Generation, "err", err)
	case err != nil:
		g.logger.DebugContext(ctx, "endpoint control still failing",
			"generation", g.state.Generation, "err", err)
	case !g.state.Healthy:
		g.logger.InfoContext(ctx, "endpoint control succeeded; scan claims resume",
			"generation", g.state.Generation)
	default:
		g.logger.DebugContext(ctx, "endpoint control succeeded",
			"generation", g.state.Generation)
	}
	g.state.Healthy = err == nil
}

// jitteredInterval returns interval with ±10% jitter, recomputed for every
// wait. Non-positive intervals are returned unchanged.
func jitteredInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return interval
	}
	span := interval / 10
	if span == 0 {
		return interval
	}
	return interval - span + time.Duration(rand.Int64N(int64(2*span)))
}
