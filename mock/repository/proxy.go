package repository

import (
	"context"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FakeProxyRepository is an in-memory ProxyRepository for tests.
type FakeProxyRepository struct {
	mu      sync.Mutex
	proxies map[int64]contract.ProxyRecord
	nextID  int64
	// Rand is an optional injectable RNG for deterministic RandomActive selection.
	// When nil, a default PCG source is used.
	Rand *rand.Rand
	// ExistsErr is returned by Exists when set.
	ExistsErr error
	// InsertErr is returned by InsertIfAbsent when set.
	InsertErr error
	// ExistsCalls counts how many times Exists was called.
	ExistsCalls int
	// InsertCalls counts how many times InsertIfAbsent was called.
	InsertCalls int
}

func NewFakeProxyRepository() *FakeProxyRepository {
	return &FakeProxyRepository{proxies: make(map[int64]contract.ProxyRecord)}
}

func (f *FakeProxyRepository) Exists(_ context.Context, protocol, ip string, port int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ExistsCalls++
	if f.ExistsErr != nil {
		return false, f.ExistsErr
	}
	for _, p := range f.proxies {
		if p.Protocol == protocol && p.IP == ip && p.Port == port {
			return true, nil
		}
	}
	return false, nil
}

func (f *FakeProxyRepository) InsertIfAbsent(_ context.Context, rec contract.ProxyRecord) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.InsertCalls++
	if f.InsertErr != nil {
		return 0, false, f.InsertErr
	}
	for _, p := range f.proxies {
		if p.Protocol == rec.Protocol && p.IP == rec.IP && p.Port == rec.Port {
			return 0, false, nil
		}
	}
	f.nextID++
	rec.ID = f.nextID
	rec.Status = contract.ProxyStatusInactive
	rec.FailCount = 0
	now := time.Now().UTC()
	rec.CreatedAt = now
	rec.UpdatedAt = now
	if rec.FirstSeenAt.IsZero() {
		rec.FirstSeenAt = now
	}
	f.proxies[rec.ID] = rec
	return rec.ID, true, nil
}

func (f *FakeProxyRepository) Get(_ context.Context, id int64) (*contract.ProxyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.proxies[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (f *FakeProxyRepository) UpdateStatus(_ context.Context, id int64, status string, latencyMS *int, failCount int, lastAliveAt *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.proxies[id]
	if !ok {
		return nil
	}
	p.Status = status
	p.LatencyMS = latencyMS
	p.FailCount = failCount
	p.LastAliveAt = lastAliveAt
	now := time.Now().UTC()
	p.LastScannedAt = &now
	p.UpdatedAt = now
	f.proxies[id] = p
	return nil
}

func (f *FakeProxyRepository) UpdateScanTime(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.proxies[id]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	p.LastScannedAt = &now
	p.UpdatedAt = now
	f.proxies[id] = p
	return nil
}

func (f *FakeProxyRepository) ListForScan(_ context.Context, limit int) ([]contract.ProxyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	var result []contract.ProxyRecord
	for _, p := range f.proxies {
		switch p.Status {
		case contract.ProxyStatusInactive:
			result = append(result, p)
		case contract.ProxyStatusActive:
			if p.LastScannedAt == nil || p.LastScannedAt.Before(now.Add(-10*time.Minute)) {
				result = append(result, p)
			}
		case contract.ProxyStatusDead:
			if p.LastScannedAt == nil || p.LastScannedAt.Before(now.Add(-3*24*time.Hour)) {
				result = append(result, p)
			}
		}
	}
	// Sort: inactive first, then active, then dead. Within each group, oldest scan first.
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if priority(result[j]) < priority(result[i]) ||
				(priority(result[j]) == priority(result[i]) && scannedBefore(result[j], result[i])) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (f *FakeProxyRepository) ListActive(_ context.Context, limit int) ([]contract.ProxyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []contract.ProxyRecord
	for _, p := range f.proxies {
		if p.Status == contract.ProxyStatusActive {
			result = append(result, p)
		}
	}
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			li := latencyOrMax(result[i])
			lj := latencyOrMax(result[j])
			if lj < li {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (f *FakeProxyRepository) Count(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.proxies)), nil
}

func (f *FakeProxyRepository) CountByStatus(_ context.Context) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := make(map[string]int64)
	for _, p := range f.proxies {
		counts[p.Status]++
	}
	return counts, nil
}

func (f *FakeProxyRepository) ClaimProxy(_ context.Context, owner string, lockTTL time.Duration) (*contract.ProxyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	expireThreshold := now.Add(-lockTTL)

	// Find best available proxy: prefer recently alive (last hour), then highest uptime, then lowest latency.
	recentThreshold := now.Add(-1 * time.Hour)
	var best *contract.ProxyRecord
	for _, p := range f.proxies {
		if p.Status != contract.ProxyStatusActive {
			continue
		}
		if p.Protocol != "http" && p.Protocol != "https" && p.Protocol != "socks4" && p.Protocol != "socks5" {
			continue
		}
		if p.LockedAt != nil && p.LockedAt.After(expireThreshold) {
			continue
		}
		if best == nil {
			best = &p
			continue
		}
		// Prefer recently alive
		pRecent := p.LastAliveAt != nil && p.LastAliveAt.After(recentThreshold)
		bRecent := best.LastAliveAt != nil && best.LastAliveAt.After(recentThreshold)
		if pRecent && !bRecent {
			best = &p
			continue
		}
		if !pRecent && bRecent {
			continue
		}
		// Both recent or both stale: prefer higher uptime
		pUptime := uptimeOrZero(p)
		bUptime := uptimeOrZero(*best)
		if pUptime > bUptime {
			best = &p
			continue
		}
		if pUptime < bUptime {
			continue
		}
		// Same uptime: prefer lower latency
		if latencyOrMax(p) < latencyOrMax(*best) {
			best = &p
			continue
		}
		// Same latency: prefer lower fail count
		if latencyOrMax(p) == latencyOrMax(*best) && p.FailCount < best.FailCount {
			best = &p
		}
	}
	if best == nil {
		return nil, nil
	}
	best.LockedBy = &owner
	best.LockedAt = &now
	best.UpdatedAt = now
	f.proxies[best.ID] = *best
	return best, nil
}

func (f *FakeProxyRepository) ExtendLock(_ context.Context, id int64, owner string, lockTTL time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.proxies[id]
	if !ok {
		return false, nil
	}
	if p.LockedBy == nil || *p.LockedBy != owner {
		return false, nil
	}
	now := time.Now().UTC()
	expireThreshold := now.Add(-lockTTL)
	if p.LockedAt != nil && p.LockedAt.Before(expireThreshold) {
		return false, nil // lock expired
	}
	p.LockedAt = &now
	p.UpdatedAt = now
	f.proxies[id] = p
	return true, nil
}

func (f *FakeProxyRepository) ReleaseProxy(_ context.Context, id int64, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.proxies[id]
	if !ok {
		return nil
	}
	if p.LockedBy == nil || *p.LockedBy != owner {
		return nil
	}
	p.LockedBy = nil
	p.LockedAt = nil
	p.UpdatedAt = time.Now().UTC()
	f.proxies[id] = p
	return nil
}

func (f *FakeProxyRepository) MarkFailedProxy(_ context.Context, id int64, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.proxies[id]
	if !ok {
		return nil
	}
	if p.LockedBy == nil || *p.LockedBy != owner {
		return nil
	}
	p.LockedBy = nil
	p.LockedAt = nil
	p.Status = contract.ProxyStatusInactive
	p.FailCount++
	p.UpdatedAt = time.Now().UTC()
	f.proxies[id] = p
	return nil
}

func (f *FakeProxyRepository) RandomActive(_ context.Context, excludeIDs []int64) (*contract.ProxyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	excludeSet := make(map[int64]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		excludeSet[id] = true
	}
	var eligible []contract.ProxyRecord
	for _, p := range f.proxies {
		if p.Status != contract.ProxyStatusActive {
			continue
		}
		if p.Protocol != "http" && p.Protocol != "https" && p.Protocol != "socks4" && p.Protocol != "socks5" {
			continue
		}
		if excludeSet[p.ID] {
			continue
		}
		eligible = append(eligible, p)
	}
	if len(eligible) == 0 {
		return nil, nil
	}
	// Sort by ID for deterministic iteration order before random selection.
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].ID < eligible[j].ID })
	r := f.Rand
	if r == nil {
		r = rand.New(rand.NewPCG(1, 2))
	}
	idx := r.IntN(len(eligible))
	rec := eligible[idx]
	return &rec, nil
}

func priority(p contract.ProxyRecord) int {
	switch p.Status {
	case contract.ProxyStatusInactive:
		return 0
	case contract.ProxyStatusActive:
		return 1
	case contract.ProxyStatusDead:
		return 2
	default:
		return 3
	}
}

func scannedBefore(a, b contract.ProxyRecord) bool {
	if a.LastScannedAt == nil && b.LastScannedAt == nil {
		return false
	}
	if a.LastScannedAt == nil {
		return true
	}
	if b.LastScannedAt == nil {
		return false
	}
	return a.LastScannedAt.Before(*b.LastScannedAt)
}

func uptimeOrZero(p contract.ProxyRecord) float64 {
	if p.UptimePercent != nil {
		return *p.UptimePercent
	}
	return 0
}

func latencyOrMax(p contract.ProxyRecord) int {
	if p.LatencyMS == nil {
		return 999999
	}
	return *p.LatencyMS
}
