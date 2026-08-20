-- +goose Up
-- +goose StatementBegin
ALTER TABLE proxies ADD COLUMN locked_by TEXT;
ALTER TABLE proxies ADD COLUMN locked_at TIMESTAMPTZ;
CREATE INDEX idx_proxies_available ON proxies(status, locked_at, latency_ms)
    WHERE status = 'active';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_proxies_available;
ALTER TABLE proxies DROP COLUMN IF EXISTS locked_at;
ALTER TABLE proxies DROP COLUMN IF EXISTS locked_by;
-- +goose StatementEnd
