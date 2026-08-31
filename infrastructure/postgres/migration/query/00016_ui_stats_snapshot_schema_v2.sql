-- UI statistics snapshot schema v2: the daily new-character series reaches
-- back 60 days (headline window + previous-window comparison). Stored v1
-- snapshots cannot satisfy the new series and are dropped; the next refresh
-- repopulates the table.
-- +goose Up
-- +goose StatementBegin
DELETE FROM ui_stats_snapshots;
ALTER TABLE ui_stats_snapshots DROP CONSTRAINT ui_stats_snapshots_schema_version_check;
ALTER TABLE ui_stats_snapshots ADD CONSTRAINT ui_stats_snapshots_schema_version_check CHECK (schema_version = 2);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM ui_stats_snapshots;
ALTER TABLE ui_stats_snapshots DROP CONSTRAINT ui_stats_snapshots_schema_version_check;
ALTER TABLE ui_stats_snapshots ADD CONSTRAINT ui_stats_snapshots_schema_version_check CHECK (schema_version = 1);
-- +goose StatementEnd
