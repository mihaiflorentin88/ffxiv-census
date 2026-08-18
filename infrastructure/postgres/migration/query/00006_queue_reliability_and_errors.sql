-- +goose Up
-- +goose StatementBegin
ALTER TABLE queue_jobs ADD COLUMN IF NOT EXISTS last_error TEXT;
ALTER TABLE queue_jobs ADD COLUMN IF NOT EXISTS failed_at TIMESTAMPTZ;
ALTER TABLE queue_jobs ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_queue_jobs_status_type_run ON queue_jobs (status, type, run_at);
CREATE INDEX IF NOT EXISTS idx_queue_jobs_failed_at ON queue_jobs (status, failed_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_queue_jobs_failed_at;
DROP INDEX IF EXISTS idx_queue_jobs_status_type_run;
-- +goose StatementEnd
