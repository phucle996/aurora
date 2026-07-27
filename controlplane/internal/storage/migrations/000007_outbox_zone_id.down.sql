ALTER TABLE storage.storage_outbox_records
    DROP CONSTRAINT IF EXISTS ck_storage_outbox_zone_id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'storage'
          AND table_name = 'storage_outbox_records'
          AND column_name = 'zone_id'
    ) AND NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'storage'
          AND table_name = 'storage_outbox_records'
          AND column_name = 'routing_scope'
    ) THEN
        ALTER TABLE storage.storage_outbox_records
            ALTER COLUMN zone_id TYPE VARCHAR(100) USING ('zone:' || zone_id::text);
        ALTER TABLE storage.storage_outbox_records
            RENAME COLUMN zone_id TO routing_scope;
    END IF;
END
$$;
