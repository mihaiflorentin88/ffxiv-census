-- +goose Up
CREATE TABLE id_sweep_state (
    singleton  BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    next_id    BIGINT      NOT NULL CHECK (next_id BETWEEN 1 AND 4294967295),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS id_sweep_state;
