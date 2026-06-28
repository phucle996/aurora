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
    enabled BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_zone_services_zone_service UNIQUE (zone_id, service_type)
);

COMMENT ON TABLE zone_services IS 'Per-zone service availability matrix indicating whether each service type is enabled in a specific zone.';
COMMENT ON COLUMN zone_services.id IS 'Primary key of zone service row. Must be generated as UUIDv7 by application/service layer.';
COMMENT ON COLUMN zone_services.zone_id IS 'Foreign key to zone that owns this service availability flag.';
COMMENT ON COLUMN zone_services.service_type IS 'Service type supported in zone, for example mail or hypervisor.';
COMMENT ON COLUMN zone_services.enabled IS 'Whether the given service type is enabled for this zone.';
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
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE tenant_memberships IS 'Bảng liên kết người dùng (Users) với Tổ chức (Tenants).';
COMMENT ON COLUMN tenant_memberships.id IS 'ID định danh duy nhất của thành viên.';
COMMENT ON COLUMN tenant_memberships.tenant_id IS 'ID của Tenant sở hữu thành viên này.';
COMMENT ON COLUMN tenant_memberships.user_id IS 'ID của User liên kết (từ schema iam).';
COMMENT ON COLUMN tenant_memberships.status IS 'Trạng thái của thành viên (active, suspended, disabled).';


-- [COMMENT]: Bảng quản lý Không gian làm việc (Workspaces)
-- Một workspace bắt buộc phải thuộc về 1 Zone cụ thể và có thể liên kết với 1 Tenant (doanh nghiệp) hoặc độc lập (cá nhân).
CREATE TABLE IF NOT EXISTS workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    code VARCHAR(100) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    zone_id UUID NOT NULL REFERENCES zones(id) ON DELETE RESTRICT,
    tenant_id UUID NULL REFERENCES tenants(id) ON DELETE CASCADE,
    owner_id UUID NOT NULL, -- references users(id) ở schema iam
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_workspaces_code_format CHECK (code ~ '^[a-z0-9-_]+$')
);

COMMENT ON TABLE workspaces IS 'Bảng quản lý các không gian làm việc (Workspaces), đơn vị chứa tài nguyên hạ tầng của khách hàng.';
COMMENT ON COLUMN workspaces.id IS 'ID định danh duy nhất của Workspace.';
COMMENT ON COLUMN workspaces.name IS 'Tên hiển thị của Workspace.';
COMMENT ON COLUMN workspaces.code IS 'Mã viết tắt định danh duy nhất của Workspace trong phạm vi Tenant/Owner.';
COMMENT ON COLUMN workspaces.status IS 'Trạng thái hoạt động của Workspace (active, suspended, deleted).';
COMMENT ON COLUMN workspaces.zone_id IS 'ID của Zone mà Workspace này thuộc về (bắt buộc).';
COMMENT ON COLUMN workspaces.tenant_id IS 'ID của Tenant sở hữu Workspace này (NULL nếu là Workspace cá nhân).';
COMMENT ON COLUMN workspaces.owner_id IS 'ID của User sở hữu Workspace (đối với Workspace cá nhân hoặc người tạo).';
