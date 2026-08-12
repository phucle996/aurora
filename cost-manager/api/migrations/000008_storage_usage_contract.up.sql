-- Upgrade the historical storage settlement tables to the generic metering
-- contract. All changes are additive or constraint replacement so existing
-- reports remain replayable and no billing history is deleted.
ALTER TABLE billing.storage_usage_line_inbox
    ADD COLUMN IF NOT EXISTS resource_name VARCHAR(255),
    ADD COLUMN IF NOT EXISTS usage_unit VARCHAR(24) NOT NULL DEFAULT 'BYTE';

ALTER TABLE billing.storage_usage_line_inbox
    DROP CONSTRAINT IF EXISTS ck_storage_usage_line_direction,
    DROP CONSTRAINT IF EXISTS ck_storage_usage_line_unit,
    DROP CONSTRAINT IF EXISTS ck_storage_usage_line_direction_unit,
    DROP CONSTRAINT IF EXISTS ck_storage_usage_line_resource_reference;

ALTER TABLE billing.storage_usage_line_inbox
    ADD CONSTRAINT ck_storage_usage_line_direction
        CHECK (direction IN ('NETWORK_IN', 'NETWORK_OUT', 'STORAGE')),
    ADD CONSTRAINT ck_storage_usage_line_unit
        CHECK (usage_unit IN ('BYTE', 'GB_HOUR_MICRO')),
    ADD CONSTRAINT ck_storage_usage_line_direction_unit
        CHECK (
            (direction IN ('NETWORK_IN', 'NETWORK_OUT') AND usage_unit = 'BYTE')
            OR (direction = 'STORAGE' AND usage_unit = 'GB_HOUR_MICRO')
        ),
    ADD CONSTRAINT ck_storage_usage_line_resource_reference
        CHECK (resource_id IS NOT NULL OR resource_name IS NOT NULL);

CREATE INDEX IF NOT EXISTS idx_storage_usage_line_resource_name
    ON billing.storage_usage_line_inbox(zone_id, resource_name, created_at)
    WHERE resource_name IS NOT NULL;
