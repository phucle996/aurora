DROP INDEX IF EXISTS billing.idx_storage_usage_line_resource_window;
DROP INDEX IF EXISTS billing.idx_storage_report_inbox_pending;

-- Restore the historical baseline thresholds when explicitly rolling back the
-- forward migration. No billing rows or settlement history are deleted.
UPDATE billing.tier_version_ranges
SET range_start = 0,
    range_end = 51200
WHERE id = '755b2b3d-de1d-fe8f-1171-365216565645'
  AND tier_version_id = 'b33aa15e-0421-4185-658b-f0b8132c1723';

UPDATE billing.tier_version_ranges
SET range_start = 51200,
    range_end = 0
WHERE id = '9d43c699-6dfa-a17e-32ca-08b67e41b411'
  AND tier_version_id = 'b33aa15e-0421-4185-658b-f0b8132c1723';
