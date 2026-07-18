DROP INDEX IF EXISTS billing.uq_tier_ranges_one_infinity;
DROP INDEX IF EXISTS billing.idx_tier_ranges_tier_id;
DROP INDEX IF EXISTS billing.uq_tiers_service_type;

ALTER TABLE billing.tier_ranges
    DROP CONSTRAINT IF EXISTS ck_tier_ranges_price_non_negative,
    DROP CONSTRAINT IF EXISTS ck_tier_ranges_end_after_start,
    DROP CONSTRAINT IF EXISTS ck_tier_ranges_start_non_negative,
    DROP COLUMN IF EXISTS updated_at;

ALTER TABLE billing.tiers
    DROP COLUMN IF EXISTS version;
