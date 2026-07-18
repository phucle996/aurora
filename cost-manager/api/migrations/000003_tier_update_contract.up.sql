-- Migration 000003: Bổ sung OCC và invariant cho luồng cập nhật Tier aggregate.
-- File phải idempotent vì migration runner hiện thực thi lại toàn bộ *.up.sql khi startup.

ALTER TABLE billing.tiers
    ADD COLUMN IF NOT EXISTS version INT NOT NULL DEFAULT 1;

ALTER TABLE billing.tier_ranges
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Một service type chỉ có một biểu giá gốc, loại bỏ khả năng ranges cạnh tranh giữa nhiều Tier.
CREATE UNIQUE INDEX IF NOT EXISTS uq_tiers_service_type
    ON billing.tiers(service_type);

-- Index FK không được PostgreSQL tự tạo; index này hỗ trợ reconcile/cascade theo parent Tier.
CREATE INDEX IF NOT EXISTS idx_tier_ranges_tier_id
    ON billing.tier_ranges(tier_id);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'ck_tier_ranges_start_non_negative'
          AND conrelid = 'billing.tier_ranges'::regclass
    ) THEN
        ALTER TABLE billing.tier_ranges
            ADD CONSTRAINT ck_tier_ranges_start_non_negative CHECK (range_start >= 0);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'ck_tier_ranges_end_after_start'
          AND conrelid = 'billing.tier_ranges'::regclass
    ) THEN
        ALTER TABLE billing.tier_ranges
            ADD CONSTRAINT ck_tier_ranges_end_after_start CHECK (range_end = 0 OR range_end > range_start);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'ck_tier_ranges_price_non_negative'
          AND conrelid = 'billing.tier_ranges'::regclass
    ) THEN
        ALTER TABLE billing.tier_ranges
            ADD CONSTRAINT ck_tier_ranges_price_non_negative CHECK (base_unit_price >= 0);
    END IF;
END $$;

-- Sentinel infinity chỉ được xuất hiện một lần trong Tier; gap/overlap được validate trên aggregate transaction.
CREATE UNIQUE INDEX IF NOT EXISTS uq_tier_ranges_one_infinity
    ON billing.tier_ranges(tier_id)
    WHERE range_end = 0;
