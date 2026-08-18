-- Census domain tables.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE characters (
    id                    BIGINT PRIMARY KEY,
    name                  VARCHAR(255) NOT NULL DEFAULT '',
    world                 VARCHAR(100) NOT NULL DEFAULT '',
    datacenter            VARCHAR(100) NOT NULL DEFAULT '',
    region                VARCHAR(100) NOT NULL DEFAULT '',
    race                  VARCHAR(100),
    tribe                 VARCHAR(100),
    gender                SMALLINT     NOT NULL DEFAULT 0,
    grand_company         VARCHAR(100),
    fc_id                 VARCHAR(100),
    fc_name               VARCHAR(255),
    achievements_private  SMALLINT     NOT NULL DEFAULT 0,
    latest_achievement_id INTEGER,
    latest_achievement_at TIMESTAMPTZ,
    first_seen_at         TIMESTAMPTZ  NOT NULL,
    last_census_at        TIMESTAMPTZ,
    deleted_at            TIMESTAMPTZ
);

CREATE INDEX idx_characters_region      ON characters (region);
CREATE INDEX idx_characters_world       ON characters (world);
CREATE INDEX idx_characters_datacenter  ON characters (datacenter);
CREATE INDEX idx_characters_race        ON characters (race);
CREATE INDEX idx_characters_fc          ON characters (fc_id);
CREATE INDEX idx_characters_last_census ON characters (last_census_at);

CREATE TABLE character_jobs (
    character_id BIGINT  NOT NULL,
    class_job_id INTEGER NOT NULL,
    name         VARCHAR(100) NOT NULL,
    level        INTEGER NOT NULL,
    exp_level    INTEGER NOT NULL,
    PRIMARY KEY (character_id, class_job_id)
);

CREATE TABLE milestone_achievements (
    achievement_id INTEGER PRIMARY KEY,
    kind           VARCHAR(100) NOT NULL,
    expansion      VARCHAR(100),
    detail         TEXT         NOT NULL
);

CREATE TABLE character_milestones (
    character_id   BIGINT      NOT NULL,
    achievement_id INTEGER     NOT NULL,
    achieved_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (character_id, achievement_id)
);

CREATE TABLE free_companies (
    id           VARCHAR(100) PRIMARY KEY,
    name         VARCHAR(255) NOT NULL,
    world        VARCHAR(100) NOT NULL DEFAULT '',
    datacenter   VARCHAR(100) NOT NULL DEFAULT '',
    member_count INTEGER      NOT NULL DEFAULT 0,
    formed_at    TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ  NOT NULL
);

CREATE TABLE census_runs (
    id              BIGSERIAL PRIMARY KEY,
    started_at      TIMESTAMPTZ NOT NULL,
    finished_at     TIMESTAMPTZ,
    characters_seen INTEGER     NOT NULL DEFAULT 0,
    new_characters  INTEGER     NOT NULL DEFAULT 0
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
