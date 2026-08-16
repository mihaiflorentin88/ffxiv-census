-- Census domain tables.
-- characters.id is the Lodestone character ID (externally assigned, no AUTOINCREMENT).
-- Timestamps are TEXT in UTC "2006-01-02T15:04:05.000Z" (same convention as queue_jobs).
-- A character discovered by the id-sweep but not yet fully fetched has name = '' and
-- last_census_at = NULL ("unverified"); a full census sets both.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE characters (
    id                    INTEGER PRIMARY KEY,
    name                  TEXT    NOT NULL DEFAULT '',
    world                 TEXT    NOT NULL DEFAULT '',
    datacenter            TEXT    NOT NULL DEFAULT '',
    region                TEXT    NOT NULL DEFAULT '',
    race                  TEXT,
    tribe                 TEXT,
    gender                INTEGER NOT NULL DEFAULT 0,
    grand_company         TEXT,
    fc_id                 TEXT,
    fc_name               TEXT,
    achievements_private  INTEGER NOT NULL DEFAULT 0,
    latest_achievement_id INTEGER,
    latest_achievement_at TEXT,
    first_seen_at         TEXT    NOT NULL,
    last_census_at        TEXT,
    deleted_at            TEXT
);

CREATE INDEX idx_characters_region      ON characters (region);
CREATE INDEX idx_characters_world       ON characters (world);
CREATE INDEX idx_characters_datacenter  ON characters (datacenter);
CREATE INDEX idx_characters_race        ON characters (race);
CREATE INDEX idx_characters_fc          ON characters (fc_id);
CREATE INDEX idx_characters_last_census ON characters (last_census_at);

CREATE TABLE character_jobs (
    character_id INTEGER NOT NULL,
    class_job_id INTEGER NOT NULL,
    name         TEXT    NOT NULL,
    level        INTEGER NOT NULL,
    exp_level    INTEGER NOT NULL,
    PRIMARY KEY (character_id, class_job_id)
);

CREATE TABLE milestone_achievements (
    achievement_id INTEGER PRIMARY KEY,
    kind           TEXT    NOT NULL,
    expansion      TEXT,
    detail         TEXT    NOT NULL
);

CREATE TABLE character_milestones (
    character_id   INTEGER NOT NULL,
    achievement_id INTEGER NOT NULL,
    achieved_at    TEXT    NOT NULL,
    PRIMARY KEY (character_id, achievement_id)
);

CREATE TABLE free_companies (
    id           TEXT    PRIMARY KEY,
    name         TEXT    NOT NULL,
    world        TEXT    NOT NULL DEFAULT '',
    datacenter   TEXT    NOT NULL DEFAULT '',
    member_count INTEGER NOT NULL DEFAULT 0,
    formed_at    TEXT,
    last_seen_at TEXT    NOT NULL
);

CREATE TABLE census_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at      TEXT    NOT NULL,
    finished_at     TEXT,
    characters_seen INTEGER NOT NULL DEFAULT 0,
    new_characters  INTEGER NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS census_runs;
DROP TABLE IF EXISTS free_companies;
DROP TABLE IF EXISTS character_milestones;
DROP TABLE IF EXISTS milestone_achievements;
DROP TABLE IF EXISTS character_jobs;
DROP TABLE IF EXISTS characters;
-- +goose StatementEnd
