package repository

import (
	"context"
	"fmt"
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
	ListErr        error
	CountErr       error
	CountActiveErr error
	BreakdownErr   error
	NewPerDayErr   error
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

// List mirrors the SQL: non-deleted rows ordered by id, limited/offset.
func (f *CharacterRepository) List(ctx context.Context, limit, offset int) ([]contract.CharacterRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	var out []contract.CharacterRecord
	for _, rec := range f.characters {
		if rec.DeletedAt != nil {
			continue
		}
		out = append(out, cloneCharacter(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
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

// Count mirrors SELECT COUNT(*) WHERE deleted_at IS NULL.
func (f *CharacterRepository) Count(ctx context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CountErr != nil {
		return 0, f.CountErr
	}
	var n int64
	for _, rec := range f.characters {
		if rec.DeletedAt == nil {
			n++
		}
	}
	return n, nil
}

// CountActive mirrors the SQL: non-deleted rows with latest_achievement_at >= since.
func (f *CharacterRepository) CountActive(ctx context.Context, since time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CountActiveErr != nil {
		return 0, f.CountActiveErr
	}
	var n int64
	for _, rec := range f.characters {
		if rec.DeletedAt != nil || rec.LatestAchievementAt == nil {
			continue
		}
		if !rec.LatestAchievementAt.Before(since) {
			n++
		}
	}
	return n, nil
}

// breakdownColumns mirrors the SQLite repository's whitelist.
var breakdownColumns = map[string]bool{"race": true, "world": true, "datacenter": true, "region": true}

// Breakdown mirrors the SQL group-by: non-deleted rows grouped by the record
// field, total and active counts, ordered by total desc then key.
func (f *CharacterRepository) Breakdown(ctx context.Context, column string, since time.Time) ([]contract.GroupCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.BreakdownErr != nil {
		return nil, f.BreakdownErr
	}
	if !breakdownColumns[column] {
		return nil, fmt.Errorf("invalid breakdown column %q", column)
	}
	counts := map[string]*contract.GroupCount{}
	for _, rec := range f.characters {
		if rec.DeletedAt != nil {
			continue
		}
		var key string
		switch column {
		case "race":
			key = rec.Race
		case "world":
			key = rec.World
		case "datacenter":
			key = rec.Datacenter
		case "region":
			key = rec.Region
		}
		g := counts[key]
		if g == nil {
			g = &contract.GroupCount{Key: key}
			counts[key] = g
		}
		g.Total++
		if rec.LatestAchievementAt != nil && !rec.LatestAchievementAt.Before(since) {
			g.Active++
		}
	}
	out := make([]contract.GroupCount, 0, len(counts))
	for _, g := range counts {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// NewPerDay mirrors the SQL: non-deleted rows with first_seen_at in
// [since, until), counted per UTC day, ordered ascending by day.
func (f *CharacterRepository) NewPerDay(ctx context.Context, since, until time.Time) ([]contract.DailyCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.NewPerDayErr != nil {
		return nil, f.NewPerDayErr
	}
	counts := map[string]int64{}
	for _, rec := range f.characters {
		if rec.DeletedAt != nil {
			continue
		}
		if rec.FirstSeenAt.Before(since) || !rec.FirstSeenAt.Before(until) {
			continue
		}
		counts[rec.FirstSeenAt.UTC().Format("2006-01-02")]++
	}
	out := make([]contract.DailyCount, 0, len(counts))
	for day, c := range counts {
		out = append(out, contract.DailyCount{Day: day, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

var _ contract.CharacterRepository = (*CharacterRepository)(nil)
