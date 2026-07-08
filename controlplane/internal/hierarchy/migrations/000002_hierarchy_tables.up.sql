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

-- [COMMENT]: Bảng quản lý dịch vụ kích hoạt theo từng Zone (Zone Services)
CREATE TABLE IF NOT EXISTS zone_services (
    id UUID PRIMARY KEY,
    zone_id UUID NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
    service_type zone_service_type NOT NULL,
    desired_state BOOLEAN NOT NULL DEFAULT false, -- [COMMENT]: Trạng thái mong muốn (true: enable, false: disable)
    actual_state zone_service_status NOT NULL DEFAULT 'unknown', -- [COMMENT]: Trạng thái vận hành thực tế
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
