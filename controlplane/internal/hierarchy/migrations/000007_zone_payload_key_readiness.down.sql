ALTER TABLE zone_encryption_keys
    DROP COLUMN IF EXISTS loaded_observed_fencing_token,
    DROP COLUMN IF EXISTS loaded_observed_at,
    DROP COLUMN IF EXISTS loaded_at;
