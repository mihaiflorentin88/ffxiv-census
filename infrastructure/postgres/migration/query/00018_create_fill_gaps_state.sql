-- +goose Up
CREATE TABLE fill_gaps_state (
    singleton      BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    last_queued_id BIGINT      NOT NULL CHECK (last_queued_id BETWEEN 0 AND 4294967295),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS fill_gaps_state;
