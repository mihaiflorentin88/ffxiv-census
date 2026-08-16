-- Queue jobs: durable async work with a claim-based lifecycle.
-- Status flow: pending -> claimed -> done
--                        \-> pending (retry, attempts++, run_at backoff)
--                        \-> failed (after max_attempts)
-- Duplicate (type, payload_hash) rows are ignored at insert time.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE queue_jobs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    type         TEXT    NOT NULL,
    payload      TEXT    NOT NULL DEFAULT '{}',
    payload_hash TEXT    NOT NULL,
    status       TEXT    NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'claimed', 'done', 'failed')),
    run_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    claimed_at   TEXT,
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (type, payload_hash)
);

CREATE INDEX idx_queue_jobs_claim ON queue_jobs (type, status, run_at);
CREATE INDEX idx_queue_jobs_status ON queue_jobs (status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS queue_jobs;
-- +goose StatementEnd
