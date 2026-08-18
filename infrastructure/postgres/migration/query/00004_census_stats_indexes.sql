-- Indexes for aggregate/stats queries
-- +goose Up
-- +goose StatementBegin
CREATE INDEX idx_characters_latest_achievement ON characters (latest_achievement_at);
CREATE INDEX idx_characters_first_seen ON characters (first_seen_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_characters_latest_achievement;
DROP INDEX IF EXISTS idx_characters_first_seen;
-- +goose StatementEnd
