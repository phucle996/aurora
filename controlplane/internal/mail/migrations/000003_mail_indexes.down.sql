-- 000003_mail_indexes.down.sql

DROP INDEX IF EXISTS idx_mail_endpoints_zone;
DROP INDEX IF EXISTS idx_mail_gateways_tenant;
DROP INDEX IF EXISTS idx_mail_templates_tenant;
DROP INDEX IF EXISTS idx_mail_consumers_tenant;
