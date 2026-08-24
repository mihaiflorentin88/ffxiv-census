package contract

import (
	"context"
	"time"
)

// AchievementRepository persists the milestone registry and characters' earned
// milestones.
type AchievementRepository interface {
	// SyncMilestones idempotently upserts the registry (INSERT OR IGNORE).
	SyncMilestones(ctx context.Context, registry []MilestoneAchievement) error
	// ListMilestones returns the full registry.
	ListMilestones(ctx context.Context) ([]MilestoneAchievement, error)
	// UpsertCharacterMilestones additively inserts or updates earned milestones.
	UpsertCharacterMilestones(ctx context.Context, characterID uint32, milestones []CharacterMilestone) error
	// ListCharacterMilestones returns a character's earned milestones.
	ListCharacterMilestones(ctx context.Context, characterID uint32) ([]CharacterMilestone, error)
	// CountExpansions returns how many distinct characters completed each
	// expansion's MSQ (kind expansion_msq with a non-nil expansion).
	CountExpansions(ctx context.Context) ([]ExpansionCount, error)
	// CountExpansionsFiltered returns how many distinct characters completed each
	// expansion's MSQ, scoped to the provided filter.
	CountExpansionsFiltered(ctx context.Context, filter CharacterFilter) ([]ExpansionCount, error)
	// NewCharactersPerDay returns daily counts of new characters in [since, until),
	// based on the Chocobo milestone (achievement ID 590).
	NewCharactersPerDay(ctx context.Context, since, until time.Time, filter CharacterFilter) ([]DailyCount, error)
	// CountChocoboMilestones returns the count of characters who obtained
	// Milestone 590 at or after since.
	CountChocoboMilestones(ctx context.Context, since time.Time, filter CharacterFilter) (int64, error)
}
