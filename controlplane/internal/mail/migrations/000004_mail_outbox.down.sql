-- Mail migration layer 000004 - rollback
DROP INDEX IF EXISTS idx_mail_outbox_pending;
DROP TABLE IF EXISTS mail_outbox_records;
