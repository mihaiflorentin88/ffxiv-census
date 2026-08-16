package contract

import (
	"context"
	"time"
)

// CharacterRepository persists character snapshots and their job levels.
type CharacterRepository interface {
	// Upsert replaces the character row and its jobs in one transaction.
	// first_seen_at is preserved on conflict; deleted_at is cleared.
	Upsert(ctx context.Context, rec CharacterRecord, jobs []ClassJobRecord) error
	// Get returns the character, or nil (no error) if not found.
	Get(ctx context.Context, id uint32) (*CharacterRecord, error)
	// GetJobs returns the character's job levels (empty if none).
	GetJobs(ctx context.Context, id uint32) ([]ClassJobRecord, error)
	// MarkDeleted records that the character no longer exists on Lodestone.
	MarkDeleted(ctx context.Context, id uint32, at time.Time) error
	// UpdateAchievementSummary sets achievements_private, latest_achievement_id,
	// and latest_achievement_at for a character (after an achievement census).
	UpdateAchievementSummary(ctx context.Context, id uint32, private bool, latestID *uint32, latestAt *time.Time) error
	// ListStale returns up to limit characters whose last_census_at is before
	// cutoff (NULL last_census_at counts as stale), ordered oldest-first.
	ListStale(ctx context.Context, cutoff time.Time, limit int) ([]CharacterRecord, error)
}
