CREATE UNIQUE INDEX IF NOT EXISTS ux_mail_consumers_workspace_name
ON mail_consumers (workspace_id, lower(name));

CREATE UNIQUE INDEX IF NOT EXISTS ux_mail_consumers_workspace_code
ON mail_consumers (workspace_id, code);

CREATE INDEX IF NOT EXISTS idx_mail_consumers_workspace_cursor
ON mail_consumers (workspace_id, id);

CREATE INDEX IF NOT EXISTS idx_mail_consumers_desired_cursor
ON mail_consumers (desired_state, id);

CREATE UNIQUE INDEX IF NOT EXISTS ux_personal_mail_templates_workspace_code ON personal_mail_templates(workspace_id,code);
CREATE INDEX IF NOT EXISTS idx_personal_mail_template_versions_cursor ON personal_mail_template_versions(template_id,created_at DESC,version DESC);
CREATE UNIQUE INDEX IF NOT EXISTS ux_tenant_mail_templates_workspace_code ON tenant_mail_templates(workspace_id,code);
CREATE INDEX IF NOT EXISTS idx_tenant_mail_template_versions_cursor ON tenant_mail_template_versions(template_id,created_at DESC,version DESC);

CREATE INDEX IF NOT EXISTS idx_mail_runtime_reports_expiry
ON mail_consumer_runtime_reports (expires_at, consumer_id);

CREATE INDEX IF NOT EXISTS idx_mail_outbox_pending
ON mail_outbox_records (status, id ASC)
WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_mail_outbox_terminal_cleanup
ON mail_outbox_records (completed_at, id)
WHERE status IN ('SUCCEEDED', 'FAILED') AND completed_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS ix_personal_mail_template_projection_tombstones_workspace
ON personal_mail_template_projection_tombstones (workspace_id, template_id);

CREATE INDEX IF NOT EXISTS ix_tenant_mail_template_projection_tombstones_workspace
ON tenant_mail_template_projection_tombstones (workspace_id, template_id);

CREATE INDEX IF NOT EXISTS idx_mail_consumer_tombstones_zone_cursor
ON mail_consumer_projection_tombstones (zone_id, consumer_id);
