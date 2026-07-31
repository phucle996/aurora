ALTER TABLE hypervisor_outbox_records
    ADD COLUMN IF NOT EXISTS payload_key_id UUID;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM hypervisor_outbox_records WHERE payload_key_id IS NULL) THEN
        RAISE EXCEPTION 'legacy plaintext hypervisor outbox rows must be drained before protected-payload cutover';
    END IF;
END
$$;

ALTER TABLE hypervisor_outbox_records
    ALTER COLUMN payload_key_id SET NOT NULL;

CREATE OR REPLACE FUNCTION enforce_hypervisor_outbox_payload_key()
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
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'hypervisor outbox payload key is not decryptable for target Zone';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_hypervisor_outbox_payload_key ON hypervisor_outbox_records;
CREATE TRIGGER trg_hypervisor_outbox_payload_key
BEFORE INSERT ON hypervisor_outbox_records
FOR EACH ROW EXECUTE FUNCTION enforce_hypervisor_outbox_payload_key();
