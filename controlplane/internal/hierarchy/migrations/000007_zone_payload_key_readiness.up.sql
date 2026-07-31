ALTER TABLE zone_encryption_keys
    ADD COLUMN IF NOT EXISTS loaded_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS loaded_observed_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS loaded_observed_fencing_token BIGINT NULL CHECK (loaded_observed_fencing_token > 0);

COMMENT ON COLUMN zone_encryption_keys.loaded_at IS 'Latest trusted Zone report timestamp that proved the matching private key was loaded; NULL means not ready.';
COMMENT ON COLUMN zone_encryption_keys.loaded_observed_at IS 'Monotonic report fence, including reports where this key was absent.';
COMMENT ON COLUMN zone_encryption_keys.loaded_observed_fencing_token IS 'Zone leader fencing token paired with loaded_observed_at.';
