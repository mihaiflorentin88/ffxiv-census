package proxy

import (
	"context"
	"time"

	proxyinfra "github.com/mihaiflorentin88/ffxiv-census/infrastructure/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// ProxyHub manages proxy acquisition for worker goroutines.
// Each goroutine calls NewProxy to atomically claim a proxy from the pool.
// Proxies are tested before handout to avoid giving workers dead proxies.
type ProxyHub struct {
	repo    contract.ProxyRepository
	lockTTL time.Duration
	checker *proxyinfra.Checker
}

// NewProxyHub creates a ProxyHub with the given repository, lock TTL, and optional checker.
func NewProxyHub(repo contract.ProxyRepository, lockTTL time.Duration, checker *proxyinfra.Checker) *ProxyHub {
	return &ProxyHub{repo: repo, lockTTL: lockTTL, checker: checker}
}

// NewProxy atomically claims an available proxy for the given owner and tests it.
// Returns nil (no error) if no proxy is available or all claimed proxies fail the check.
// Up to 3 attempts are made to find a working proxy.
func (h *ProxyHub) NewProxy(ctx context.Context, owner string) (*Proxy, error) {
	for range 3 {
		rec, err := h.repo.ClaimProxy(ctx, owner, h.lockTTL)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, nil
		}

		p := New(rec, h.repo)
		if h.checker == nil {
			return p, nil
		}

		_, err = h.checker.Check(ctx, rec.Protocol, rec.IP, rec.Port)
		if err == nil {
			return p, nil
		}

		// Proxy failed check — mark it failed and try another.
		_ = p.MarkFailed(ctx, owner)
	}
	return nil, nil
}

// LockTTL returns the lock TTL configured for this hub.
func (h *ProxyHub) LockTTL() time.Duration {
	return h.lockTTL
}

// RandomActive returns an unlocked random active proxy for public provider scraping.
// This method must not be used for Lodestone APIs; Lodestone workers must use
// NewProxy so proxies are checked and atomically owner-locked.
func (h *ProxyHub) RandomActive(ctx context.Context) (*Proxy, error) {
	rec, err := h.repo.RandomActive(ctx, nil)
	if err != nil || rec == nil {
		return nil, err
	}
	return New(rec, h.repo), nil
}

// RandomActiveExcluding returns an unlocked random active proxy, excluding the
// given IDs. This method must not be used for Lodestone APIs; Lodestone workers
// must use NewProxy so proxies are checked and atomically owner-locked.
func (h *ProxyHub) RandomActiveExcluding(ctx context.Context, excludeIDs []int64) (*Proxy, error) {
	rec, err := h.repo.RandomActive(ctx, excludeIDs)
	if err != nil || rec == nil {
		return nil, err
	}
	return New(rec, h.repo), nil
}

// SwapActive returns an unlocked random active proxy different from current.
// This method must not be used for Lodestone APIs; Lodestone workers must use
// NewProxy so proxies are checked and atomically owner-locked.
func (h *ProxyHub) SwapActive(ctx context.Context, current *Proxy) (*Proxy, error) {
	if current == nil {
		return h.RandomActive(ctx)
	}
	excludeID := current.Record().ID
	rec, err := h.repo.RandomActive(ctx, []int64{excludeID})
	if err != nil || rec == nil {
		return nil, err
	}
	return New(rec, h.repo), nil
}
