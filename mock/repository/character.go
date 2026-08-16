package repository

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// CharacterRepository is an in-memory fake with error injection and call recording.
// It mirrors the SQLite implementation's semantics (first_seen_at preservation,
// deleted_at clearing, jobs replacement, stale ordering) so handler tests don't
// drift from production behavior.
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

func NewCharacterFake() *CharacterRepository {
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
	// Mirror SQL ON CONFLICT semantics: preserve first_seen_at, clear deleted_at.
	if existing, ok := f.characters[rec.ID]; ok {
		rec.FirstSeenAt = existing.FirstSeenAt
	}
	rec.DeletedAt = nil
	f.characters[rec.ID] = cloneCharacter(rec)
	// Always replace the job set (SQL deletes then re-inserts; nil -> empty).
	f.jobs[rec.ID] = append([]contract.ClassJobRecord(nil), jobs...)
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
	cp := cloneCharacter(rec)
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
	rec, ok := f.characters[id]
	if !ok {
		return nil // SQL UPDATE affects 0 rows; no phantom record
	}
	rec.DeletedAt = cloneTime(&at)
	f.characters[id] = rec
	return nil
}

func (f *CharacterRepository) UpdateAchievementSummary(ctx context.Context, id uint32, private bool, latestID *uint32, latestAt *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.UpdateErr != nil {
		return f.UpdateErr
	}
	rec, ok := f.characters[id]
	if !ok {
		return nil // SQL UPDATE affects 0 rows
	}
	rec.AchievementsPrivate = private
	rec.LatestAchievementID = cloneUint32(latestID)
	rec.LatestAchievementAt = cloneTime(latestAt)
	f.characters[id] = rec
	return nil
}

func (f *CharacterRepository) SetAchievementsPrivate(ctx context.Context, id uint32, private bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.characters[id]
	if !ok {
		return nil // SQL UPDATE affects 0 rows
	}
	rec.AchievementsPrivate = private
	f.characters[id] = rec
	return nil
}

func (f *CharacterRepository) ListStale(ctx context.Context, cutoff time.Time, limit int) ([]contract.CharacterRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListStaleErr != nil {
		return nil, f.ListStaleErr
	}
	var stale []contract.CharacterRecord
	for _, rec := range f.characters {
		if rec.DeletedAt != nil {
			continue
		}
		if rec.LastCensusAt == nil || rec.LastCensusAt.Before(cutoff) {
			stale = append(stale, cloneCharacter(rec))
		}
	}
	// Order oldest-first (NULL last_census_at sorts first, like SQLite ASC).
	sort.Slice(stale, func(i, j int) bool {
		li, lj := stale[i].LastCensusAt, stale[j].LastCensusAt
		if li == nil && lj == nil {
			return stale[i].ID < stale[j].ID
		}
		if li == nil {
			return true
		}
		if lj == nil {
			return false
		}
		if li.Equal(*lj) {
			return stale[i].ID < stale[j].ID
		}
		return li.Before(*lj)
	})
	if limit > 0 && len(stale) > limit {
		stale = stale[:limit]
	}
	return stale, nil
}

var _ contract.CharacterRepository = (*CharacterRepository)(nil)
