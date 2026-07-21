-- [COMMENT]: Active-name uniqueness cho phép giữ tombstone/history mà vẫn tái sử dụng tên sau delete.
CREATE UNIQUE INDEX IF NOT EXISTS ux_mail_consumers_workspace_active_name
ON mail_consumers (workspace_id, lower(name))
WHERE deleted_at IS NULL;

-- [COMMENT]: Code chỉ unique khi resource chưa delete xong; UUID mới fence toàn bộ event cũ khi code được reuse.
CREATE UNIQUE INDEX IF NOT EXISTS ux_mail_consumers_workspace_active_code
ON mail_consumers (workspace_id, code) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_mail_consumers_workspace_cursor
ON mail_consumers (workspace_id, id)
WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_mail_consumers_desired_cursor
ON mail_consumers (desired_state, id)
WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS ux_personal_mail_templates_workspace_code ON personal_mail_templates(workspace_id,code);
CREATE INDEX IF NOT EXISTS idx_personal_mail_template_versions_cursor ON personal_mail_template_versions(template_id,created_at DESC,version DESC);
CREATE UNIQUE INDEX IF NOT EXISTS ux_tenant_mail_templates_workspace_code ON tenant_mail_templates(workspace_id,code);
CREATE INDEX IF NOT EXISTS idx_tenant_mail_template_versions_cursor ON tenant_mail_template_versions(template_id,created_at DESC,version DESC);

CREATE INDEX IF NOT EXISTS idx_mail_runtime_reports_expiry
ON mail_consumer_runtime_reports (expires_at, consumer_id);
