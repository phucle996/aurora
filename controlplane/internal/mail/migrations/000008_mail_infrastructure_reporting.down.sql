DROP INDEX IF EXISTS idx_mail_infrastructure_reports_expiry;
DROP TABLE IF EXISTS mail_infrastructure_reports;

ALTER TABLE mail_consumer_runtime_reports
    DROP COLUMN IF EXISTS runtime_boot_id,
    DROP COLUMN IF EXISTS runtime_node_id;
