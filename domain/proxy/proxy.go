package proxy

import (
	"context"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Proxy wraps a ProxyRecord with domain behavior for lock management.
type Proxy struct {
	record *contract.ProxyRecord
	repo   contract.ProxyRepository
}

// New creates a Proxy domain object from a record and repository.
func New(record *contract.ProxyRecord, repo contract.ProxyRepository) *Proxy {
	return &Proxy{record: record, repo: repo}
}

// ID returns the proxy ID.
func (p *Proxy) ID() int64 { return p.record.ID }

// Protocol returns the proxy protocol (http, https, socks4, socks5).
func (p *Proxy) Protocol() string { return p.record.Protocol }

// IP returns the proxy IP address.
func (p *Proxy) IP() string { return p.record.IP }

// Port returns the proxy port.
func (p *Proxy) Port() int { return p.record.Port }

// Address returns the full proxy address as protocol://ip:port.
func (p *Proxy) Address() string {
	return fmt.Sprintf("%s://%s:%d", p.record.Protocol, p.record.IP, p.record.Port)
}

// LatencyMS returns the proxy latency in milliseconds, or nil if unknown.
func (p *Proxy) LatencyMS() *int { return p.record.LatencyMS }

// Status returns the proxy status.
func (p *Proxy) Status() string { return p.record.Status }

// LockedBy returns the owner holding the lock, or nil if unlocked.
func (p *Proxy) LockedBy() *string { return p.record.LockedBy }

// LockedAt returns the time the lock was acquired, or nil if unlocked.
func (p *Proxy) LockedAt() *time.Time { return p.record.LockedAt }

// Record returns the underlying ProxyRecord.
func (p *Proxy) Record() *contract.ProxyRecord { return p.record }

// IsActive returns true if the proxy status is active.
func (p *Proxy) IsActive() bool {
	return p.record.Status == contract.ProxyStatusActive
}

// CanUse returns true if the proxy is active and locked by the given owner.
func (p *Proxy) CanUse(owner string) bool {
	if p.record.Status != contract.ProxyStatusActive {
		return false
	}
	if p.record.LockedBy == nil {
		return false
	}
	return *p.record.LockedBy == owner
}

// ExtendLock extends the lock TTL for this proxy.
// Returns false if the proxy is not owned by the caller.
func (p *Proxy) ExtendLock(ctx context.Context, owner string, lockTTL time.Duration) (bool, error) {
	ok, err := p.repo.ExtendLock(ctx, p.record.ID, owner, lockTTL)
	if err != nil {
		return false, err
	}
	if ok {
		now := time.Now().UTC()
		p.record.LockedAt = &now
	}
	return ok, nil
}

// SetLockTime updates the in-memory lock time without persisting.
// Used by ProxyHub after acquiring a lock.
func (p *Proxy) SetLockTime(t time.Time) {
	p.record.LockedAt = &t
}

// Release releases the lock on this proxy.
func (p *Proxy) Release(ctx context.Context, owner string) error {
	if err := p.repo.ReleaseProxy(ctx, p.record.ID, owner); err != nil {
		return err
	}
	p.record.LockedBy = nil
	p.record.LockedAt = nil
	return nil
}

// MarkFailed releases the lock and increments the fail count.
// This prevents the proxy from being immediately re-acquired by another worker.
func (p *Proxy) MarkFailed(ctx context.Context, owner string) error {
	// Release the lock first.
	if err := p.Release(ctx, owner); err != nil {
		return err
	}
	// Increment fail count and set to inactive so it's not immediately re-selected.
	newFailCount := p.record.FailCount + 1
	if err := p.repo.UpdateStatus(ctx, p.record.ID, contract.ProxyStatusInactive, nil, newFailCount, p.record.LastAliveAt); err != nil {
		return err
	}
	p.record.FailCount = newFailCount
	p.record.Status = contract.ProxyStatusInactive
	return nil
}
