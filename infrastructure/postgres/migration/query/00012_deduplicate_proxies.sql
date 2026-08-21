-- +goose Up
-- Remove exact (protocol, ip, port) duplicates, keeping the most recently
-- updated row, then restore the tuple uniqueness constraint if absent.
WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY protocol, ip, port
               ORDER BY updated_at DESC, id DESC
           ) AS duplicate_rank
    FROM proxies
)
DELETE FROM proxies p
USING ranked r
WHERE p.id = r.id
  AND r.duplicate_rank > 1;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'proxies'::regclass
          AND contype = 'u'
          AND pg_get_constraintdef(oid) = 'UNIQUE (protocol, ip, port)'
    ) THEN
        ALTER TABLE proxies
            ADD CONSTRAINT proxies_protocol_ip_port_key
            UNIQUE (protocol, ip, port);
    END IF;
END
$$;

-- +goose Down
-- Irreversible data cleanup; retain the original tuple uniqueness invariant.
