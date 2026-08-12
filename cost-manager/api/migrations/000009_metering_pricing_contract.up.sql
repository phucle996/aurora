-- Storage metering pricing and indexes are introduced as a forward-only change.
-- Historical migrations remain byte-for-byte immutable because their checksums
-- are persisted in billing.schema_migrations.

-- Storage usage is billed in decimal GB_HOUR. The baseline catalog used MB
-- thresholds; update only the immutable baseline rows that were seeded by
-- 000006. ON CONFLICT/row predicates make this safe for a partially seeded DB.
UPDATE billing.tier_version_ranges
SET range_start = 0,
    range_end = 50
WHERE id = '755b2b3d-de1d-fe8f-1171-365216565645'
  AND tier_version_id = 'b33aa15e-0421-4185-658b-f0b8132c1723';

UPDATE billing.tier_version_ranges
SET range_start = 50,
    range_end = 0
WHERE id = '9d43c699-6dfa-a17e-32ca-08b67e41b411'
  AND tier_version_id = 'b33aa15e-0421-4185-658b-f0b8132c1723';

-- These indexes were previously appended to 000003. Keep them in the new
-- migration so greenfield and upgraded databases converge without rewriting
-- the historical migration checksum.
CREATE INDEX IF NOT EXISTS idx_storage_report_inbox_pending
    ON billing.storage_usage_report_inbox(status, received_at, report_id)
    WHERE status IN ('RECEIVED', 'PROCESSING', 'UNRATED');

CREATE INDEX IF NOT EXISTS idx_storage_usage_line_resource_window
    ON billing.storage_usage_line_inbox(zone_id, resource_id, created_at);
