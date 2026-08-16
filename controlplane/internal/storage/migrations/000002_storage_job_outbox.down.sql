DROP TRIGGER IF EXISTS trg_storage_outbox_payload_key ON storage_outbox_records;
DROP FUNCTION IF EXISTS enforce_storage_outbox_payload_key();
DROP TABLE IF EXISTS storage_outbox_records;
