-- Queue jobs: durable async work with a claim-based lifecycle.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE queue_jobs (
    id           BIGSERIAL PRIMARY KEY,
    type         VARCHAR(100) NOT NULL,
    payload      TEXT         NOT NULL DEFAULT '{}',
    payload_hash VARCHAR(64)  NOT NULL,
    status       VARCHAR(20)  NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'claimed', 'done', 'failed')),
    run_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    attempts     INTEGER      NOT NULL DEFAULT 0,
    max_attempts INTEGER      NOT NULL DEFAULT 5,
    claimed_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (type, payload_hash)
);

CREATE INDEX idx_queue_jobs_claim ON queue_jobs (type, status, run_at);
CREATE INDEX idx_queue_jobs_status ON queue_jobs (status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS queue_jobs;
-- +goose StatementEnd
