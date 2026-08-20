package proxy

import (
	"context"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// ProxyHub manages proxy acquisition for worker goroutines.
// Each goroutine calls NewProxy to atomically claim a proxy from the pool.
type ProxyHub struct {
	repo    contract.ProxyRepository
	lockTTL time.Duration
}

// NewProxyHub creates a ProxyHub with the given repository and lock TTL.
func NewProxyHub(repo contract.ProxyRepository, lockTTL time.Duration) *ProxyHub {
	return &ProxyHub{repo: repo, lockTTL: lockTTL}
}

// NewProxy atomically claims an available proxy for the given owner.
// Returns nil (no error) if no proxy is available.
func (h *ProxyHub) NewProxy(ctx context.Context, owner string) (*Proxy, error) {
	rec, err := h.repo.ClaimProxy(ctx, owner, h.lockTTL)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return New(rec, h.repo), nil
}

// SetLockTime returns the lock TTL configured for this hub.
func (h *ProxyHub) LockTTL() time.Duration {
	return h.lockTTL
}
