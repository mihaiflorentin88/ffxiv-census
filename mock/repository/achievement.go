package repository

import (
	"context"
	"sort"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// AchievementRepository is an in-memory fake with error injection.
type AchievementRepository struct {
	mu                 sync.Mutex
	registry           map[uint32]contract.MilestoneAchievement
	milestones         map[uint32][]contract.CharacterMilestone
	SyncErr            error
	UpsertErr          error
	CountExpansionsErr error
}

func NewAchievementFake() *AchievementRepository {
	return &AchievementRepository{
		registry:   map[uint32]contract.MilestoneAchievement{},
		milestones: map[uint32][]contract.CharacterMilestone{},
	}
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

// CountExpansions mirrors the SQL join: for each earned milestone whose
// registry entry is kind expansion_msq with a non-nil expansion, count distinct
// characters per expansion, ordered by expansion name.
func (f *AchievementRepository) CountExpansions(ctx context.Context) ([]contract.ExpansionCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CountExpansionsErr != nil {
		return nil, f.CountExpansionsErr
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

var _ contract.AchievementRepository = (*AchievementRepository)(nil)
