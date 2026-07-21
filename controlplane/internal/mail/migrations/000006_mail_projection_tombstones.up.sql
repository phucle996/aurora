-- [COMMENT]: Hard-delete template vẫn cần authoritative tombstone để Zone L2 rebuild sau outbox retention.
CREATE TABLE IF NOT EXISTS personal_mail_template_projection_tombstones (
    template_id VARCHAR(128) PRIMARY KEY,
    workspace_id UUID NOT NULL,
    template_revision BIGINT NOT NULL CHECK (template_revision > 0),
    last_published_version BIGINT NOT NULL CHECK (last_published_version > 0),
    event_id UUID NOT NULL UNIQUE,
    deleted_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS ix_personal_mail_template_projection_tombstones_workspace
ON personal_mail_template_projection_tombstones (workspace_id, template_id);

-- [COMMENT]: Tenant đi bảng riêng để reconciliation không suy diễn scope từ payload.
CREATE TABLE IF NOT EXISTS tenant_mail_template_projection_tombstones (
    template_id VARCHAR(128) PRIMARY KEY,
    workspace_id UUID NOT NULL,
    template_revision BIGINT NOT NULL CHECK (template_revision > 0),
    last_published_version BIGINT NOT NULL CHECK (last_published_version > 0),
    event_id UUID NOT NULL UNIQUE,
    deleted_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS ix_tenant_mail_template_projection_tombstones_workspace
ON tenant_mail_template_projection_tombstones (workspace_id, template_id);
