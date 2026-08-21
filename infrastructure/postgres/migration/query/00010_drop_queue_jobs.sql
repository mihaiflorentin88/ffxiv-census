-- +goose Up
DROP TABLE IF EXISTS queue_jobs;

-- +goose Down
-- Recreate table (see 00002_create_queue_jobs.sql)
