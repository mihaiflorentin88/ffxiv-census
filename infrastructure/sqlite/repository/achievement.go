package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// AchievementRepository is a SQLite implementation of contract.AchievementRepository.
type AchievementRepository struct {
	driver contract.SQLiteDriver
}

func NewAchievementRepository(driver contract.SQLiteDriver) contract.AchievementRepository {
	return &AchievementRepository{driver: driver}
}

func (r *AchievementRepository) SyncMilestones(ctx context.Context, registry []contract.MilestoneAchievement) error {
	for _, m := range registry {
		_, err := r.driver.Execute(ctx,
			`INSERT OR IGNORE INTO milestone_achievements (achievement_id, kind, expansion, detail)
			 VALUES (?, ?, ?, ?)`,
			m.AchievementID, m.Kind, nullableString(m.Expansion), m.Detail)
		if err != nil {
			return fmt.Errorf("sync milestone %d: %w", m.AchievementID, err)
		}
	}
	return nil
}

func (r *AchievementRepository) ListMilestones(ctx context.Context) ([]contract.MilestoneAchievement, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT achievement_id, kind, expansion, detail FROM milestone_achievements ORDER BY achievement_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.MilestoneAchievement
	for rows.Next() {
		var m contract.MilestoneAchievement
		var expansion sql.NullString
		if err := rows.Scan(&m.AchievementID, &m.Kind, &expansion, &m.Detail); err != nil {
			return nil, err
		}
		m.Expansion = sqlStringPtr(expansion)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *AchievementRepository) UpsertCharacterMilestones(ctx context.Context, characterID uint32, milestones []contract.CharacterMilestone) error {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("milestones begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM character_milestones WHERE character_id = ?`, characterID); err != nil {
		return err
	}
	for _, m := range milestones {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO character_milestones (character_id, achievement_id, achieved_at) VALUES (?, ?, ?)`,
			m.CharacterID, m.AchievementID, formatTime(m.AchievedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *AchievementRepository) ListCharacterMilestones(ctx context.Context, characterID uint32) ([]contract.CharacterMilestone, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT character_id, achievement_id, achieved_at FROM character_milestones
		  WHERE character_id = ? ORDER BY achieved_at DESC`, characterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.CharacterMilestone
	for rows.Next() {
		var m contract.CharacterMilestone
		var achievedAt string
		if err := rows.Scan(&m.CharacterID, &m.AchievementID, &achievedAt); err != nil {
			return nil, err
		}
		if t, err := parseTime(achievedAt); err == nil {
			m.AchievedAt = t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountExpansions returns per-expansion counts of distinct characters who
// completed the expansion MSQ, ordered by expansion name.
func (r *AchievementRepository) CountExpansions(ctx context.Context) ([]contract.ExpansionCount, error) {
	return r.CountExpansionsFiltered(ctx, contract.CharacterFilter{})
}

func (r *AchievementRepository) CountExpansionsFiltered(ctx context.Context, filter contract.CharacterFilter) ([]contract.ExpansionCount, error) {
	conds, args := characterFilterWhere(filter)

	rows, err := r.driver.FetchMany(ctx,
		`SELECT ma.expansion, COUNT(DISTINCT cm.character_id)
		   FROM character_milestones cm
		   JOIN milestone_achievements ma ON ma.achievement_id = cm.achievement_id
		   JOIN characters c ON c.id = cm.character_id
		  WHERE ma.kind = 'expansion_msq' AND ma.expansion IS NOT NULL AND c.deleted_at IS NULL`+conds+`
		  GROUP BY ma.expansion ORDER BY ma.expansion`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.ExpansionCount
	for rows.Next() {
		var e contract.ExpansionCount
		if err := rows.Scan(&e.Expansion, &e.Count); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// NewCharactersPerDay returns daily counts of new characters in [since, until).
// Prioritizes the Chocobo milestone (achievement_id = 590) achieved_at, falling back to first_seen_at.
func (r *AchievementRepository) NewCharactersPerDay(ctx context.Context, since, until time.Time, filter contract.CharacterFilter) ([]contract.DailyCount, error) {
	conds, filterArgs := characterFilterWhere(filter)

	query := `
		WITH character_dates AS (
			SELECT
				c.id,
				COALESCE(
					(SELECT cm.achieved_at FROM character_milestones cm WHERE cm.character_id = c.id AND cm.achievement_id = 590 LIMIT 1),
					c.first_seen_at
				) AS event_time
			FROM characters c
			WHERE c.deleted_at IS NULL` + conds + `
		)
		SELECT substr(event_time, 1, 10) AS day, COUNT(*) AS count
		FROM character_dates
		WHERE event_time >= ? AND event_time < ?
		GROUP BY substr(event_time, 1, 10)
		ORDER BY 1 ASC`

	args := append(filterArgs, formatTime(since), formatTime(until))
	rows, err := r.driver.FetchMany(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("new characters per day: %w", err)
	}
	defer rows.Close()

	var out []contract.DailyCount
	for rows.Next() {
		var d contract.DailyCount
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CountChocoboMilestones returns the count of characters who obtained
// Milestone 590 (or first seen) at or after since.
func (r *AchievementRepository) CountChocoboMilestones(ctx context.Context, since time.Time, filter contract.CharacterFilter) (int64, error) {
	conds, filterArgs := characterFilterWhere(filter)

	query := `
		WITH character_dates AS (
			SELECT
				c.id,
				COALESCE(
					(SELECT cm.achieved_at FROM character_milestones cm WHERE cm.character_id = c.id AND cm.achievement_id = 590 LIMIT 1),
					c.first_seen_at
				) AS event_time
			FROM characters c
			WHERE c.deleted_at IS NULL` + conds + `
		)
		SELECT COUNT(*)
		FROM character_dates
		WHERE event_time >= ?`

	args := append(filterArgs, formatTime(since))
	row, err := r.driver.FetchOne(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("count chocobo milestones: %w", err)
	}

	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
