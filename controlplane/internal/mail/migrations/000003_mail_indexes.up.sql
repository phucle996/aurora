-- [COMMENT]: Active-name uniqueness cho phép giữ tombstone/history mà vẫn tái sử dụng tên sau delete.
CREATE UNIQUE INDEX IF NOT EXISTS ux_mail_consumers_workspace_active_name
ON mail_consumers (workspace_id, lower(name))
WHERE deleted_at IS NULL;

-- [COMMENT]: Retry POST với cùng idempotency_key trong JSON body trả aggregate cũ; reuse khác payload bị service từ chối.
CREATE UNIQUE INDEX IF NOT EXISTS ux_mail_consumers_workspace_idempotency
ON mail_consumers (workspace_id, create_idempotency_key);

CREATE INDEX IF NOT EXISTS idx_mail_consumers_workspace_cursor
ON mail_consumers (workspace_id, id)
WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_mail_consumers_desired_cursor
ON mail_consumers (desired_state, id)
WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS ux_personal_mail_templates_active_name ON personal_mail_templates(workspace_id,lower(name)) WHERE archived_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS ux_personal_mail_templates_idempotency ON personal_mail_templates(workspace_id,create_idempotency_key);
CREATE INDEX IF NOT EXISTS idx_personal_mail_template_versions_cursor ON personal_mail_template_versions(template_id,created_at DESC,version DESC);
CREATE UNIQUE INDEX IF NOT EXISTS ux_tenant_mail_templates_active_name ON tenant_mail_templates(workspace_id,lower(name)) WHERE archived_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS ux_tenant_mail_templates_idempotency ON tenant_mail_templates(workspace_id,create_idempotency_key);
CREATE INDEX IF NOT EXISTS idx_tenant_mail_template_versions_cursor ON tenant_mail_template_versions(template_id,created_at DESC,version DESC);

CREATE INDEX IF NOT EXISTS idx_mail_runtime_reports_expiry
ON mail_consumer_runtime_reports (expires_at, consumer_id);

CREATE INDEX IF NOT EXISTS idx_mail_result_inbox_pending
ON mail_result_inbox (received_at, event_id)
WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_mail_submissions_workspace_history
ON mail_submissions (workspace_id, created_at DESC, submission_id DESC);

CREATE INDEX IF NOT EXISTS idx_mail_submissions_consumer_history
ON mail_submissions (consumer_id, created_at DESC, submission_id DESC);

CREATE INDEX IF NOT EXISTS idx_mail_delivery_attempts_submission
ON mail_delivery_attempts (submission_id, state_version);
