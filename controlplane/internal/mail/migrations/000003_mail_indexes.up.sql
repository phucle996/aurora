-- [COMMENT]: Business row hard-delete nên unique index không cần soft-delete predicate.
CREATE UNIQUE INDEX IF NOT EXISTS ux_mail_consumers_workspace_name
ON mail_consumers (workspace_id, lower(name));

-- [COMMENT]: Hard delete giải phóng code; lần tạo sau bắt buộc dùng UUID mới.
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
