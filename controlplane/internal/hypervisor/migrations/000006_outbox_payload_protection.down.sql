DROP TRIGGER IF EXISTS trg_hypervisor_outbox_payload_key ON hypervisor_outbox_records;
DROP FUNCTION IF EXISTS enforce_hypervisor_outbox_payload_key();

ALTER TABLE hypervisor_outbox_records
    DROP COLUMN IF EXISTS payload_key_id;
