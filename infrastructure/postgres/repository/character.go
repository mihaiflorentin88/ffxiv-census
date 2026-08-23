package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// CharacterRepository is a PostgreSQL implementation of contract.CharacterRepository.
type CharacterRepository struct {
	driver contract.DatabaseDriver
}

func NewCharacterRepository(driver contract.DatabaseDriver) contract.CharacterRepository {
	return &CharacterRepository{driver: driver}
}

const characterColumns = `id, name, world, datacenter, region, race, tribe, gender, grand_company,
		        fc_id, fc_name, bio, active_job, item_level,
		        achievements_private, latest_achievement_id, latest_achievement_at,
		        first_seen_at, last_census_at, deleted_at`

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

	query := `INSERT INTO characters (
			id, name, world, datacenter, region, race, tribe, gender, grand_company,
			fc_id, fc_name, bio, active_job, item_level,
			achievements_private, latest_achievement_id, latest_achievement_at,
			first_seen_at, last_census_at, deleted_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		ON CONFLICT (id) DO UPDATE SET
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
			bio = excluded.bio,
			active_job = excluded.active_job,
			item_level = excluded.item_level,
			achievements_private = CASE WHEN excluded.achievements_private = 1 THEN 1 ELSE characters.achievements_private END,
			latest_achievement_id = COALESCE(excluded.latest_achievement_id, characters.latest_achievement_id),
			latest_achievement_at = COALESCE(excluded.latest_achievement_at, characters.latest_achievement_at),
			last_census_at = excluded.last_census_at,
			deleted_at = NULL`

	if _, err := tx.ExecContext(ctx, query,
		rec.ID, rec.Name, rec.World, rec.Datacenter, rec.Region, rec.Race, rec.Tribe,
		rec.Gender, rec.GrandCompany, nullableString(rec.FreeCompanyID), nullableString(rec.FreeCompanyName),
		rec.Bio, rec.ActiveJob, rec.ItemLevel,
		boolInt(rec.AchievementsPrivate), nullableUint32(rec.LatestAchievementID), nullableTime(rec.LatestAchievementAt),
		rec.FirstSeenAt, nullableTime(rec.LastCensusAt), nullableTime(rec.DeletedAt)); err != nil {
		return fmt.Errorf("character upsert: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM character_jobs WHERE character_id = $1`, rec.ID); err != nil {
		return fmt.Errorf("character upsert delete jobs: %w", err)
	}
	for _, j := range jobs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO character_jobs (character_id, class_job_id, name, level, exp_level)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (character_id, class_job_id) DO UPDATE SET
				name = excluded.name,
				level = excluded.level,
				exp_level = excluded.exp_level`,
			j.CharacterID, j.ClassJobID, j.Name, j.Level, j.ExpLevel); err != nil {
			return fmt.Errorf("character upsert insert job: %w", err)
		}
	}
	return tx.Commit()
}

func (r *CharacterRepository) UpsertGear(ctx context.Context, charID uint32, gear []contract.CharacterGearRecord) error {
	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("gear upsert begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM character_gear WHERE character_id = $1`, charID); err != nil {
		return fmt.Errorf("gear upsert delete: %w", err)
	}

	now := time.Now().UTC()
	for _, g := range gear {
		upAt := g.UpdatedAt
		if upAt.IsZero() {
			upAt = now
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO character_gear (character_id, slot, item_id, name, item_level, dye, materia, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (character_id, slot) DO UPDATE SET
				item_id = excluded.item_id,
				name = excluded.name,
				item_level = excluded.item_level,
				dye = excluded.dye,
				materia = excluded.materia,
				updated_at = excluded.updated_at`,
			charID, g.Slot, g.ItemID, g.Name, g.ItemLevel, nullableString(g.Dye), strings.Join(g.Materia, ","), upAt); err != nil {
			return fmt.Errorf("gear upsert insert: %w", err)
		}
	}
	return tx.Commit()
}

func (r *CharacterRepository) GetGear(ctx context.Context, id uint32) ([]contract.CharacterGearRecord, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT character_id, slot, item_id, name, item_level, dye, materia, updated_at
		   FROM character_gear WHERE character_id = $1 ORDER BY slot`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contract.CharacterGearRecord
	for rows.Next() {
		var g contract.CharacterGearRecord
		var dye, materia sql.NullString
		var upAt time.Time
		if err := rows.Scan(&g.CharacterID, &g.Slot, &g.ItemID, &g.Name, &g.ItemLevel, &dye, &materia, &upAt); err != nil {
			return nil, fmt.Errorf("gear scan: %w", err)
		}
		g.Dye = sqlStringPtr(dye)
		if materia.Valid && materia.String != "" {
			g.Materia = strings.Split(materia.String, ",")
		} else {
			g.Materia = []string{}
		}
		g.UpdatedAt = upAt
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *CharacterRepository) FindIDGaps(ctx context.Context, maxID uint32, limit int) ([][2]uint32, error) {
	if maxID <= 1 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT c1.id + 1 AS gap_start,
		       (SELECT MIN(c3.id) - 1 FROM characters c3 WHERE c3.id > c1.id) AS gap_end
		  FROM characters c1
		 WHERE NOT EXISTS (SELECT 1 FROM characters c2 WHERE c2.id = c1.id + 1)
		   AND c1.id < $1
		 ORDER BY c1.id ASC
		 LIMIT $2`

	rows, err := r.driver.FetchMany(ctx, query, maxID, limit)
	if err != nil {
		return nil, fmt.Errorf("find id gaps: %w", err)
	}
	defer rows.Close()

	var gaps [][2]uint32
	for rows.Next() {
		var start, end int64
		if err := rows.Scan(&start, &end); err != nil {
			return nil, fmt.Errorf("scan id gap: %w", err)
		}
		if start <= end && start <= int64(maxID) {
			if end > int64(maxID) {
				end = int64(maxID)
			}
			gaps = append(gaps, [2]uint32{uint32(start), uint32(end)})
		}
	}
	return gaps, rows.Err()
}

func (r *CharacterRepository) Get(ctx context.Context, id uint32) (*contract.CharacterRecord, error) {
	row, err := r.driver.FetchOne(ctx,
		fmt.Sprintf(`SELECT %s FROM characters WHERE id = $1`, characterColumns), id)
	if err != nil {
		return nil, err
	}
	rec, err := scanCharacter(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return rec, nil
}

func (r *CharacterRepository) GetJobs(ctx context.Context, id uint32) ([]contract.ClassJobRecord, error) {
	rows, err := r.driver.FetchMany(ctx,
		`SELECT character_id, class_job_id, name, level, exp_level
		   FROM character_jobs WHERE character_id = $1 ORDER BY class_job_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contract.ClassJobRecord
	for rows.Next() {
		var j contract.ClassJobRecord
		if err := rows.Scan(&j.CharacterID, &j.ClassJobID, &j.Name, &j.Level, &j.ExpLevel); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r *CharacterRepository) MarkDeleted(ctx context.Context, id uint32, at time.Time) error {
	_, err := r.driver.Execute(ctx,
		`UPDATE characters SET deleted_at = $1 WHERE id = $2`, at, id)
	if err != nil {
		return fmt.Errorf("character mark deleted: %w", err)
	}
	return nil
}

func (r *CharacterRepository) UpdateAchievementSummary(ctx context.Context, id uint32, private bool, latestID *uint32, latestAt *time.Time) error {
	_, err := r.driver.Execute(ctx,
		`UPDATE characters
		    SET achievements_private = $1,
		        latest_achievement_id = $2,
		        latest_achievement_at = $3
		  WHERE id = $4`,
		boolInt(private), nullableUint32(latestID), nullableTime(latestAt), id)
	if err != nil {
		return fmt.Errorf("character update achievement summary: %w", err)
	}
	return nil
}

func (r *CharacterRepository) SetAchievementsPrivate(ctx context.Context, id uint32, private bool) error {
	_, err := r.driver.Execute(ctx,
		`UPDATE characters SET achievements_private = $1 WHERE id = $2`, boolInt(private), id)
	if err != nil {
		return fmt.Errorf("character set achievements private: %w", err)
	}
	return nil
}

func (r *CharacterRepository) ListStale(ctx context.Context, cutoff time.Time, limit int) ([]contract.CharacterRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if cutoff.IsZero() {
		rows, err = r.driver.FetchMany(ctx,
			fmt.Sprintf(`SELECT %s FROM characters
			              ORDER BY last_census_at ASC NULLS FIRST, id ASC
			              LIMIT $1`, characterColumns), limit)
	} else {
		rows, err = r.driver.FetchMany(ctx,
			fmt.Sprintf(`SELECT %s FROM characters
			              WHERE last_census_at < $1 OR last_census_at IS NULL
			              ORDER BY last_census_at ASC NULLS FIRST, id ASC
			              LIMIT $2`, characterColumns), cutoff, limit)
	}
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

var breakdownColumns = map[string]bool{"race": true, "world": true, "datacenter": true, "region": true}

func characterFilterWhere(f contract.CharacterFilter) (string, []any) {
	return characterFilterWhereWithStart(f, 1)
}

func characterFilterWhereWithStart(f contract.CharacterFilter, startIdx int) (string, []any) {
	var where []string
	var args []any

	addParam := func(clauseTpl string, val any) {
		paramIdx := startIdx + len(args)
		args = append(args, val)
		where = append(where, fmt.Sprintf(clauseTpl, paramIdx))
	}

	if f.World != "" {
		addParam("world = $%d", f.World)
	}
	if f.Datacenter != "" {
		addParam("datacenter = $%d", f.Datacenter)
	}
	if f.Region != "" {
		addParam("region = $%d", f.Region)
	}
	if f.Race != "" {
		addParam("race = $%d", f.Race)
	}
	if f.GrandCompany != "" {
		addParam("grand_company = $%d", f.GrandCompany)
	}
	if f.FreeCompanyID != "" {
		addParam("fc_id = $%d", f.FreeCompanyID)
	}
	if f.Name != "" {
		addParam("name ILIKE $%d", "%"+f.Name+"%")
	}
	if f.Since != nil {
		addParam("latest_achievement_at >= $%d", *f.Since)
	}
	if f.ActiveOnly {
		where = append(where, "deleted_at IS NULL")
	}
	if f.MinLevel > 0 {
		addParam("id IN (SELECT character_id FROM character_jobs WHERE level >= $%d)", f.MinLevel)
	}
	if len(where) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(where, " AND "), args
}

func characterOrderBy(sortBy, sortOrder string) string {
	var col string
	switch strings.ToLower(sortBy) {
	case "name":
		col = "name"
	case "world":
		col = "world"
	case "created_at":
		col = "first_seen_at"
	case "updated_at":
		col = "last_census_at"
	default:
		col = "id"
	}

	var dir string
	if strings.ToLower(sortOrder) == "desc" {
		dir = "DESC"
	} else {
		dir = "ASC"
	}
	return fmt.Sprintf("%s %s", col, dir)
}

func (r *CharacterRepository) List(ctx context.Context, f contract.CharacterFilter, limit, offset int) ([]contract.CharacterRecord, error) {
	where, args := characterFilterWhere(f)
	orderBy := characterOrderBy(f.SortBy, f.SortOrder)

	args = append(args, limit, offset)
	limitPos := len(args) - 1
	offsetPos := len(args)

	query := fmt.Sprintf(`SELECT %s FROM characters WHERE deleted_at IS NULL %s ORDER BY %s LIMIT $%d OFFSET $%d`,
		characterColumns, where, orderBy, limitPos, offsetPos)

	rows, err := r.driver.FetchMany(ctx, query, args...)
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

func (r *CharacterRepository) Stream(ctx context.Context, f contract.CharacterFilter, fn func(rec contract.CharacterRecord) error) error {
	where, args := characterFilterWhere(f)
	query := fmt.Sprintf(`SELECT %s FROM characters WHERE deleted_at IS NULL %s ORDER BY id ASC`,
		characterColumns, where)

	rows, err := r.driver.FetchMany(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
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
	where, args := characterFilterWhere(f)
	query := fmt.Sprintf(`SELECT COUNT(*) FROM characters WHERE deleted_at IS NULL %s`, where)
	row, err := r.driver.FetchOne(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *CharacterRepository) CountActive(ctx context.Context, since time.Time) (int64, error) {
	row, err := r.driver.FetchOne(ctx,
		`SELECT COUNT(*) FROM characters
		  WHERE latest_achievement_at >= $1 AND deleted_at IS NULL`, since)
	if err != nil {
		return 0, err
	}
	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *CharacterRepository) Breakdown(ctx context.Context, column string, since time.Time, f contract.CharacterFilter) ([]contract.GroupCount, error) {
	if !breakdownColumns[column] {
		return nil, fmt.Errorf("invalid breakdown column %q", column)
	}
	filterWhere, filterArgs := characterFilterWhereWithStart(f, 2)

	args := []any{since}
	args = append(args, filterArgs...)
	// In PostgreSQL, COUNT(*) FILTER (WHERE ...) is native and fast
	query := fmt.Sprintf(`SELECT %s AS key,
	                             COUNT(*) AS total,
	                             COUNT(*) FILTER (WHERE latest_achievement_at >= $1) AS active
	                        FROM characters
	                       WHERE deleted_at IS NULL AND %s != '' %s
	                       GROUP BY %s
	                       ORDER BY total DESC`, column, column, filterWhere, column)

	rows, err := r.driver.FetchMany(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contract.GroupCount
	for rows.Next() {
		var g contract.GroupCount
		var key sql.NullString
		if err := rows.Scan(&key, &g.Total, &g.Active); err != nil {
			return nil, err
		}
		if key.Valid {
			g.Key = key.String
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *CharacterRepository) NewPerDay(ctx context.Context, since, until time.Time, f contract.CharacterFilter) ([]contract.DailyCount, error) {
	filterWhere, filterArgs := characterFilterWhereWithStart(f, 3)

	args := []any{since, until}
	args = append(args, filterArgs...)
	query := fmt.Sprintf(`SELECT TO_CHAR(first_seen_at, 'YYYY-MM-DD') AS day,
	                             COUNT(*) AS count
	                        FROM characters
	                       WHERE first_seen_at >= $1 AND first_seen_at < $2
	                         AND deleted_at IS NULL %s
	                       GROUP BY day
	                       ORDER BY day ASC`, filterWhere)

	rows, err := r.driver.FetchMany(ctx, query, args...)
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
	row, err := r.driver.FetchOne(ctx, `SELECT COALESCE(MAX(id), 0) FROM characters`)
	if err != nil {
		return 0, err
	}
	var maxID int64
	if err := row.Scan(&maxID); err != nil {
		return 0, err
	}
	return uint32(maxID), nil
}

func scanCharacter(row rowScanner) (*contract.CharacterRecord, error) {
	var c contract.CharacterRecord
	var race, tribe, grandCompany, fcID, fcName sql.NullString
	var bio, activeJob sql.NullString
	var itemLevel sql.NullInt64
	var achievementsPrivate int
	var latestAchievementID sql.NullInt64
	var latestAchievementAt, lastCensusAt, deletedAt sql.NullTime
	var firstSeenAt time.Time

	err := row.Scan(
		&c.ID, &c.Name, &c.World, &c.Datacenter, &c.Region,
		&race, &tribe, &c.Gender, &grandCompany,
		&fcID, &fcName, &bio, &activeJob, &itemLevel,
		&achievementsPrivate, &latestAchievementID, &latestAchievementAt,
		&firstSeenAt, &lastCensusAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}

	c.Race = race.String
	c.Tribe = tribe.String
	c.GrandCompany = grandCompany.String
	c.FreeCompanyID = sqlStringPtr(fcID)
	c.FreeCompanyName = sqlStringPtr(fcName)
	c.Bio = bio.String
	c.ActiveJob = activeJob.String
	if itemLevel.Valid {
		c.ItemLevel = int(itemLevel.Int64)
	}
	c.AchievementsPrivate = achievementsPrivate == 1
	c.LatestAchievementID = sqlUint32Ptr(latestAchievementID)
	c.LatestAchievementAt = sqlTimePtr(latestAchievementAt)
	c.FirstSeenAt = firstSeenAt
	c.LastCensusAt = sqlTimePtr(lastCensusAt)
	c.DeletedAt = sqlTimePtr(deletedAt)

	return &c, nil
}
