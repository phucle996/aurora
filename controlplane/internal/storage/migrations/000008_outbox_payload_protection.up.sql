ALTER TABLE storage_outbox_records
    ADD COLUMN IF NOT EXISTS payload_key_id UUID,
    ADD COLUMN IF NOT EXISTS resource_name VARCHAR(255),
    ADD COLUMN IF NOT EXISTS rollback_quota_bytes BIGINT;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM storage_outbox_records WHERE payload_key_id IS NULL) THEN
        RAISE EXCEPTION 'legacy plaintext storage outbox rows must be drained before protected-payload cutover';
    END IF;
    IF EXISTS (
        SELECT 1 FROM storage_outbox_records
        WHERE job_topic IN ('storage.bucket.create', 'storage.bucket.resize', 'storage.bucket.delete')
          AND (resource_name IS NULL OR length(btrim(resource_name)) = 0)
    ) OR EXISTS (
        SELECT 1 FROM storage_outbox_records
        WHERE job_topic = 'storage.bucket.resize' AND rollback_quota_bytes IS NULL
    ) THEN
        RAISE EXCEPTION 'legacy storage bucket outbox rows lack opaque-result settlement metadata';
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION enforce_storage_outbox_payload_key()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    -- Serialize key retirement with the transaction that makes ciphertext durable.
    PERFORM 1 FROM hierarchy.zones WHERE id = NEW.zone_id FOR KEY SHARE;
    PERFORM 1
    FROM hierarchy.zone_encryption_keys
    WHERE id = NEW.payload_key_id
      AND zone_id = NEW.zone_id
      AND status IN ('active', 'decrypt_only')
    FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'storage outbox payload key is not decryptable for target Zone';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_storage_outbox_payload_key ON storage_outbox_records;
CREATE TRIGGER trg_storage_outbox_payload_key
BEFORE INSERT ON storage_outbox_records
FOR EACH ROW EXECUTE FUNCTION enforce_storage_outbox_payload_key();

ALTER TABLE storage_outbox_records
    ALTER COLUMN payload_key_id SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'ck_storage_outbox_payload_key_id') THEN
        ALTER TABLE storage_outbox_records ADD CONSTRAINT ck_storage_outbox_payload_key_id
            CHECK (payload_key_id <> '00000000-0000-0000-0000-000000000000'::uuid);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'ck_storage_outbox_bucket_resource_name') THEN
        ALTER TABLE storage_outbox_records ADD CONSTRAINT ck_storage_outbox_bucket_resource_name
            CHECK (
                job_topic NOT IN ('storage.bucket.create', 'storage.bucket.resize', 'storage.bucket.delete')
                OR length(btrim(resource_name)) BETWEEN 1 AND 255
            );
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'ck_storage_outbox_resize_rollback') THEN
        ALTER TABLE storage_outbox_records ADD CONSTRAINT ck_storage_outbox_resize_rollback
            CHECK (job_topic <> 'storage.bucket.resize' OR rollback_quota_bytes IS NOT NULL);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'ck_storage_outbox_rollback_quota') THEN
        ALTER TABLE storage_outbox_records ADD CONSTRAINT ck_storage_outbox_rollback_quota
            CHECK (rollback_quota_bytes IS NULL OR rollback_quota_bytes >= 0);
    END IF;
END
$$;
