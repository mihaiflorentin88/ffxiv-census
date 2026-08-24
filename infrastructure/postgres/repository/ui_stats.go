package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

const uiStatsAdvisoryLockID int64 = 0x4646584956554953

type UIStatsRepository struct {
	driver contract.DatabaseDriver
}

func NewUIStatsRepository(driver contract.DatabaseDriver) contract.UIStatsRepository {
	return &UIStatsRepository{driver: driver}
}

func (r *UIStatsRepository) LoadCurrent(ctx context.Context) (*contract.UIStatsSnapshot, error) {
	row, err := r.driver.FetchOne(ctx, `
		SELECT schema_version, generated_at, activity_since, max_level,
		       source_character_count, refresh_duration_ms, payload
		FROM ui_stats_snapshots
		WHERE snapshot_key = 'current'`)
	if err != nil {
		return nil, err
	}
	var (
		schemaVersion                       int
		generatedAt, activitySince          time.Time
		maxLevel                            uint32
		sourceCharacters, refreshDurationMS int64
		payload                             []byte
	)
	if err := row.Scan(&schemaVersion, &generatedAt, &activitySince, &maxLevel, &sourceCharacters, &refreshDurationMS, &payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, census.ErrUIStatsUnavailable
		}
		return nil, err
	}
	var snapshot contract.UIStatsSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, fmt.Errorf("decode UI statistics snapshot: %w", err)
	}
	snapshot.SchemaVersion = schemaVersion
	snapshot.GeneratedAt = generatedAt.UTC()
	snapshot.ActivitySince = activitySince.UTC()
	snapshot.MaxLevel = maxLevel
	snapshot.SourceCharacters = sourceCharacters
	snapshot.RefreshDuration = time.Duration(refreshDurationMS) * time.Millisecond
	if err := census.ValidateUIStatsSnapshot(&snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r *UIStatsRepository) Refresh(ctx context.Context, opts contract.UIStatsRefreshOptions) (*contract.UIStatsRefreshResult, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	if opts.ActivitySince.IsZero() {
		return nil, errors.New("UI statistics activity cutoff is required")
	}
	if opts.MaxLevel == 0 {
		return nil, errors.New("UI statistics max level is required")
	}

	db, err := r.driver.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, uiStatsAdvisoryLockID).Scan(&locked); err != nil {
		return nil, fmt.Errorf("acquire UI statistics refresh lock: %w", err)
	}
	if !locked {
		return &contract.UIStatsRefreshResult{Skipped: true}, nil
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, uiStatsAdvisoryLockID)
	}()

	started := time.Now()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("begin UI statistics refresh: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	snapshot := &contract.UIStatsSnapshot{
		SchemaVersion: contract.UIStatsSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		ActivitySince: opts.ActivitySince.UTC(),
		MaxLevel:      opts.MaxLevel,
	}
	if err := r.readCharacterStats(ctx, tx, snapshot); err != nil {
		return nil, err
	}
	if err := r.readMilestoneStats(ctx, tx, snapshot); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit UI statistics read snapshot: %w", err)
	}

	snapshot.RefreshDuration = time.Since(started)
	if err := census.ValidateUIStatsSnapshot(snapshot); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("encode UI statistics snapshot: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO ui_stats_snapshots (
			snapshot_key, schema_version, generated_at, activity_since, max_level,
			source_character_count, refresh_duration_ms, payload
		) VALUES ('current', $1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (snapshot_key) DO UPDATE SET
			schema_version = excluded.schema_version,
			generated_at = excluded.generated_at,
			activity_since = excluded.activity_since,
			max_level = excluded.max_level,
			source_character_count = excluded.source_character_count,
			refresh_duration_ms = excluded.refresh_duration_ms,
			payload = excluded.payload`,
		snapshot.SchemaVersion, snapshot.GeneratedAt, snapshot.ActivitySince, snapshot.MaxLevel,
		snapshot.SourceCharacters, snapshot.RefreshDuration.Milliseconds(), payload); err != nil {
		return nil, fmt.Errorf("publish UI statistics snapshot: %w", err)
	}
	return &contract.UIStatsRefreshResult{
		Snapshot:     snapshot,
		PayloadBytes: int64(len(payload)),
	}, nil
}

func (r *UIStatsRepository) readCharacterStats(ctx context.Context, tx *sql.Tx, snapshot *contract.UIStatsSnapshot) error {
	rows, err := tx.QueryContext(ctx, `
		WITH base AS (
			SELECT COALESCE(region, '') AS region,
			       COALESCE(datacenter, '') AS datacenter,
			       COALESCE(world, '') AS world,
			       COALESCE(race, '') AS race,
			       COALESCE(tribe, '') AS tribe,
			       gender,
			       latest_achievement_at,
			       max_job_level
			FROM characters
			WHERE deleted_at IS NULL
		)
		SELECT region, datacenter, world, race, tribe, gender,
		       GROUPING(region), GROUPING(datacenter), GROUPING(world),
		       GROUPING(race), GROUPING(tribe), GROUPING(gender),
		       COUNT(*) AS total,
		       COUNT(*) FILTER (WHERE latest_achievement_at >= $1) AS active,
		       COUNT(*) FILTER (WHERE max_job_level >= $2) AS max_level
		FROM base
		GROUP BY GROUPING SETS (
			(), (region), (datacenter), (world),
			(race), (region, race), (datacenter, race), (world, race),
			(tribe), (region, tribe), (datacenter, tribe), (world, tribe),
			(gender), (region, gender), (datacenter, gender), (world, gender),
			(race, gender), (region, race, gender),
			(datacenter, race, gender), (world, race, gender)
		)`, snapshot.ActivitySince, snapshot.MaxLevel)
	if err != nil {
		return fmt.Errorf("query UI character statistics: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var region, dc, world, race, tribe sql.NullString
		var gender sql.NullInt64
		var gRegion, gDC, gWorld, gRace, gTribe, gGender int
		var total, active, maxLevel int64
		if err := rows.Scan(&region, &dc, &world, &race, &tribe, &gender,
			&gRegion, &gDC, &gWorld, &gRace, &gTribe, &gGender,
			&total, &active, &maxLevel); err != nil {
			return fmt.Errorf("scan UI character statistics: %w", err)
		}

		noDimension := gRace == 1 && gTribe == 1 && gGender == 1
		if noDimension && gRegion == 1 && gDC == 1 && gWorld == 1 {
			snapshot.Summary = contract.StatsSummary{Total: total, Active: active, MaxLevel: maxLevel}
			snapshot.SourceCharacters = total
			continue
		}
		if noDimension {
			if gRegion == 0 {
				snapshot.Groups = append(snapshot.Groups, contract.ScopedGroupCount{Dimension: "region", Key: region.String, Total: total, Active: active})
			} else if gDC == 0 {
				snapshot.Groups = append(snapshot.Groups, contract.ScopedGroupCount{Dimension: "datacenter", Key: dc.String, Total: total, Active: active})
			} else if gWorld == 0 && world.String != "" {
				snapshot.Groups = append(snapshot.Groups, contract.ScopedGroupCount{Dimension: "world", Key: world.String, Total: total, Active: active})
			}
			continue
		}

		scope := contract.StatsScope{}
		if gWorld == 0 {
			scope.World = world.String
		} else if gDC == 0 {
			scope.Datacenter = dc.String
		} else if gRegion == 0 {
			scope.Region = region.String
		}
		dimension, key := "", ""
		switch {
		case gRace == 0 && gGender == 0:
			dimension = "race_gender"
			key = race.String + "|" + genderName(gender.Int64)
		case gRace == 0:
			dimension, key = "race", race.String
		case gTribe == 0:
			dimension, key = "tribe", tribe.String
		case gGender == 0:
			dimension, key = "gender", genderName(gender.Int64)
		}
		if dimension != "" && (scope != (contract.StatsScope{World: ""}) || gWorld == 1) {
			snapshot.Groups = append(snapshot.Groups, contract.ScopedGroupCount{
				Scope: scope, Dimension: dimension, Key: key, Total: total, Active: active,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate UI character statistics: %w", err)
	}
	return nil
}

func (r *UIStatsRepository) readMilestoneStats(ctx context.Context, tx *sql.Tx, snapshot *contract.UIStatsSnapshot) error {
	rows, err := tx.QueryContext(ctx, `
		WITH tracked AS MATERIALIZED (
			SELECT cm.character_id, cm.achievement_id, cm.achieved_at,
			       m.expansion, COALESCE(c.world, '') AS world
			FROM character_milestones cm
			JOIN milestone_achievements m ON m.achievement_id = cm.achievement_id
			JOIN characters c ON c.id = cm.character_id
			WHERE c.deleted_at IS NULL
			  AND (cm.achievement_id = 590 OR
			       ((m.kind = 'expansion_msq' OR m.kind = 'expansion') AND m.expansion IS NOT NULL))
		), expansion_stats AS (
			SELECT world, expansion, GROUPING(world) AS global_scope,
			       COUNT(DISTINCT character_id) AS count
			FROM tracked
			WHERE expansion IS NOT NULL
			GROUP BY GROUPING SETS ((expansion), (world, expansion))
		), daily_stats AS (
			SELECT world, TO_CHAR(achieved_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
			       GROUPING(world) AS global_scope,
			       COUNT(DISTINCT character_id) AS count
			FROM tracked
			WHERE achievement_id = 590 AND achieved_at >= $1
			GROUP BY GROUPING SETS (
				(TO_CHAR(achieved_at AT TIME ZONE 'UTC', 'YYYY-MM-DD')),
				(world, TO_CHAR(achieved_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'))
			)
		)
		SELECT 'expansion' AS kind, world, expansion AS value, global_scope, count
		FROM expansion_stats
		UNION ALL
		SELECT 'daily' AS kind, world, day AS value, global_scope, count
		FROM daily_stats`, snapshot.ActivitySince)
	if err != nil {
		return fmt.Errorf("query UI milestone statistics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var world sql.NullString
		var value string
		var globalScope int
		var count int64
		if err := rows.Scan(&kind, &world, &value, &globalScope, &count); err != nil {
			return fmt.Errorf("scan UI milestone statistics: %w", err)
		}
		scope := contract.StatsScope{}
		if globalScope == 0 {
			if world.String == "" {
				continue
			}
			scope.World = world.String
		}
		switch kind {
		case "expansion":
			snapshot.Expansions = append(snapshot.Expansions, contract.ScopedExpansionCount{Scope: scope, Expansion: value, Count: count})
		case "daily":
			snapshot.NewCharacters = append(snapshot.NewCharacters, contract.ScopedDailyCount{Scope: scope, Day: value, Count: count})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate UI milestone statistics: %w", err)
	}
	return nil
}

func genderName(value int64) string {
	switch value {
	case 1:
		return "Male"
	case 2:
		return "Female"
	default:
		return "Unknown"
	}
}

var _ contract.UIStatsRepository = (*UIStatsRepository)(nil)
