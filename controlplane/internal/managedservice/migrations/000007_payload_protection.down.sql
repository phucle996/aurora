DROP TRIGGER IF EXISTS trg_managed_service_outbox_payload_key ON managed_service_outbox_records;
DROP FUNCTION IF EXISTS enforce_managed_service_outbox_payload_key();

ALTER TABLE managed_service_outbox_records
    DROP COLUMN IF EXISTS payload_key_id;
