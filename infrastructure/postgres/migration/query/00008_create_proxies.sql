-- +goose Up
-- +goose StatementBegin
CREATE TABLE proxies (
    id              SERIAL PRIMARY KEY,
    protocol        TEXT NOT NULL,
    ip              TEXT NOT NULL,
    port            INTEGER NOT NULL,
    country         TEXT,
    anonymity       TEXT,
    latency_ms      INTEGER,
    uptime_percent  REAL,
    status          TEXT NOT NULL DEFAULT 'inactive',
    last_scanned_at TIMESTAMPTZ,
    last_alive_at   TIMESTAMPTZ,
    first_seen_at   TIMESTAMPTZ NOT NULL,
    source          TEXT NOT NULL,
    fail_count      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    UNIQUE(protocol, ip, port)
);
CREATE INDEX idx_proxies_scan_priority ON proxies(status, last_scanned_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS proxies;
-- +goose StatementEnd
