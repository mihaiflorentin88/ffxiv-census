-- Indexes for milestone analytics
-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_character_milestones_achievement_achieved ON character_milestones (achievement_id, achieved_at);
CREATE INDEX IF NOT EXISTS idx_character_milestones_char_ach ON character_milestones (character_id, achievement_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_character_milestones_achievement_achieved;
DROP INDEX IF EXISTS idx_character_milestones_char_ach;
-- +goose StatementEnd
