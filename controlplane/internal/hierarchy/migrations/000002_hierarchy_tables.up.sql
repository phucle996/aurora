-- ======================================================================================================
-- 📂 MIGRATION: 000002_hierarchy_tables.up.sql
--            Hierarchy/Hierarchy Module — Table Definitions & In-Table Constraints
-- ======================================================================================================

-- [COMMENT]: Bảng danh mục các Vùng Hạ tầng (Zones)
CREATE TABLE IF NOT EXISTS zones (
    id UUID PRIMARY KEY,
    code TEXT NOT NULL,
    name TEXT NOT NULL,
    location TEXT NOT NULL,
    description TEXT NULL,
    status zone_status NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE zones IS 'Zone catalog as independent edge location taxonomy used by placement and runtime decisions.';
COMMENT ON COLUMN zones.id IS 'Primary key of zone row. Must be generated as UUIDv7 by application/service layer.';
COMMENT ON COLUMN zones.code IS 'Stable unique zone code, for example edge-hcm-1.';
COMMENT ON COLUMN zones.name IS 'Human-readable zone display name.';
COMMENT ON COLUMN zones.location IS 'Human-readable physical location of the zone.';
COMMENT ON COLUMN zones.description IS 'Optional description of the zone purpose and operational notes.';
COMMENT ON COLUMN zones.status IS 'Operational status of zone lifecycle (planned, active, draining, maintenance, disabled).';
COMMENT ON COLUMN zones.created_at IS 'Timestamp when zone row was created.';
COMMENT ON COLUMN zones.updated_at IS 'Timestamp when zone row was last updated.';

-- [COMMENT]: Versioned public HPKE capability registered by SRE for one Zone.
-- Private counterparts are materialized only at Dataplane filesystem boundary
-- and must never be added to this table, PostgreSQL, Kafka or Zone KV.
CREATE TABLE IF NOT EXISTS zone_encryption_keys (
    id UUID PRIMARY KEY,
    zone_id UUID NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
    public_key BYTEA NOT NULL,
    fingerprint BYTEA NOT NULL,
    algorithm TEXT NOT NULL DEFAULT 'HPKE_X25519_HKDF_SHA256_AES_256_GCM',
    status zone_encryption_key_status NOT NULL DEFAULT 'staged',
    registered_by TEXT NOT NULL,
    registered_proof_id UUID NOT NULL,
    activated_by TEXT NULL,
    activated_proof_id UUID NULL,
    decrypt_only_by TEXT NULL,
    decrypt_only_proof_id UUID NULL,
    retired_by TEXT NULL,
    retired_proof_id UUID NULL,
    activated_at TIMESTAMPTZ NULL,
    decrypt_only_at TIMESTAMPTZ NULL,
    retired_at TIMESTAMPTZ NULL,
    loaded_at TIMESTAMPTZ NULL,
    loaded_observed_at TIMESTAMPTZ NULL,
    loaded_observed_fencing_token BIGINT NULL CHECK (loaded_observed_fencing_token > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_zone_encryption_keys_public_key_size CHECK (octet_length(public_key) = 32),
    CONSTRAINT ck_zone_encryption_keys_fingerprint_size CHECK (octet_length(fingerprint) = 32),
    CONSTRAINT ck_zone_encryption_keys_algorithm CHECK (algorithm = 'HPKE_X25519_HKDF_SHA256_AES_256_GCM'),
    CONSTRAINT ck_zone_encryption_keys_registered_actor CHECK (length(btrim(registered_by)) BETWEEN 1 AND 128),
    CONSTRAINT ck_zone_encryption_keys_lifecycle CHECK (
        (status = 'staged' AND activated_at IS NULL AND decrypt_only_at IS NULL AND retired_at IS NULL)
        OR (status = 'active' AND activated_at IS NOT NULL AND activated_by IS NOT NULL AND activated_proof_id IS NOT NULL AND decrypt_only_at IS NULL AND retired_at IS NULL)
        OR (status = 'decrypt_only' AND activated_at IS NOT NULL AND decrypt_only_at IS NOT NULL AND decrypt_only_by IS NOT NULL AND decrypt_only_proof_id IS NOT NULL AND retired_at IS NULL)
        OR (status = 'retired' AND retired_at IS NOT NULL AND retired_by IS NOT NULL AND retired_proof_id IS NOT NULL)
    )
);

COMMENT ON TABLE zone_encryption_keys IS 'Versioned public HPKE keys for Zone-bound protected job payloads; never stores a private key.';
COMMENT ON COLUMN zone_encryption_keys.id IS 'UUIDv7 public key identifier exposed as key_id in transport metadata.';
COMMENT ON COLUMN zone_encryption_keys.zone_id IS 'Zone receiving ciphertext sealed to this public key.';
COMMENT ON COLUMN zone_encryption_keys.public_key IS 'Exactly 32 raw X25519 public-key bytes; not secret material.';
COMMENT ON COLUMN zone_encryption_keys.fingerprint IS 'SHA-256 of raw public_key; globally unique to prevent accidental cross-Zone key-pair reuse.';
COMMENT ON COLUMN zone_encryption_keys.algorithm IS 'Non-negotiable V1 HPKE suite.';
COMMENT ON COLUMN zone_encryption_keys.status IS 'Lifecycle state: staged, active, decrypt_only or retired.';
COMMENT ON COLUMN zone_encryption_keys.registered_proof_id IS 'ACR critical-proof challenge bound to registration request.';
COMMENT ON COLUMN zone_encryption_keys.activated_proof_id IS 'ACR critical-proof challenge bound to activation request.';
COMMENT ON COLUMN zone_encryption_keys.decrypt_only_proof_id IS 'Proof that activated the replacement key and atomically demoted this key.';
COMMENT ON COLUMN zone_encryption_keys.retired_proof_id IS 'ACR critical-proof challenge bound to retirement request.';
COMMENT ON COLUMN zone_encryption_keys.loaded_at IS 'Latest trusted Zone report timestamp that proved the matching private key was loaded; NULL means not ready.';
COMMENT ON COLUMN zone_encryption_keys.loaded_observed_at IS 'Monotonic report fence, including reports where this key was absent, so an older leader cannot resurrect readiness.';
COMMENT ON COLUMN zone_encryption_keys.loaded_observed_fencing_token IS 'Zone leader fencing token paired with loaded_observed_at; lower-token reports cannot resurrect readiness after failover.';

-- [COMMENT]: Bảng quản lý dịch vụ kích hoạt theo từng Zone (Zone Services)
CREATE TABLE IF NOT EXISTS zone_services (
    id UUID PRIMARY KEY,
    zone_id UUID NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
    service_type zone_service_type NOT NULL,
    desired_state BOOLEAN NOT NULL DEFAULT false, -- [COMMENT]: Trạng thái mong muốn (true: enable, false: disable)
    actual_state zone_service_status NOT NULL DEFAULT 'unknown', -- [COMMENT]: Trạng thái vận hành thực tế
    actual_observed_at TIMESTAMPTZ NULL, -- [COMMENT]: Fence chống report Zone cũ rollback actual_state trong HA
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_zone_services_zone_service UNIQUE (zone_id, service_type)
);

COMMENT ON TABLE zone_services IS 'Per-zone service availability matrix indicating desired and actual status of services.';
COMMENT ON COLUMN zone_services.id IS 'Primary key of zone service row. Must be generated as UUIDv7 by application/service layer.';
COMMENT ON COLUMN zone_services.zone_id IS 'Foreign key to zone that owns this service.';
COMMENT ON COLUMN zone_services.service_type IS 'Service type supported in zone, for example mail or hypervisor.';
COMMENT ON COLUMN zone_services.desired_state IS 'Desired state indicating if service is enabled (true) or disabled (false) for this zone.';
COMMENT ON COLUMN zone_services.actual_state IS 'Actual operational health state of service inside this zone.';
COMMENT ON COLUMN zone_services.actual_observed_at IS 'Source observation timestamp; only a newer Zone report may replace actual_state.';
COMMENT ON COLUMN zone_services.created_at IS 'Timestamp when zone service row was created.';
COMMENT ON COLUMN zone_services.updated_at IS 'Timestamp when zone service row was last updated.';

-- [COMMENT]: Bảng quản lý Tổ chức / Doanh nghiệp (Tenants)
CREATE TABLE IF NOT EXISTS tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(100) NOT NULL,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_tenants_code UNIQUE (code),
    CONSTRAINT ck_tenants_code_format CHECK (code ~ '^[a-z0-9-_]+$')
);

COMMENT ON TABLE tenants IS 'Bảng lưu trữ thông tin các Tổ chức / Doanh nghiệp sử dụng dịch vụ.';
COMMENT ON COLUMN tenants.id IS 'ID định danh duy nhất của Tenant.';
COMMENT ON COLUMN tenants.code IS 'Mã viết tắt định danh duy nhất của Tenant để tạo slug/namespace (ví dụ: acme).';
COMMENT ON COLUMN tenants.name IS 'Tên của doanh nghiệp/tổ chức.';
COMMENT ON COLUMN tenants.status IS 'Trạng thái hoạt động của Tenant (active, suspended, deleted).';

-- [COMMENT]: Bảng quản lý các Tên miền liên kết với Tổ chức (Tenant Domains)
CREATE TABLE IF NOT EXISTS tenant_domains (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    domain VARCHAR(255) NOT NULL,
    is_primary BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE tenant_domains IS 'Bảng lưu trữ các tên miền (domain) thuộc sở hữu của Tenant để phục vụ cấu hình xác thực/định tuyến.';
COMMENT ON COLUMN tenant_domains.id IS 'ID định danh duy nhất của bản ghi tên miền.';
COMMENT ON COLUMN tenant_domains.tenant_id IS 'ID của Tenant liên kết.';
COMMENT ON COLUMN tenant_domains.domain IS 'Địa chỉ domain liên kết (ví dụ: acme.com).';
COMMENT ON COLUMN tenant_domains.is_primary IS 'Đánh dấu đây có phải là tên miền chính thức của Tenant hay không.';

-- [COMMENT]: Bảng quản lý Thành viên trong Tổ chức (Tenant Memberships)
CREATE TABLE IF NOT EXISTS tenant_memberships (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    is_ownership BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE tenant_memberships IS 'Bảng liên kết người dùng (Users) với Tổ chức (Tenants).';
COMMENT ON COLUMN tenant_memberships.id IS 'ID định danh duy nhất của thành viên.';
COMMENT ON COLUMN tenant_memberships.tenant_id IS 'ID của Tenant sở hữu thành viên này.';
COMMENT ON COLUMN tenant_memberships.user_id IS 'ID của User liên kết (từ schema iam).';
COMMENT ON COLUMN tenant_memberships.status IS 'Trạng thái của thành viên (active, suspended, disabled).';

-- [COMMENT]: Bảng quản lý Không gian làm việc cá nhân (Personal Workspaces)
CREATE TABLE IF NOT EXISTS personal_workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    code VARCHAR(100) NOT NULL,
    description TEXT NULL,
    zone_id UUID NOT NULL REFERENCES zones(id) ON DELETE RESTRICT,
    owner_id UUID NOT NULL, -- references users(id) ở schema iam
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_personal_workspaces_code_format CHECK (code ~ '^[a-z0-9-_]+$')
);

COMMENT ON TABLE personal_workspaces IS 'Bảng quản lý các không gian làm việc cá nhân, độc lập với doanh nghiệp.';
COMMENT ON COLUMN personal_workspaces.id IS 'ID định danh duy nhất của Workspace.';
COMMENT ON COLUMN personal_workspaces.name IS 'Tên hiển thị của Workspace.';
COMMENT ON COLUMN personal_workspaces.code IS 'Mã viết tắt định danh duy nhất của Workspace.';
COMMENT ON COLUMN personal_workspaces.description IS 'Optional description of the workspace.';
COMMENT ON COLUMN personal_workspaces.zone_id IS 'ID của Zone mà Workspace này thuộc về (bắt buộc).';
COMMENT ON COLUMN personal_workspaces.owner_id IS 'ID của User sở hữu Workspace cá nhân này.';

-- [COMMENT]: Bảng quản lý Không gian làm việc doanh nghiệp (Tenant Workspaces)
CREATE TABLE IF NOT EXISTS tenant_workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    code VARCHAR(100) NOT NULL,
    description TEXT NULL,
    zone_id UUID NOT NULL REFERENCES zones(id) ON DELETE RESTRICT,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    owner_id UUID NOT NULL, -- Người tạo workspace, references users(id) ở schema iam
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_tenant_workspaces_code_format CHECK (code ~ '^[a-z0-9-_]+$')
);

COMMENT ON TABLE tenant_workspaces IS 'Bảng quản lý các không gian làm việc thuộc sở hữu của doanh nghiệp (Tenant).';
COMMENT ON COLUMN tenant_workspaces.id IS 'ID định danh duy nhất của Workspace.';
COMMENT ON COLUMN tenant_workspaces.name IS 'Tên hiển thị của Workspace.';
COMMENT ON COLUMN tenant_workspaces.code IS 'Mã viết tắt định danh duy nhất của Workspace trong phạm vi Tenant.';
COMMENT ON COLUMN tenant_workspaces.description IS 'Optional description of the workspace.';
COMMENT ON COLUMN tenant_workspaces.zone_id IS 'ID của Zone mà Workspace này thuộc về (bắt buộc).';
COMMENT ON COLUMN tenant_workspaces.tenant_id IS 'ID của Tenant sở hữu Workspace này (NOT NULL).';
COMMENT ON COLUMN tenant_workspaces.owner_id IS 'ID của User tạo ra Workspace trong Tenant.';
