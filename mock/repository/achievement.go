package repository

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type AchievementRepository struct {
	mu                         sync.Mutex
	registry                   map[uint32]contract.MilestoneAchievement
	milestones                 map[uint32][]contract.CharacterMilestone
	chars                      *CharacterRepository
	SyncErr                    error
	UpsertErr                  error
	CountExpansionsErr         error
	CountExpansionsFilteredErr error
	NewCharactersPerDayErr     error
	CountChocoboMilestonesErr  error

	// ExpansionsResponse can override CountExpansions / CountExpansionsFiltered responses.
	ExpansionsResponse []contract.ExpansionCount
	// NewCharactersResponse can override NewCharactersPerDay responses.
	NewCharactersResponse []contract.DailyCount
	// ChocoboCountResponse can override CountChocoboMilestones response.
	ChocoboCountResponse int64
}

func NewAchievementFake() *AchievementRepository {
	return &AchievementRepository{
		registry:   map[uint32]contract.MilestoneAchievement{},
		milestones: map[uint32][]contract.CharacterMilestone{},
	}
}

func (f *AchievementRepository) SetCharacterRepo(chars *CharacterRepository) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chars = chars
}

func (f *AchievementRepository) SyncMilestones(ctx context.Context, registry []contract.MilestoneAchievement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SyncErr != nil {
		return f.SyncErr
	}
	for _, m := range registry {
		// Mirror SQL INSERT OR IGNORE: existing rows keep their original values.
		if _, exists := f.registry[m.AchievementID]; !exists {
			f.registry[m.AchievementID] = cloneMilestone(m)
		}
	}
	return nil
}

func (f *AchievementRepository) ListMilestones(ctx context.Context) ([]contract.MilestoneAchievement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]contract.MilestoneAchievement, 0, len(f.registry))
	for _, m := range f.registry {
		out = append(out, cloneMilestone(m))
	}
	return out, nil
}

func (f *AchievementRepository) UpsertCharacterMilestones(ctx context.Context, characterID uint32, milestones []contract.CharacterMilestone) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.UpsertErr != nil {
		return f.UpsertErr
	}
	f.milestones[characterID] = append([]contract.CharacterMilestone(nil), milestones...)
	return nil
}

func (f *AchievementRepository) ListCharacterMilestones(ctx context.Context, characterID uint32) ([]contract.CharacterMilestone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contract.CharacterMilestone(nil), f.milestones[characterID]...), nil
}

// CountExpansions mirrors CountExpansionsFiltered with an empty filter.
func (f *AchievementRepository) CountExpansions(ctx context.Context) ([]contract.ExpansionCount, error) {
	return f.CountExpansionsFiltered(ctx, contract.CharacterFilter{})
}

// CountExpansionsFiltered returns per-expansion counts of distinct characters.
func (f *AchievementRepository) CountExpansionsFiltered(ctx context.Context, filter contract.CharacterFilter) ([]contract.ExpansionCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CountExpansionsFilteredErr != nil {
		return nil, f.CountExpansionsFilteredErr
	}
	if f.CountExpansionsErr != nil {
		return nil, f.CountExpansionsErr
	}
	if f.ExpansionsResponse != nil {
		return f.ExpansionsResponse, nil
	}

	perExpansion := map[string]map[uint32]bool{}
	for characterID, list := range f.milestones {
		for _, m := range list {
			reg, ok := f.registry[m.AchievementID]
			if !ok || reg.Kind != contract.MilestoneKindExpansion || reg.Expansion == nil {
				continue
			}
			seen := perExpansion[*reg.Expansion]
			if seen == nil {
				seen = map[uint32]bool{}
				perExpansion[*reg.Expansion] = seen
			}
			seen[characterID] = true
		}
	}
	out := make([]contract.ExpansionCount, 0, len(perExpansion))
	for expansion, seen := range perExpansion {
		out = append(out, contract.ExpansionCount{Expansion: expansion, Count: int64(len(seen))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Expansion < out[j].Expansion })
	return out, nil
}

func (f *AchievementRepository) NewCharactersPerDay(ctx context.Context, since, until time.Time, filter contract.CharacterFilter) ([]contract.DailyCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.NewCharactersPerDayErr != nil {
		return nil, f.NewCharactersPerDayErr
	}
	if f.NewCharactersResponse != nil {
		return f.NewCharactersResponse, nil
	}
	// Query actual milestone data: characters with achievement 590 in [since, until)
	dayCounts := make(map[string]int64)
	for charID, milestones := range f.milestones {
		for _, m := range milestones {
			if m.AchievementID != 590 {
				continue
			}
			if m.AchievedAt.Before(since) || !m.AchievedAt.Before(until) {
				continue
			}
			if f.chars != nil {
				rec, ok := f.chars.characters[charID]
				if !ok || rec.DeletedAt != nil || !matchesFilter(rec, f.chars.jobs[charID], filter) {
					continue
				}
			}
			day := m.AchievedAt.UTC().Format("2006-01-02")
			dayCounts[day]++
		}
	}
	var out []contract.DailyCount
	for day, count := range dayCounts {
		out = append(out, contract.DailyCount{Day: day, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

func (f *AchievementRepository) CountChocoboMilestones(ctx context.Context, since time.Time, filter contract.CharacterFilter) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CountChocoboMilestonesErr != nil {
		return 0, f.CountChocoboMilestonesErr
	}
	if f.ChocoboCountResponse != 0 {
		return f.ChocoboCountResponse, nil
	}
	// Count characters with chocobo milestone (590) achieved_at >= since
	seen := make(map[uint32]bool)
	for charID, milestones := range f.milestones {
		for _, m := range milestones {
			if m.AchievementID != 590 || m.AchievedAt.Before(since) {
				continue
			}
			if f.chars != nil {
				rec, ok := f.chars.characters[charID]
				if !ok || rec.DeletedAt != nil || !matchesFilter(rec, f.chars.jobs[charID], filter) {
					continue
				}
			}
			seen[charID] = true
		}
	}
	return int64(len(seen)), nil
}

var _ contract.AchievementRepository = (*AchievementRepository)(nil)
