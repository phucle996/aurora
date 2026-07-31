DROP TRIGGER IF EXISTS trg_mail_protected_projection ON mail_outbox_records;
DROP TRIGGER IF EXISTS trg_mail_outbox_payload_key ON mail_outbox_records;
DROP TABLE IF EXISTS mail_protected_projections;
DROP FUNCTION IF EXISTS project_mail_protected_payload();
DROP FUNCTION IF EXISTS enforce_mail_outbox_payload_key();

ALTER TABLE mail_outbox_records
    DROP COLUMN IF EXISTS payload_key_id;
