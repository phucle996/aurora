DROP INDEX IF EXISTS billing.idx_storage_usage_line_resource_name;

ALTER TABLE billing.storage_usage_line_inbox
    DROP CONSTRAINT IF EXISTS ck_storage_usage_line_resource_reference,
    DROP CONSTRAINT IF EXISTS ck_storage_usage_line_direction_unit,
    DROP CONSTRAINT IF EXISTS ck_storage_usage_line_unit;

ALTER TABLE billing.storage_usage_line_inbox
    DROP COLUMN IF EXISTS resource_name,
    DROP COLUMN IF EXISTS usage_unit;

ALTER TABLE billing.storage_usage_line_inbox
    DROP CONSTRAINT IF EXISTS ck_storage_usage_line_direction;

ALTER TABLE billing.storage_usage_line_inbox
    ADD CONSTRAINT ck_storage_usage_line_direction
        CHECK (direction IN ('NETWORK_OUT'));
