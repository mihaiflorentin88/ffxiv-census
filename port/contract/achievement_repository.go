package contract

import "context"

// AchievementRepository persists the milestone registry and characters' earned
// milestones.
type AchievementRepository interface {
	// SyncMilestones idempotently upserts the registry (INSERT OR IGNORE).
	SyncMilestones(ctx context.Context, registry []MilestoneAchievement) error
	// ListMilestones returns the full registry.
	ListMilestones(ctx context.Context) ([]MilestoneAchievement, error)
	// UpsertCharacterMilestones replaces a character's earned milestones.
	UpsertCharacterMilestones(ctx context.Context, characterID uint32, milestones []CharacterMilestone) error
	// ListCharacterMilestones returns a character's earned milestones.
	ListCharacterMilestones(ctx context.Context, characterID uint32) ([]CharacterMilestone, error)
}
