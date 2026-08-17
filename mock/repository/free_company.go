package repository

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FreeCompanyRepository is an in-memory fake with error injection.
type FreeCompanyRepository struct {
	mu        sync.Mutex
	fcs       map[string]contract.FreeCompanyRecord
	UpsertErr error
	GetErr    error
	ListErr   error
	CountErr  error
}

func NewFreeCompanyFake() *FreeCompanyRepository {
	return &FreeCompanyRepository{fcs: map[string]contract.FreeCompanyRecord{}}
}
func (f *FreeCompanyRepository) Upsert(ctx context.Context, rec contract.FreeCompanyRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.UpsertErr != nil {
		return f.UpsertErr
	}
	f.fcs[rec.ID] = cloneFreeCompany(rec)
	return nil
}

func (f *FreeCompanyRepository) Get(ctx context.Context, id string) (*contract.FreeCompanyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.GetErr != nil {
		return nil, f.GetErr
	}
	rec, ok := f.fcs[id]
	if !ok {
		return nil, nil
	}
	cp := cloneFreeCompany(rec)
	return &cp, nil
}

func matchesFCFilter(rec contract.FreeCompanyRecord, filter contract.FreeCompanyFilter) bool {
	if filter.World != "" && rec.World != filter.World {
		return false
	}
	if filter.Datacenter != "" && rec.Datacenter != filter.Datacenter {
		return false
	}
	if filter.Name != "" && !strings.Contains(strings.ToLower(rec.Name), strings.ToLower(filter.Name)) {
		return false
	}
	return true
}

func (f *FreeCompanyRepository) List(ctx context.Context, filter contract.FreeCompanyFilter, limit, offset int) ([]contract.FreeCompanyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	if limit == 0 {
		return nil, nil
	}
	var out []contract.FreeCompanyRecord
	for _, rec := range f.fcs {
		if !matchesFCFilter(rec, filter) {
			continue
		}
		out = append(out, cloneFreeCompany(rec))
	}

	desc := strings.EqualFold(filter.SortOrder, "desc")
	switch strings.ToLower(filter.SortBy) {
	case "name":
		sort.Slice(out, func(i, j int) bool {
			if strings.EqualFold(out[i].Name, out[j].Name) {
				return out[i].ID < out[j].ID
			}
			if desc {
				return strings.ToLower(out[i].Name) > strings.ToLower(out[j].Name)
			}
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
	case "world":
		sort.Slice(out, func(i, j int) bool {
			if out[i].World == out[j].World {
				return out[i].ID < out[j].ID
			}
			if desc {
				return out[i].World > out[j].World
			}
			return out[i].World < out[j].World
		})
	case "formed", "formed_at":
		sort.Slice(out, func(i, j int) bool {
			fi, fj := out[i].FormedAt, out[j].FormedAt
			if fi == nil && fj == nil {
				return out[i].ID < out[j].ID
			}
			if fi == nil {
				return !desc
			}
			if fj == nil {
				return desc
			}
			if fi.Equal(*fj) {
				return out[i].ID < out[j].ID
			}
			if desc {
				return fi.After(*fj)
			}
			return fi.Before(*fj)
		})
	default: // member_count
		sort.Slice(out, func(i, j int) bool {
			if out[i].MemberCount == out[j].MemberCount {
				return out[i].ID < out[j].ID
			}
			if desc {
				return out[i].MemberCount > out[j].MemberCount
			}
			return out[i].MemberCount < out[j].MemberCount
		})
	}

	if offset < 0 {
		offset = 0
	}
	if offset >= len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *FreeCompanyRepository) Count(ctx context.Context, filter contract.FreeCompanyFilter) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CountErr != nil {
		return 0, f.CountErr
	}
	var n int64
	for _, rec := range f.fcs {
		if matchesFCFilter(rec, filter) {
			n++
		}
	}
	return n, nil
}

var _ contract.FreeCompanyRepository = (*FreeCompanyRepository)(nil)
