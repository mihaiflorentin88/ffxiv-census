package repository

import (
	"context"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FakeProxyRepository is an in-memory ProxyRepository for tests.
type FakeProxyRepository struct {
	mu      sync.Mutex
	proxies map[int64]contract.ProxyRecord
	nextID  int64
}

func NewFakeProxyRepository() *FakeProxyRepository {
	return &FakeProxyRepository{proxies: make(map[int64]contract.ProxyRecord)}
}

func (f *FakeProxyRepository) Upsert(_ context.Context, rec contract.ProxyRecord) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.proxies {
		if p.Protocol == rec.Protocol && p.IP == rec.IP && p.Port == rec.Port {
			if rec.Country != nil {
				p.Country = rec.Country
			}
			if rec.Anonymity != nil {
				p.Anonymity = rec.Anonymity
			}
			if rec.UptimePercent != nil {
				p.UptimePercent = rec.UptimePercent
			}
			p.Source = rec.Source
			p.UpdatedAt = time.Now().UTC()
			f.proxies[p.ID] = p
			return p.ID, true, nil
		}
	}
	f.nextID++
	rec.ID = f.nextID
	rec.Status = contract.ProxyStatusInactive
	rec.CreatedAt = time.Now().UTC()
	rec.UpdatedAt = rec.CreatedAt
	f.proxies[rec.ID] = rec
	return rec.ID, false, nil
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
	if len(result) > limit {
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

func latencyOrMax(p contract.ProxyRecord) int {
	if p.LatencyMS == nil {
		return 999999
	}
	return *p.LatencyMS
}
