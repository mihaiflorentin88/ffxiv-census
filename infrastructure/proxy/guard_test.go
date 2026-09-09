package proxy

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

const testGuardBudget = 2 * time.Second

// controlledCheck returns a direct-checker callback that consumes one result
// per control request; cancellation always unblocks it.
func controlledCheck(results <-chan error) func(context.Context) error {
	return func(ctx context.Context) error {
		select {
		case err := <-results:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// waitSnapshot polls until the guard reports want; the state changes only
// after a completed control request, so convergence is deterministic.
func waitSnapshot(t *testing.T, g *EndpointGuard, want contract.GuardSnapshot) {
	t.Helper()
	deadline := time.Now().Add(testGuardBudget)
	for {
		if snap := g.Snapshot(); snap == want {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("guard snapshot = %+v, want %+v", snap, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitCalls polls until the callback counter reaches want.
func awaitCalls(t *testing.T, calls *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(testGuardBudget)
	for calls.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("callback call count = %d, want >= %d", calls.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitRun asserts Run returned nil promptly.
func awaitRun(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(testGuardBudget):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestEndpointGuard_StateTransitions(t *testing.T) {
	results := make(chan error)
	g := NewEndpointGuard(controlledCheck(results), 2*time.Millisecond, nil)

	if snap := g.Snapshot(); snap != (contract.GuardSnapshot{Healthy: false, Generation: 0}) {
		t.Fatalf("initial snapshot = %+v, want unhealthy generation 0", snap)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	results <- nil // first control succeeds
	waitSnapshot(t, g, contract.GuardSnapshot{Healthy: true, Generation: 0})

	results <- errors.New("endpoint control failed") // failure
	waitSnapshot(t, g, contract.GuardSnapshot{Healthy: false, Generation: 1})

	results <- errors.New("endpoint control failed again") // repeated failure
	waitSnapshot(t, g, contract.GuardSnapshot{Healthy: false, Generation: 1})

	results <- nil // recovery
	waitSnapshot(t, g, contract.GuardSnapshot{Healthy: true, Generation: 1})

	cancel()
	awaitRun(t, done)
}

func TestEndpointGuard_StartupControlBeforeFirstInterval(t *testing.T) {
	entered := make(chan struct{}, 1)
	check := func(context.Context) error {
		entered <- struct{}{}
		return nil
	}
	g := NewEndpointGuard(check, time.Hour, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	// An hour-long interval must not delay the startup control request.
	select {
	case <-entered:
	case <-time.After(testGuardBudget):
		cancel()
		t.Fatal("startup control did not run before the first interval")
	}
	waitSnapshot(t, g, contract.GuardSnapshot{Healthy: true, Generation: 0})

	cancel()
	awaitRun(t, done)
}

func TestEndpointGuard_BlocksUntilCancelled(t *testing.T) {
	started := make(chan struct{}, 1)
	check := func(ctx context.Context) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	g := NewEndpointGuard(check, time.Hour, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	select {
	case <-started:
	case <-time.After(testGuardBudget):
		cancel()
		t.Fatal("control callback never started")
	}

	cancel()
	awaitRun(t, done)
}

func TestEndpointGuard_ControlsAreSerial(t *testing.T) {
	var calls, inFlight, maxInFlight atomic.Int64
	check := func(context.Context) error {
		cur := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(100 * time.Microsecond) // widen the overlap window
		inFlight.Add(-1)
		calls.Add(1)
		return nil
	}
	g := NewEndpointGuard(check, 0, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	awaitCalls(t, &calls, 200)
	cancel()
	awaitRun(t, done)

	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("max concurrent control requests = %d, want 1", got)
	}
	if got := inFlight.Load(); got != 0 {
		t.Fatalf("control requests still in flight after Run returned: %d", got)
	}
}

func TestEndpointGuard_RealDirectChecker(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantHealthy bool
	}{
		{"valid json is healthy", http.StatusOK, `{"ip":"203.0.113.9"}`, true},
		{"invalid payload is not a healthy baseline", http.StatusOK, `{"ip":"nope"}`, false},
		{"target error is unhealthy", http.StatusBadGateway, `{"ip":"203.0.113.9"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, targetHits := httpTarget(t, tt.status, tt.body)
			h := NewHealthChecker(target.URL, fixtureBudget, nil)
			var calls atomic.Int64
			g := NewEndpointGuard(func(ctx context.Context) error {
				err := h.CheckDirect(ctx)
				calls.Add(1)
				return err
			}, time.Hour, nil)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- g.Run(ctx) }()

			awaitCalls(t, &calls, 1)
			waitSnapshot(t, g, contract.GuardSnapshot{Healthy: tt.wantHealthy, Generation: 0})
			if got := targetHits.Load(); got != 1 {
				t.Fatalf("target request count = %d, want 1", got)
			}

			cancel()
			awaitRun(t, done)
		})
	}
}

func TestEndpointGuard_JitterBounds(t *testing.T) {
	const interval = time.Second
	for range 1000 {
		wait := jitteredInterval(interval)
		if wait < interval/10*9 || wait > interval/10*11 {
			t.Fatalf("jittered wait = %v, want within [%v, %v]", wait, interval/10*9, interval/10*11)
		}
	}
	if wait := jitteredInterval(0); wait != 0 {
		t.Fatalf("jittered wait for zero interval = %v, want 0", wait)
	}
}
