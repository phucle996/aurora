-- IAM migration layer 000012
-- MFA has one durable active enrollment. Pending setup, login continuation and
-- TOTP replay state are short-lived Auth-State Redis records.

-- The SQL challenge table was never the runtime source and is safe to remove.
DROP TABLE IF EXISTS mfa_challenges CASCADE;

-- Rows without an encrypted secret represent the old disabled/partial state.
-- Hard-delete them before tightening the durable schema; recovery rows linked
-- only by user_id are removed below when their enrollment no longer exists.
DELETE FROM mfa_settings
WHERE secret_ciphertext IS NULL;

UPDATE mfa_settings
SET secret_key_id = 'runtime-master-v1'
WHERE secret_key_id IS NULL;

ALTER TABLE mfa_recovery_codes
    ADD COLUMN IF NOT EXISTS mfa_setting_id uuid NULL;

UPDATE mfa_recovery_codes rc
SET mfa_setting_id = ms.id
FROM mfa_settings ms
WHERE rc.mfa_setting_id IS NULL
  AND rc.user_id = ms.user_id;

DELETE FROM mfa_recovery_codes
WHERE mfa_setting_id IS NULL;

ALTER TABLE mfa_recovery_codes
    DROP CONSTRAINT IF EXISTS mfa_recovery_codes_user_id_fkey,
    ADD CONSTRAINT mfa_recovery_codes_mfa_setting_id_fkey
        FOREIGN KEY (mfa_setting_id) REFERENCES mfa_settings(id) ON DELETE CASCADE,
    ALTER COLUMN mfa_setting_id SET NOT NULL,
    DROP COLUMN user_id,
    DROP COLUMN IF EXISTS used_at,
    DROP COLUMN IF EXISTS generation,
    DROP COLUMN IF EXISTS revoked_at;

ALTER TABLE mfa_settings
    ALTER COLUMN secret_ciphertext SET NOT NULL,
    ALTER COLUMN secret_key_id SET NOT NULL,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS setup_expires_at,
    DROP COLUMN IF EXISTS enabled_at,
    DROP COLUMN IF EXISTS recovery_generation,
    DROP COLUMN IF EXISTS last_accepted_totp_step,
    DROP COLUMN IF EXISTS disabled_at;

DROP INDEX IF EXISTS mfa_recovery_codes_user_hash_uidx;
DROP INDEX IF EXISTS mfa_recovery_codes_user_id_idx;
DROP INDEX IF EXISTS mfa_recovery_codes_used_at_idx;
DROP INDEX IF EXISTS mfa_recovery_codes_user_generation_hash_uidx;
DROP INDEX IF EXISTS mfa_recovery_codes_active_idx;
DROP INDEX IF EXISTS mfa_settings_status_idx;

CREATE INDEX IF NOT EXISTS mfa_recovery_codes_setting_idx
    ON mfa_recovery_codes(mfa_setting_id, created_at);

CREATE UNIQUE INDEX IF NOT EXISTS mfa_recovery_codes_setting_hash_uidx
    ON mfa_recovery_codes(mfa_setting_id, code_hash);

COMMENT ON TABLE mfa_settings IS
    'One active MFA enrollment per user. Removing MFA hard-deletes this row.';
COMMENT ON COLUMN mfa_settings.secret_ciphertext IS
    'Encrypted TOTP secret. Plain secret must never be stored.';
COMMENT ON TABLE mfa_recovery_codes IS
    'Unused recovery-code hashes only. Consuming a code hard-deletes its row.';
