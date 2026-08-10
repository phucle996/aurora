DROP INDEX IF EXISTS external_identities_user_idx;
DROP INDEX IF EXISTS external_identities_user_provider_uk;

ALTER TABLE external_identities
    ADD COLUMN IF NOT EXISTS revoked_at timestamptz NULL;

CREATE UNIQUE INDEX IF NOT EXISTS external_identities_active_user_provider_uk
    ON external_identities (user_id, provider)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS external_identities_user_idx
    ON external_identities (user_id)
    WHERE revoked_at IS NULL;
