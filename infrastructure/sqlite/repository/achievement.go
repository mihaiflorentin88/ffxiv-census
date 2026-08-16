package repository

import (
	"context"
	"database/sql"
	"fmt"

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
