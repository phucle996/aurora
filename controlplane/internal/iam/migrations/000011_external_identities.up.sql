-- IAM migration layer 000011
-- External Google/GitHub identities for user login.

ALTER TABLE users
    ALTER COLUMN email TYPE varchar(320);

COMMENT ON COLUMN users.password_hash IS
    'Current Argon2id password hash. Every account must have a password credential; external identities are additional login methods.';

CREATE TABLE IF NOT EXISTS external_identities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider varchar(32) NOT NULL
        CHECK (provider IN ('google', 'github')),
    provider_subject varchar(255) NOT NULL,
    provider_email varchar(320) NOT NULL,
    email_verified_at timestamptz NOT NULL,
    display_name varchar(120) NOT NULL,
    avatar_url varchar(2048) NULL,
    last_login_at timestamptz NULL,
    revoked_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT external_identities_provider_subject_uk
        UNIQUE (provider, provider_subject)
);

CREATE UNIQUE INDEX IF NOT EXISTS external_identities_active_user_provider_uk
    ON external_identities (user_id, provider)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS external_identities_user_idx
    ON external_identities (user_id)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS external_identities_email_idx
    ON external_identities (provider_email);

DROP TRIGGER IF EXISTS trg_external_identities_updated_at ON external_identities;
CREATE TRIGGER trg_external_identities_updated_at
BEFORE UPDATE ON external_identities
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE external_identities IS
    'Verified external login identities. Provider subject is the identity key; provider_email is only a latest verified snapshot.';
COMMENT ON COLUMN external_identities.provider_subject IS
    'Opaque stable subject from the provider. Never replace this with an email or mutable username.';
COMMENT ON COLUMN external_identities.email_verified_at IS
    'Local time when ACR accepted the provider email assertion as verified.';
COMMENT ON COLUMN external_identities.revoked_at IS
    'Soft unlink timestamp. Provider subject uniqueness remains immutable to prevent account takeover by reuse.';
