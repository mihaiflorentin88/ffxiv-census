-- Profile and Gear extensions, and index for gap scanning
-- +goose Up
-- +goose StatementBegin
ALTER TABLE characters ADD COLUMN avatar_url TEXT;
ALTER TABLE characters ADD COLUMN portrait_url TEXT;
ALTER TABLE characters ADD COLUMN bio TEXT;
ALTER TABLE characters ADD COLUMN active_job TEXT;
ALTER TABLE characters ADD COLUMN item_level INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS character_gear (
    character_id INTEGER NOT NULL,
    slot TEXT NOT NULL,
    item_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    item_level INTEGER NOT NULL DEFAULT 0,
    dye TEXT,
    materia TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (character_id, slot),
    FOREIGN KEY (character_id) REFERENCES characters(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_character_gear_char_id ON character_gear(character_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS character_gear;
-- +goose StatementEnd
