DROP TRIGGER IF EXISTS trg_storage_outbox_payload_key ON storage_outbox_records;
DROP FUNCTION IF EXISTS enforce_storage_outbox_payload_key();

ALTER TABLE storage_outbox_records
    DROP CONSTRAINT IF EXISTS ck_storage_outbox_rollback_quota,
    DROP CONSTRAINT IF EXISTS ck_storage_outbox_resize_rollback,
    DROP CONSTRAINT IF EXISTS ck_storage_outbox_bucket_resource_name,
    DROP CONSTRAINT IF EXISTS ck_storage_outbox_payload_key_id,
    DROP COLUMN IF EXISTS rollback_quota_bytes,
    DROP COLUMN IF EXISTS resource_name,
    DROP COLUMN IF EXISTS payload_key_id;
