-- External social identities are a live one-to-one binding, not an audit
-- tombstone. Account timeline owns the user-visible history of link/unlink.
DROP INDEX IF EXISTS external_identities_active_user_provider_uk;
DROP INDEX IF EXISTS external_identities_user_idx;

ALTER TABLE external_identities
    DROP COLUMN IF EXISTS revoked_at;

CREATE UNIQUE INDEX IF NOT EXISTS external_identities_user_provider_uk
    ON external_identities (user_id, provider);

CREATE INDEX IF NOT EXISTS external_identities_user_idx
    ON external_identities (user_id);

COMMENT ON TABLE external_identities IS 'Live verified external login identities. Unlink hard-deletes the binding; account timeline retains user-visible history.';
COMMENT ON COLUMN external_identities.linked_at IS 'Most recent successful explicit link time for the current live binding.';
