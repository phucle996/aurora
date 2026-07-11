-- ======================================================================================================
-- 📂 MIGRATION: 000001_storage_tables.up.sql
--            Storage Module — Table Definitions & Constraints
-- ======================================================================================================

-- [COMMENT]: Bảng lưu trữ thông tin cấu hình và siêu dữ liệu của Object Storage Buckets cá nhân.
CREATE TABLE IF NOT EXISTS personal_buckets (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL, -- Tên bucket vật lý (Unique toàn cục trên cluster MinIO/S3)
    workspace_id UUID NOT NULL, -- Tham chiếu vật lý tới hierarchy.personal_workspaces(id)
    zone_id UUID NOT NULL,      -- Tham chiếu logic tới hierarchy.zones(id)
    status VARCHAR(50) NOT NULL DEFAULT 'creating', -- Trạng thái vòng đời (creating, active, suspended, deleted)
    capacity_quota_bytes BIGINT NOT NULL DEFAULT 0, -- Hạn mức dung lượng tối đa (Bytes)
    used_bytes BIGINT NOT NULL DEFAULT 0,          -- Dung lượng thực tế đang sử dụng (Bytes)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT ux_personal_buckets_name UNIQUE (name),
    CONSTRAINT fk_personal_buckets_workspace FOREIGN KEY (workspace_id) REFERENCES hierarchy.personal_workspaces(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_personal_buckets_workspace ON personal_buckets(workspace_id);

-- [COMMENT]: Bảng lưu trữ thông tin cấu hình và siêu dữ liệu của Object Storage Buckets doanh nghiệp.
CREATE TABLE IF NOT EXISTS tenant_buckets (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL, -- Tên bucket vật lý (Unique toàn cục trên cluster MinIO/S3)
    workspace_id UUID NOT NULL, -- Tham chiếu vật lý tới hierarchy.tenant_workspaces(id)
    zone_id UUID NOT NULL,      -- Tham chiếu logic tới hierarchy.zones(id)
    tenant_id UUID NOT NULL,    -- Tham chiếu logic tới hierarchy.tenants(id)
    status VARCHAR(50) NOT NULL DEFAULT 'creating', -- Trạng thái vòng đời (creating, active, suspended, deleted)
    capacity_quota_bytes BIGINT NOT NULL DEFAULT 0, -- Hạn mức dung lượng tối đa (Bytes)
    used_bytes BIGINT NOT NULL DEFAULT 0,          -- Dung lượng thực tế đang sử dụng (Bytes)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT ux_tenant_buckets_name UNIQUE (name),
    CONSTRAINT fk_tenant_buckets_workspace FOREIGN KEY (workspace_id) REFERENCES hierarchy.tenant_workspaces(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_tenant_buckets_tenant_zone ON tenant_buckets(tenant_id, zone_id);

-- [COMMENT]: Bảng lưu trữ thông tin tài khoản truy cập (Access Keys) của từng Bucket cá nhân.
CREATE TABLE IF NOT EXISTS personal_credentials (
    id UUID PRIMARY KEY,
    bucket_id UUID NOT NULL, -- Khóa ngoại trỏ trực tiếp đến bảng personal_buckets
    access_key VARCHAR(255) NOT NULL, -- Mã access key truy cập MinIO
    secret_key TEXT NOT NULL,         -- Secret Key (mã hóa)
    policy TEXT NOT NULL,             -- JSON policy
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_personal_credentials_bucket FOREIGN KEY (bucket_id) REFERENCES personal_buckets(id) ON DELETE CASCADE,
    CONSTRAINT ux_personal_credentials_access_key UNIQUE (access_key)
);

CREATE INDEX IF NOT EXISTS idx_personal_credentials_bucket ON personal_credentials(bucket_id);

-- [COMMENT]: Bảng lưu trữ thông tin tài khoản truy cập (Access Keys) của từng Bucket doanh nghiệp.
CREATE TABLE IF NOT EXISTS tenant_credentials (
    id UUID PRIMARY KEY,
    bucket_id UUID NOT NULL, -- Khóa ngoại trỏ trực tiếp đến bảng tenant_buckets
    access_key VARCHAR(255) NOT NULL, -- Mã access key truy cập MinIO
    secret_key TEXT NOT NULL,         -- Secret Key (mã hóa)
    policy TEXT NOT NULL,             -- JSON policy
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_tenant_credentials_bucket FOREIGN KEY (bucket_id) REFERENCES tenant_buckets(id) ON DELETE CASCADE,
    CONSTRAINT ux_tenant_credentials_access_key UNIQUE (access_key)
);

CREATE INDEX IF NOT EXISTS idx_tenant_credentials_bucket ON tenant_credentials(bucket_id);
