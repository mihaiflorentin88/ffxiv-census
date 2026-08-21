-- +goose Up
DROP TABLE IF EXISTS free_companies CASCADE;

-- +goose Down
-- Recreate table (see 00003_create_census_tables.sql)
