-- +goose Up
ALTER TABLE characters DROP COLUMN IF EXISTS avatar_url;
ALTER TABLE characters DROP COLUMN IF EXISTS portrait_url;

-- +goose Down
ALTER TABLE characters ADD COLUMN avatar_url TEXT;
ALTER TABLE characters ADD COLUMN portrait_url TEXT;
