package repository

import (
	"context"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FreeCompanyRepository is an in-memory fake with error injection.
type FreeCompanyRepository struct {
	mu        sync.Mutex
	fcs       map[string]contract.FreeCompanyRecord
	UpsertErr error
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
	rec, ok := f.fcs[id]
	if !ok {
		return nil, nil
	}
	cp := cloneFreeCompany(rec)
	return &cp, nil
}

var _ contract.FreeCompanyRepository = (*FreeCompanyRepository)(nil)
