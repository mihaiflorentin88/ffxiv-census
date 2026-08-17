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
	// SetAchievementsPrivate marks a character's achievement visibility without
	// touching milestones or the latest-achievement fields.
	SetAchievementsPrivate(ctx context.Context, id uint32, private bool) error
	// ListStale returns up to limit characters whose last_census_at is before
	// cutoff (NULL last_census_at counts as stale), ordered oldest-first.
	ListStale(ctx context.Context, cutoff time.Time, limit int) ([]CharacterRecord, error)
	// List returns up to limit non-deleted characters ordered by id, starting
	// at offset (pagination for the REST API).
	List(ctx context.Context, limit, offset int) ([]CharacterRecord, error)
	// Count returns the number of non-deleted characters.
	Count(ctx context.Context) (int64, error)
	// CountActive returns the number of non-deleted characters whose
	// latest_achievement_at is at or after since.
	CountActive(ctx context.Context, since time.Time) (int64, error)
	// Breakdown groups non-deleted characters by column (one of
	// race|world|datacenter|region), with total and active counts per group.
	// Active counts rows whose latest_achievement_at is at or after since.
	// Groups are ordered by total count descending.
	Breakdown(ctx context.Context, column string, since time.Time) ([]GroupCount, error)
	// NewPerDay returns non-deleted characters first seen in [since, until),
	// counted per UTC day, ordered ascending by day.
	NewPerDay(ctx context.Context, since, until time.Time) ([]DailyCount, error)
}
