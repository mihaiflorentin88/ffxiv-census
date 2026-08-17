package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// CharacterRepository is a SQLite implementation of contract.CharacterRepository.
type CharacterRepository struct {
	driver contract.SQLiteDriver
}

func NewCharacterRepository(driver contract.SQLiteDriver) contract.CharacterRepository {
	return &CharacterRepository{driver: driver}
}

const characterColumns = `id, name, world, datacenter, region, race, tribe, gender, grand_company,
		        fc_id, fc_name, achievements_private, latest_achievement_id, latest_achievement_at,
		        first_seen_at, last_census_at, deleted_at`

// Upsert replaces the character row and its jobs in one transaction.
func (r *CharacterRepository) Upsert(ctx context.Context, rec contract.CharacterRecord, jobs []contract.ClassJobRecord) error {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("character upsert begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO characters (
			id, name, world, datacenter, region, race, tribe, gender, grand_company,
			fc_id, fc_name, achievements_private, latest_achievement_id, latest_achievement_at,
			first_seen_at, last_census_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			world = excluded.world,
			datacenter = excluded.datacenter,
			region = excluded.region,
			race = excluded.race,
			tribe = excluded.tribe,
			gender = excluded.gender,
			grand_company = excluded.grand_company,
			fc_id = excluded.fc_id,
			fc_name = excluded.fc_name,
			achievements_private = excluded.achievements_private,
			latest_achievement_id = excluded.latest_achievement_id,
			latest_achievement_at = excluded.latest_achievement_at,
			last_census_at = excluded.last_census_at,
			deleted_at = NULL`,
		rec.ID, rec.Name, rec.World, rec.Datacenter, rec.Region, rec.Race, rec.Tribe,
		rec.Gender, rec.GrandCompany, nullableString(rec.FreeCompanyID), nullableString(rec.FreeCompanyName),
		boolInt(rec.AchievementsPrivate), nullableUint32(rec.LatestAchievementID), nullableTime(rec.LatestAchievementAt),
		formatTime(rec.FirstSeenAt), nullableTime(rec.LastCensusAt), nullableTime(rec.DeletedAt)); err != nil {
		return fmt.Errorf("character upsert: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM character_jobs WHERE character_id = ?`, rec.ID); err != nil {
		return fmt.Errorf("character upsert delete jobs: %w", err)
	}
	for _, j := range jobs {
		// ON CONFLICT guards against colliding class_job_id keys within one
		// payload (godestone reports class and job entries that can map to the
		// same key, e.g. crafters/gatherers where class ID == job ID): the later
		// entry's values win instead of failing the whole upsert.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO character_jobs (character_id, class_job_id, name, level, exp_level)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(character_id, class_job_id) DO UPDATE SET
				name = excluded.name,
				level = excluded.level,
				exp_level = excluded.exp_level`,
			j.CharacterID, j.ClassJobID, j.Name, j.Level, j.ExpLevel); err != nil {
			return fmt.Errorf("character upsert insert job: %w", err)
		}
	}
	return tx.Commit()
}

// Get returns the character or nil when absent.
func (r *CharacterRepository) Get(ctx context.Context, id uint32) (*contract.CharacterRecord, error) {
	row, err := r.driver.FetchOne(ctx,
		`SELECT `+characterColumns+` FROM characters WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	rec, err := scanCharacter(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rec, err
}

// GetJobs returns the character's job levels.
func (r *CharacterRepository) GetJobs(ctx context.Context, id uint32) ([]contract.ClassJobRecord, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT character_id, class_job_id, name, level, exp_level
		   FROM character_jobs WHERE character_id = ? ORDER BY class_job_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []contract.ClassJobRecord
	for rows.Next() {
		var j contract.ClassJobRecord
		if err := rows.Scan(&j.CharacterID, &j.ClassJobID, &j.Name, &j.Level, &j.ExpLevel); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (r *CharacterRepository) MarkDeleted(ctx context.Context, id uint32, at time.Time) error {
	_, err := r.driver.Execute(ctx,
		`UPDATE characters SET deleted_at = ? WHERE id = ?`, formatTime(at), id)
	if err != nil {
		return fmt.Errorf("mark deleted: %w", err)
	}
	return nil
}

func (r *CharacterRepository) UpdateAchievementSummary(ctx context.Context, id uint32, private bool, latestID *uint32, latestAt *time.Time) error {
	_, err := r.driver.Execute(ctx,
		`UPDATE characters
		    SET achievements_private = ?, latest_achievement_id = ?, latest_achievement_at = ?
		  WHERE id = ?`,
		boolInt(private), nullableUint32(latestID), nullableTime(latestAt), id)
	if err != nil {
		return fmt.Errorf("update achievement summary: %w", err)
	}
	return nil
}

func (r *CharacterRepository) SetAchievementsPrivate(ctx context.Context, id uint32, private bool) error {
	_, err := r.driver.Execute(ctx,
		`UPDATE characters SET achievements_private = ? WHERE id = ?`, boolInt(private), id)
	if err != nil {
		return fmt.Errorf("set achievements private: %w", err)
	}
	return nil
}

func (r *CharacterRepository) ListStale(ctx context.Context, cutoff time.Time, limit int) ([]contract.CharacterRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.driver.FetchMany(ctx,
		`SELECT `+characterColumns+`
		   FROM characters
		  WHERE deleted_at IS NULL
		    AND (last_census_at IS NULL OR last_census_at < ?)
		  ORDER BY last_census_at ASC
		  LIMIT ?`, formatTime(cutoff), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.CharacterRecord
	for rows.Next() {
		rec, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// scanCharacter scans one character row into a CharacterRecord.
// race/tribe/grand_company/fc_id/fc_name/latest_achievement_at/last_census_at/
// deleted_at are nullable TEXT scanned into sql.NullString. latest_achievement_id
// is INTEGER scanned into sql.NullInt64.
func scanCharacter(row rowScanner) (*contract.CharacterRecord, error) {
	var rec contract.CharacterRecord
	var gender uint8
	var achievementsPrivate int
	var name, world, datacenter, region string
	var race, tribe, grandCompany, fcID, fcName sql.NullString
	var latestID sql.NullInt64
	var firstSeen string
	var latestAt, lastCensus, deletedAt sql.NullString
	if err := row.Scan(&rec.ID, &name, &world, &datacenter, &region,
		&race, &tribe, &gender, &grandCompany,
		&fcID, &fcName, &achievementsPrivate, &latestID,
		&latestAt, &firstSeen, &lastCensus, &deletedAt); err != nil {
		return nil, err
	}
	rec.Name = name
	rec.World = world
	rec.Datacenter = datacenter
	rec.Region = region
	rec.Race = race.String
	rec.Tribe = tribe.String
	rec.GrandCompany = grandCompany.String
	rec.Gender = gender
	rec.AchievementsPrivate = achievementsPrivate != 0
	rec.FreeCompanyID = sqlStringPtr(fcID)
	rec.FreeCompanyName = sqlStringPtr(fcName)
	rec.LatestAchievementID = sqlUint32Ptr(latestID)
	rec.LatestAchievementAt = sqlTimePtr(latestAt)
	if t, err := parseTime(firstSeen); err == nil {
		rec.FirstSeenAt = t
	}
	rec.LastCensusAt = sqlTimePtr(lastCensus)
	rec.DeletedAt = sqlTimePtr(deletedAt)
	return &rec, nil
}
