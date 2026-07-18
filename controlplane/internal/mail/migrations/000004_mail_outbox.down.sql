-- Mail migration layer 000004 - rollback
DROP INDEX IF EXISTS idx_mail_outbox_pending;
DROP INDEX IF EXISTS idx_mail_outbox_terminal_cleanup;
DROP TABLE IF EXISTS mail_outbox_records;

-- Dọn dẹp Slot & Publication nếu tồn tại
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = 'outbox_slot') THEN
        PERFORM pg_drop_replication_slot('outbox_slot');
    END IF;
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'outbox_pub') THEN
        DROP PUBLICATION outbox_pub;
    END IF;
END $$;
