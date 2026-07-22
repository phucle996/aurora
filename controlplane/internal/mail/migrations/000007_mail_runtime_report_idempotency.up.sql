-- [COMMENT]: Upgrade path cho database dev đã chạy 000002 trước Phase 9; fresh install đã có
-- column này nên toàn bộ câu lệnh vẫn idempotent.
ALTER TABLE mail_consumer_runtime_reports
    ADD COLUMN IF NOT EXISTS event_id UUID;

UPDATE mail_consumer_runtime_reports
SET event_id = gen_random_uuid()
WHERE event_id IS NULL;

ALTER TABLE mail_consumer_runtime_reports
    ALTER COLUMN event_id SET NOT NULL;
