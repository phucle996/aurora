-- Destructive rollback for the pre-000012 schema shape. Durable history lost
-- by hard-delete cannot be reconstructed by a down migration.

ALTER TABLE mfa_settings
    ADD COLUMN IF NOT EXISTS disabled_at timestamptz NULL,
    ALTER COLUMN secret_ciphertext DROP NOT NULL,
    ALTER COLUMN secret_key_id DROP NOT NULL;

ALTER TABLE mfa_recovery_codes
    ADD COLUMN IF NOT EXISTS user_id uuid NULL,
    ADD COLUMN IF NOT EXISTS used_at timestamptz NULL;

UPDATE mfa_recovery_codes rc
SET user_id = ms.user_id
FROM mfa_settings ms
WHERE rc.user_id IS NULL
  AND rc.mfa_setting_id = ms.id;

ALTER TABLE mfa_recovery_codes
    DROP CONSTRAINT IF EXISTS mfa_recovery_codes_mfa_setting_id_fkey,
    ADD CONSTRAINT mfa_recovery_codes_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    ALTER COLUMN user_id SET NOT NULL,
    DROP COLUMN IF EXISTS mfa_setting_id;

CREATE UNIQUE INDEX IF NOT EXISTS mfa_recovery_codes_user_hash_uidx
    ON mfa_recovery_codes(user_id, code_hash);

CREATE INDEX IF NOT EXISTS mfa_recovery_codes_user_id_idx
    ON mfa_recovery_codes(user_id);

CREATE INDEX IF NOT EXISTS mfa_recovery_codes_used_at_idx
    ON mfa_recovery_codes(used_at);

CREATE TABLE IF NOT EXISTS mfa_challenges (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status challenge_status NOT NULL DEFAULT 'pending',
    allowed_methods text[] NOT NULL DEFAULT ARRAY['totp', 'recovery_code'],
    expires_at timestamptz NOT NULL,
    verified_at timestamptz NULL,
    failed_attempts integer NOT NULL DEFAULT 0,
    created_ip inet NULL,
    created_user_agent text NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
