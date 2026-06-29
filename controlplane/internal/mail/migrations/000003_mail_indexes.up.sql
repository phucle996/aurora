-- 000003_mail_indexes.up.sql

CREATE INDEX IF NOT EXISTS idx_mail_consumers_tenant ON mail_consumers(tenant_id);

CREATE INDEX IF NOT EXISTS idx_mail_gateways_tenant ON mail_gateways(tenant_id);

