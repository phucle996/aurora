ALTER TABLE mail_outbox_records
    ADD COLUMN IF NOT EXISTS payload_key_id UUID;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM mail_outbox_records WHERE payload_key_id IS NULL) THEN
        RAISE EXCEPTION 'legacy plaintext mail outbox rows must be drained before protected-payload cutover';
    END IF;
END
$$;

ALTER TABLE mail_outbox_records
    ALTER COLUMN payload_key_id SET NOT NULL;

CREATE OR REPLACE FUNCTION enforce_mail_outbox_payload_key()
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
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'mail outbox payload key is not decryptable for target Zone';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_mail_outbox_payload_key ON mail_outbox_records;
CREATE TRIGGER trg_mail_outbox_payload_key
BEFORE INSERT ON mail_outbox_records
FOR EACH ROW EXECUTE FUNCTION enforce_mail_outbox_payload_key();

CREATE TABLE IF NOT EXISTS mail_protected_projections (
    event_id UUID PRIMARY KEY,
    resource_kind VARCHAR(32) NOT NULL CHECK (resource_kind IN ('consumer', 'template')),
    resource_id VARCHAR(128) NOT NULL,
    zone_id UUID NOT NULL,
    job_topic VARCHAR(100) NOT NULL,
    payload BYTEA NOT NULL,
    payload_key_id UUID NOT NULL,
    source_outbox_id BIGINT NOT NULL UNIQUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION project_mail_protected_payload()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    kind VARCHAR(32);
BEGIN
    kind := split_part(NEW.job_topic, '.', 2);
    IF kind NOT IN ('consumer', 'template') THEN
        RAISE EXCEPTION 'unsupported mail protected projection topic: %', NEW.job_topic;
    END IF;
    INSERT INTO mail_protected_projections (
        event_id, resource_kind, resource_id, zone_id, job_topic, payload,
        payload_key_id, source_outbox_id, updated_at
    ) VALUES (
        NEW.event_id, kind, NEW.resource_id, NEW.zone_id, NEW.job_topic, NEW.payload,
        NEW.payload_key_id, NEW.id, now()
    )
    ON CONFLICT (event_id) DO NOTHING;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_mail_protected_projection ON mail_outbox_records;
CREATE TRIGGER trg_mail_protected_projection
AFTER INSERT ON mail_outbox_records
FOR EACH ROW EXECUTE FUNCTION project_mail_protected_payload();

-- Existing protected rows are safe to project byte-for-byte. The migration
-- never parses or reconstructs their plaintext command.
INSERT INTO mail_protected_projections (
    event_id, resource_kind, resource_id, zone_id, job_topic, payload,
    payload_key_id, source_outbox_id, updated_at
)
SELECT event_id, split_part(job_topic, '.', 2), resource_id, zone_id, job_topic,
       payload, payload_key_id, id, updated_at
FROM mail_outbox_records
ON CONFLICT (event_id) DO NOTHING;

CREATE INDEX IF NOT EXISTS ix_mail_protected_projections_resource_head
ON mail_protected_projections (resource_kind, resource_id, source_outbox_id DESC);
