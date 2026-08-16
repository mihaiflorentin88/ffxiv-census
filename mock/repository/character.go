package repository

import (
	"context"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// CharacterRepository is an in-memory fake with error injection and call recording.
type CharacterRepository struct {
	mu             sync.Mutex
	characters     map[uint32]contract.CharacterRecord
	jobs           map[uint32][]contract.ClassJobRecord
	UpsertErr      error
	GetErr         error
	MarkDeletedErr error
	UpdateErr      error
	ListStaleErr   error
	UpsertCalls    int
}

func NewFake() *CharacterRepository {
	return &CharacterRepository{
		characters: map[uint32]contract.CharacterRecord{},
		jobs:       map[uint32][]contract.ClassJobRecord{},
	}
}

func (f *CharacterRepository) Upsert(ctx context.Context, rec contract.CharacterRecord, jobs []contract.ClassJobRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.UpsertCalls++
	if f.UpsertErr != nil {
		return f.UpsertErr
	}
	if rec.FirstSeenAt.IsZero() {
		rec.FirstSeenAt = time.Now().UTC()
	}
	now := time.Now().UTC()
	rec.LastCensusAt = &now
	rec.DeletedAt = nil
	f.characters[rec.ID] = rec
	if jobs != nil {
		f.jobs[rec.ID] = append([]contract.ClassJobRecord(nil), jobs...)
	}
	return nil
}

func (f *CharacterRepository) Get(ctx context.Context, id uint32) (*contract.CharacterRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.GetErr != nil {
		return nil, f.GetErr
	}
	rec, ok := f.characters[id]
	if !ok {
		return nil, nil
	}
	cp := rec
	return &cp, nil
}

func (f *CharacterRepository) GetJobs(ctx context.Context, id uint32) ([]contract.ClassJobRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contract.ClassJobRecord(nil), f.jobs[id]...), nil
}

func (f *CharacterRepository) MarkDeleted(ctx context.Context, id uint32, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.MarkDeletedErr != nil {
		return f.MarkDeletedErr
	}
	rec := f.characters[id]
	rec.DeletedAt = &at
	f.characters[id] = rec
	return nil
}

func (f *CharacterRepository) UpdateAchievementSummary(ctx context.Context, id uint32, private bool, latestID *uint32, latestAt *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.UpdateErr != nil {
		return f.UpdateErr
	}
	rec := f.characters[id]
	rec.AchievementsPrivate = private
	rec.LatestAchievementID = latestID
	rec.LatestAchievementAt = latestAt
	f.characters[id] = rec
	return nil
}

func (f *CharacterRepository) ListStale(ctx context.Context, cutoff time.Time, limit int) ([]contract.CharacterRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListStaleErr != nil {
		return nil, f.ListStaleErr
	}
	var out []contract.CharacterRecord
	for _, rec := range f.characters {
		if rec.DeletedAt != nil {
			continue
		}
		if rec.LastCensusAt == nil || rec.LastCensusAt.Before(cutoff) {
			out = append(out, rec)
		}
	}
	return out, nil
}

var _ contract.CharacterRepository = (*CharacterRepository)(nil)
