-- Profile and Gear extensions
-- +goose Up
-- +goose StatementBegin
ALTER TABLE characters ADD COLUMN IF NOT EXISTS avatar_url TEXT;
ALTER TABLE characters ADD COLUMN IF NOT EXISTS portrait_url TEXT;
ALTER TABLE characters ADD COLUMN IF NOT EXISTS bio TEXT;
ALTER TABLE characters ADD COLUMN IF NOT EXISTS active_job VARCHAR(100);
ALTER TABLE characters ADD COLUMN IF NOT EXISTS item_level INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS character_gear (
    character_id BIGINT       NOT NULL,
    slot         VARCHAR(50)  NOT NULL,
    item_id      INTEGER      NOT NULL,
    name         VARCHAR(255) NOT NULL,
    item_level   INTEGER      NOT NULL DEFAULT 0,
    dye          VARCHAR(100),
    materia      TEXT,
    updated_at   TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (character_id, slot),
    FOREIGN KEY (character_id) REFERENCES characters(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_character_gear_char_id ON character_gear(character_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS character_gear;
-- +goose StatementEnd
