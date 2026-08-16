package repository

import (
	"context"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// AchievementRepository is an in-memory fake with error injection.
type AchievementRepository struct {
	mu         sync.Mutex
	registry   map[uint32]contract.MilestoneAchievement
	milestones map[uint32][]contract.CharacterMilestone
	SyncErr    error
	UpsertErr  error
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
		f.registry[m.AchievementID] = m
	}
	return nil
}

func (f *AchievementRepository) ListMilestones(ctx context.Context) ([]contract.MilestoneAchievement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]contract.MilestoneAchievement, 0, len(f.registry))
	for _, m := range f.registry {
		out = append(out, m)
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

var _ contract.AchievementRepository = (*AchievementRepository)(nil)
