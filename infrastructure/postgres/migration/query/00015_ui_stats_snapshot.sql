-- Bounded UI analytics snapshot and denormalized maximum job level.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE characters
    ADD COLUMN max_job_level SMALLINT NOT NULL DEFAULT 0;

UPDATE characters c
SET max_job_level = j.max_level
FROM (
    SELECT character_id, MAX(level)::SMALLINT AS max_level
    FROM character_jobs
    GROUP BY character_id
) j
WHERE j.character_id = c.id;

CREATE TABLE ui_stats_snapshots (
    snapshot_key            TEXT PRIMARY KEY,
    schema_version          INTEGER NOT NULL,
    generated_at            TIMESTAMPTZ NOT NULL,
    activity_since          TIMESTAMPTZ NOT NULL,
    max_level               INTEGER NOT NULL,
    source_character_count  BIGINT NOT NULL,
    refresh_duration_ms     BIGINT NOT NULL,
    payload                 JSONB NOT NULL,
    CHECK (snapshot_key = 'current'),
    CHECK (schema_version = 1)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ui_stats_snapshots;
ALTER TABLE characters DROP COLUMN IF EXISTS max_job_level;
-- +goose StatementEnd
