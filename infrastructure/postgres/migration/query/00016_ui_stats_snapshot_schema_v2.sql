-- UI statistics snapshot schema v2: the daily new-character series reaches
-- back 60 days (headline window + previous-window comparison). Stored v1
-- snapshots stay in place but no longer satisfy the current schema version,
-- so readers reject them and pages serve the explicit unavailable state
-- until the next refresh repopulates the table.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE ui_stats_snapshots DROP CONSTRAINT ui_stats_snapshots_schema_version_check;
ALTER TABLE ui_stats_snapshots ADD CONSTRAINT ui_stats_snapshots_schema_version_check CHECK (schema_version IN (1, 2));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM ui_stats_snapshots;
ALTER TABLE ui_stats_snapshots DROP CONSTRAINT ui_stats_snapshots_schema_version_check;
ALTER TABLE ui_stats_snapshots ADD CONSTRAINT ui_stats_snapshots_schema_version_check CHECK (schema_version = 1);
-- +goose StatementEnd
