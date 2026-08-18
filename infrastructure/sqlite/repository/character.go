package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
		        fc_id, fc_name, avatar_url, portrait_url, bio, active_job, item_level,
		        achievements_private, latest_achievement_id, latest_achievement_at,
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
			fc_id, fc_name, avatar_url, portrait_url, bio, active_job, item_level,
			achievements_private, latest_achievement_id, latest_achievement_at,
			first_seen_at, last_census_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			avatar_url = excluded.avatar_url,
			portrait_url = excluded.portrait_url,
			bio = excluded.bio,
			active_job = excluded.active_job,
			item_level = excluded.item_level,
			achievements_private = CASE WHEN excluded.achievements_private = 1 THEN 1 ELSE characters.achievements_private END,
			latest_achievement_id = COALESCE(excluded.latest_achievement_id, characters.latest_achievement_id),
			latest_achievement_at = COALESCE(excluded.latest_achievement_at, characters.latest_achievement_at),
			last_census_at = excluded.last_census_at,
			deleted_at = NULL`,
		rec.ID, rec.Name, rec.World, rec.Datacenter, rec.Region, rec.Race, rec.Tribe,
		rec.Gender, rec.GrandCompany, nullableString(rec.FreeCompanyID), nullableString(rec.FreeCompanyName),
		rec.AvatarURL, rec.PortraitURL, rec.Bio, rec.ActiveJob, rec.ItemLevel,
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

// UpsertGear replaces all gear slots for a character in one transaction.
func (r *CharacterRepository) UpsertGear(ctx context.Context, charID uint32, gear []contract.CharacterGearRecord) error {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("character gear upsert begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM character_gear WHERE character_id = ?`, charID); err != nil {
		return fmt.Errorf("character gear upsert delete: %w", err)
	}
	for _, g := range gear {
		materiaBytes, err := json.Marshal(g.Materia)
		if err != nil {
			return fmt.Errorf("character gear marshal materia: %w", err)
		}
		updatedAt := g.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO character_gear (character_id, slot, item_id, name, item_level, dye, materia, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(character_id, slot) DO UPDATE SET
				item_id = excluded.item_id,
				name = excluded.name,
				item_level = excluded.item_level,
				dye = excluded.dye,
				materia = excluded.materia,
				updated_at = excluded.updated_at`,
			charID, g.Slot, g.ItemID, g.Name, g.ItemLevel, nullableString(g.Dye), string(materiaBytes), formatTime(updatedAt)); err != nil {
			return fmt.Errorf("character gear upsert insert: %w", err)
		}
	}
	return tx.Commit()
}

// GetGear returns the character's equipped gear slots.
func (r *CharacterRepository) GetGear(ctx context.Context, id uint32) ([]contract.CharacterGearRecord, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT character_id, slot, item_id, name, item_level, dye, materia, updated_at
		   FROM character_gear WHERE character_id = ? ORDER BY slot`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var gear []contract.CharacterGearRecord
	for rows.Next() {
		var g contract.CharacterGearRecord
		var dye, materia, updatedAt sql.NullString
		if err := rows.Scan(&g.CharacterID, &g.Slot, &g.ItemID, &g.Name, &g.ItemLevel, &dye, &materia, &updatedAt); err != nil {
			return nil, err
		}
		g.Dye = sqlStringPtr(dye)
		if materia.Valid && materia.String != "" {
			var m []string
			if err := json.Unmarshal([]byte(materia.String), &m); err == nil {
				g.Materia = m
			} else {
				g.Materia = strings.Split(materia.String, ",")
			}
		}
		if updatedAt.Valid {
			if t, err := parseTime(updatedAt.String); err == nil {
				g.UpdatedAt = t
			}
		}
		gear = append(gear, g)
	}
	return gear, rows.Err()
}

// FindIDGaps returns missing/unscanned ID ranges [[start, end], ...] between 1 and maxID.
func (r *CharacterRepository) FindIDGaps(ctx context.Context, maxID uint32, limit int) ([][2]uint32, error) {
	if limit <= 0 || maxID == 0 {
		return nil, nil
	}

	var gaps [][2]uint32

	// Check if there is a gap at the start [1, min_id - 1]
	row, err := r.driver.FetchOne(ctx, `SELECT MIN(id) FROM characters WHERE id <= ? AND deleted_at IS NULL`, maxID)
	if err != nil {
		return nil, err
	}
	var minID sql.NullInt64
	if err := row.Scan(&minID); err != nil {
		return nil, fmt.Errorf("min id scan: %w", err)
	}
	if !minID.Valid {
		return nil, nil
	}
	if minID.Int64 > 1 {
		gaps = append(gaps, [2]uint32{1, uint32(minID.Int64 - 1)})
		if len(gaps) >= limit {
			return gaps, nil
		}
	}

	remainingLimit := limit - len(gaps)
	query := `
		WITH ranked AS (
			SELECT id, LEAD(id) OVER (ORDER BY id) AS next_id
			FROM characters
			WHERE id <= ? AND deleted_at IS NULL
		)
		SELECT id + 1 AS gap_start, next_id - 1 AS gap_end
		FROM ranked
		WHERE next_id IS NOT NULL AND next_id > id + 1
		ORDER BY id ASC
		LIMIT ?`

	rows, err := r.driver.FetchMany(ctx, query, maxID, remainingLimit)
	if err != nil {
		return nil, fmt.Errorf("find id gaps: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var start, end uint32
		if err := rows.Scan(&start, &end); err != nil {
			return nil, fmt.Errorf("scan id gap: %w", err)
		}
		gaps = append(gaps, [2]uint32{start, end})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return gaps, nil
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

// breakdownColumns is the whitelist of GROUP BY columns for Breakdown; the
// column name is interpolated directly into SQL, so anything else is rejected.
var breakdownColumns = map[string]bool{"race": true, "world": true, "datacenter": true, "region": true}

// characterFilterWhere returns the additional " AND ..." conditions for a
// filter, plus their args. Returns "" (and nil args) when the filter is empty.
func characterFilterWhere(f contract.CharacterFilter) (string, []any) {
	var conds []string
	var args []any
	if f.World != "" {
		conds = append(conds, "world = ?")
		args = append(args, f.World)
	}
	if f.Datacenter != "" {
		conds = append(conds, "datacenter = ?")
		args = append(args, f.Datacenter)
	}
	if f.Region != "" {
		conds = append(conds, "region = ?")
		args = append(args, f.Region)
	}
	if f.Race != "" {
		conds = append(conds, "race = ?")
		args = append(args, f.Race)
	}
	if f.Name != "" {
		conds = append(conds, "name LIKE ?")
		args = append(args, "%"+f.Name+"%")
	}
	if f.GrandCompany != "" {
		conds = append(conds, "grand_company = ?")
		args = append(args, f.GrandCompany)
	}
	if f.FreeCompanyID != "" {
		conds = append(conds, "fc_id = ?")
		args = append(args, f.FreeCompanyID)
	}
	if f.ActiveOnly {
		conds = append(conds, "latest_achievement_at IS NOT NULL AND latest_achievement_at != ''")
	}
	if f.MinLevel > 0 {
		conds = append(conds, "EXISTS (SELECT 1 FROM character_jobs cj WHERE cj.character_id = characters.id AND cj.level >= ?)")
		args = append(args, f.MinLevel)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(conds, " AND "), args
}

func characterOrderBy(sortBy, sortOrder string) string {
	order := "ASC"
	if strings.EqualFold(sortOrder, "desc") {
		order = "DESC"
	}

	switch strings.ToLower(sortBy) {
	case "name":
		return " ORDER BY LOWER(name) " + order + ", id ASC"
	case "world":
		return " ORDER BY world " + order + ", id ASC"
	case "created_at", "first_seen_at":
		return " ORDER BY first_seen_at " + order + ", id ASC"
	case "updated_at", "last_census_at":
		return " ORDER BY last_census_at " + order + ", id ASC"
	default:
		return " ORDER BY id " + order
	}
}

func (r *CharacterRepository) List(ctx context.Context, f contract.CharacterFilter, limit, offset int) ([]contract.CharacterRecord, error) {
	conds, args := characterFilterWhere(f)
	orderBy := characterOrderBy(f.SortBy, f.SortOrder)
	q := `SELECT ` + characterColumns + ` FROM characters WHERE deleted_at IS NULL` + conds + orderBy + ` LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := r.driver.FetchMany(ctx, q, args...)
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

// Stream iterates non-deleted characters matching filter ordered by id, invoking fn for each record.
func (r *CharacterRepository) Stream(ctx context.Context, f contract.CharacterFilter, fn func(rec contract.CharacterRecord) error) error {
	conds, args := characterFilterWhere(f)
	q := `SELECT ` + characterColumns + ` FROM characters WHERE deleted_at IS NULL` + conds + ` ORDER BY id`
	rows, err := r.driver.FetchMany(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec, err := scanCharacter(rows)
		if err != nil {
			return err
		}
		if err := fn(*rec); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (r *CharacterRepository) Count(ctx context.Context, f contract.CharacterFilter) (int64, error) {
	conds, args := characterFilterWhere(f)
	row, err := r.driver.FetchOne(ctx, `SELECT COUNT(*) FROM characters WHERE deleted_at IS NULL`+conds, args...)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CountActive returns the number of non-deleted characters whose
// latest_achievement_at is at or after since (TEXT comparison matches the
// fixed-width UTC layout).
func (r *CharacterRepository) CountActive(ctx context.Context, since time.Time) (int64, error) {
	row, err := r.driver.FetchOne(ctx,
		`SELECT COUNT(*) FROM characters WHERE deleted_at IS NULL AND latest_achievement_at >= ?`,
		formatTime(since))
	if err != nil {
		return 0, err
	}
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Breakdown groups non-deleted characters by column with total and active
// counts, ordered by total descending.
func (r *CharacterRepository) Breakdown(ctx context.Context, column string, since time.Time, f contract.CharacterFilter) ([]contract.GroupCount, error) {
	if !breakdownColumns[column] {
		return nil, fmt.Errorf("invalid breakdown column %q", column)
	}
	conds, args := characterFilterWhere(f)
	rows, err := r.driver.FetchMany(ctx,
		`SELECT `+column+`, COUNT(*),
		        SUM(CASE WHEN latest_achievement_at >= ? THEN 1 ELSE 0 END)
		   FROM characters WHERE deleted_at IS NULL`+conds+`
		  GROUP BY `+column+` ORDER BY COUNT(*) DESC`,
		append([]any{formatTime(since)}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.GroupCount
	for rows.Next() {
		var g contract.GroupCount
		if err := rows.Scan(&g.Key, &g.Total, &g.Active); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// NewPerDay returns non-deleted characters first seen in [since, until),
// counted per UTC day, ordered ascending by day.
func (r *CharacterRepository) NewPerDay(ctx context.Context, since, until time.Time, f contract.CharacterFilter) ([]contract.DailyCount, error) {
	conds, args := characterFilterWhere(f)
	rows, err := r.driver.FetchMany(ctx,
		`SELECT substr(first_seen_at, 1, 10), COUNT(*)
		   FROM characters
		  WHERE deleted_at IS NULL AND first_seen_at >= ? AND first_seen_at < ?`+conds+`
		  GROUP BY substr(first_seen_at, 1, 10) ORDER BY 1`,
		append([]any{formatTime(since), formatTime(until)}, args...)...)
	if err != nil {
		return nil, err
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
func (r *CharacterRepository) MaxID(ctx context.Context) (uint32, error) {
	row, err := r.driver.FetchOne(ctx, `SELECT COALESCE(MAX(id), 0) FROM characters WHERE deleted_at IS NULL`)
	if err != nil {
		return 0, err
	}
	var maxID uint32
	if err := row.Scan(&maxID); err != nil {
		return 0, fmt.Errorf("character max id scan: %w", err)
	}
	return maxID, nil
}

// race/tribe/grand_company/fc_id/fc_name/latest_achievement_at/last_census_at/
// deleted_at are nullable TEXT scanned into sql.NullString. latest_achievement_id
// is INTEGER scanned into sql.NullInt64.
func scanCharacter(row rowScanner) (*contract.CharacterRecord, error) {
	var rec contract.CharacterRecord
	var gender uint8
	var achievementsPrivate int
	var name, world, datacenter, region string
	var race, tribe, grandCompany, fcID, fcName sql.NullString
	var avatarURL, portraitURL, bio, activeJob sql.NullString
	var itemLevel sql.NullInt64
	var latestID sql.NullInt64
	var firstSeen string
	var latestAt, lastCensus, deletedAt sql.NullString
	if err := row.Scan(&rec.ID, &name, &world, &datacenter, &region,
		&race, &tribe, &gender, &grandCompany,
		&fcID, &fcName,
		&avatarURL, &portraitURL, &bio, &activeJob, &itemLevel,
		&achievementsPrivate, &latestID,
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
	rec.AvatarURL = avatarURL.String
	rec.PortraitURL = portraitURL.String
	rec.Bio = bio.String
	rec.ActiveJob = activeJob.String
	rec.ItemLevel = int(itemLevel.Int64)
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
