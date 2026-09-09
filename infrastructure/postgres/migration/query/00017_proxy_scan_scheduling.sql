-- +goose Up
-- +goose StatementBegin
ALTER TABLE proxies
 ADD COLUMN general_healthy boolean NOT NULL DEFAULT false,
 ADD COLUMN last_completed_at timestamptz,
 ADD COLUMN last_verified_at timestamptz,
 ADD COLUMN next_attempt_at timestamptz,
 ADD COLUMN scan_not_before timestamptz,
 ADD COLUMN recovery_step integer NOT NULL DEFAULT 0,
 ADD COLUMN observation_version bigint NOT NULL DEFAULT 0,
 ADD COLUMN scan_token text,
 ADD COLUMN scan_lease_until timestamptz,
 ADD COLUMN claimed_version bigint,
 ADD COLUMN destination_cooldown_until timestamptz;

-- Index predicates must be immutable: PostgreSQL rejects NOW() in partial
-- index predicates. Deadline eligibility is resolved at query time.
CREATE INDEX idx_proxies_verification_due ON proxies(next_attempt_at, id)
 WHERE general_healthy;
CREATE INDEX idx_proxies_recovery_due ON proxies(next_attempt_at, id)
 WHERE NOT general_healthy AND last_verified_at IS NOT NULL;
CREATE INDEX idx_proxies_background_completed ON proxies(last_completed_at NULLS FIRST, id)
 WHERE NOT general_healthy;
CREATE INDEX idx_proxies_fresh_verified ON proxies(last_verified_at)
 WHERE general_healthy;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_proxies_fresh_verified;
DROP INDEX IF EXISTS idx_proxies_background_completed;
DROP INDEX IF EXISTS idx_proxies_recovery_due;
DROP INDEX IF EXISTS idx_proxies_verification_due;
ALTER TABLE proxies DROP COLUMN IF EXISTS destination_cooldown_until;
ALTER TABLE proxies DROP COLUMN IF EXISTS claimed_version;
ALTER TABLE proxies DROP COLUMN IF EXISTS scan_lease_until;
ALTER TABLE proxies DROP COLUMN IF EXISTS scan_token;
ALTER TABLE proxies DROP COLUMN IF EXISTS observation_version;
ALTER TABLE proxies DROP COLUMN IF EXISTS recovery_step;
ALTER TABLE proxies DROP COLUMN IF EXISTS scan_not_before;
ALTER TABLE proxies DROP COLUMN IF EXISTS next_attempt_at;
ALTER TABLE proxies DROP COLUMN IF EXISTS last_verified_at;
ALTER TABLE proxies DROP COLUMN IF EXISTS last_completed_at;
ALTER TABLE proxies DROP COLUMN IF EXISTS general_healthy;
-- +goose StatementEnd
