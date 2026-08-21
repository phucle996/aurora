-- Greenfield Storage resource baseline.
CREATE TABLE personal_buckets (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    workspace_id UUID NOT NULL,
    zone_id UUID NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PROVISIONING',
    capacity_quota_bytes BIGINT NOT NULL DEFAULT 0,
    used_bytes BIGINT NOT NULL DEFAULT 0,
    used_bytes_observed_at TIMESTAMPTZ,
    encrypt_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    versioning_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    object_locking_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    replication_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    retention_days INTEGER NOT NULL DEFAULT 0,
    legal_hold_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    tags JSONB NOT NULL DEFAULT '{}',
    lifecycle_rules JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ux_personal_buckets_name UNIQUE (name),
    CONSTRAINT ck_personal_buckets_status
        CHECK (status IN ('PROVISIONING', 'READY', 'UPDATING', 'DELETING', 'FAILED')),
    CONSTRAINT fk_personal_buckets_workspace
        FOREIGN KEY (workspace_id)
        REFERENCES hierarchy.personal_workspaces(id) ON DELETE RESTRICT
);

CREATE INDEX idx_personal_buckets_workspace
    ON personal_buckets(workspace_id);

CREATE TABLE tenant_buckets (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    workspace_id UUID NOT NULL,
    zone_id UUID NOT NULL,
    tenant_id UUID NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PROVISIONING',
    capacity_quota_bytes BIGINT NOT NULL DEFAULT 0,
    used_bytes BIGINT NOT NULL DEFAULT 0,
    used_bytes_observed_at TIMESTAMPTZ,
    encrypt_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    versioning_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    object_locking_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    replication_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    retention_days INTEGER NOT NULL DEFAULT 0,
    legal_hold_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    tags JSONB NOT NULL DEFAULT '{}',
    lifecycle_rules JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ux_tenant_buckets_name UNIQUE (name),
    CONSTRAINT ck_tenant_buckets_status
        CHECK (status IN ('PROVISIONING', 'READY', 'UPDATING', 'DELETING', 'FAILED')),
    CONSTRAINT fk_tenant_buckets_workspace
        FOREIGN KEY (workspace_id)
        REFERENCES hierarchy.tenant_workspaces(id) ON DELETE RESTRICT
);

CREATE INDEX idx_tenant_buckets_tenant_zone
    ON tenant_buckets(tenant_id, zone_id);

CREATE TABLE personal_credentials (
    id UUID PRIMARY KEY,
    bucket_id UUID NOT NULL,
    access_key VARCHAR(255) NOT NULL,
    policy TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_personal_credentials_bucket
        FOREIGN KEY (bucket_id) REFERENCES personal_buckets(id) ON DELETE CASCADE,
    CONSTRAINT ux_personal_credentials_access_key UNIQUE (access_key)
);

CREATE INDEX idx_personal_credentials_bucket
    ON personal_credentials(bucket_id);

CREATE TABLE tenant_credentials (
    id UUID PRIMARY KEY,
    bucket_id UUID NOT NULL,
    access_key VARCHAR(255) NOT NULL,
    policy TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_tenant_credentials_bucket
        FOREIGN KEY (bucket_id) REFERENCES tenant_buckets(id) ON DELETE CASCADE,
    CONSTRAINT ux_tenant_credentials_access_key UNIQUE (access_key)
);

CREATE INDEX idx_tenant_credentials_bucket
    ON tenant_credentials(bucket_id);
