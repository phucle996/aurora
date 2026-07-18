-- Migration 000001: Khởi tạo schema và cấu trúc bảng phục vụ cơ chế tính cước lũy tiến (Tiered Billing) theo Zone

CREATE SCHEMA IF NOT EXISTS billing;

-- 1. Bảng Packs (Gói giải pháp thương mại tổng hợp: Student, Free Tier, Enterprise)
CREATE TABLE IF NOT EXISTS billing.packs (
    id            UUID PRIMARY KEY,
    name          VARCHAR(128) NOT NULL,
    code          VARCHAR(64)  NOT NULL UNIQUE, -- VD: 'STUDENT_PACK', 'FREE_TIER', 'ENTERPRISE_PACK'
    tier_target   VARCHAR(32)  NOT NULL DEFAULT 'FREE_TIER', -- Mục tiêu áp dụng: 'STUDENT', 'FREE_TIER', 'ENTERPRISE'
    monthly_price BIGINT       NOT NULL DEFAULT 0, -- Phí trọn gói cơ bản hàng tháng (USD Micro-units)
    currency      CHAR(3)      NOT NULL DEFAULT 'USD',
    discount_rate NUMERIC(5, 2)  NOT NULL DEFAULT 0.00,  -- Tỷ lệ chiết khấu giảm giá (%)
    status        VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE', -- Trạng thái gói: 'ACTIVE' | 'DEPRECATED'
    description   TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 2. Bảng Tiers (Biểu giá cơ sở chung cho từng loại tài nguyên mà không phụ thuộc vào Zone)
CREATE TABLE IF NOT EXISTS billing.tiers (
    id            UUID PRIMARY KEY,
    name          VARCHAR(128) NOT NULL, -- VD: 'Standard Storage Base Tier'
    code          VARCHAR(64)  NOT NULL UNIQUE, -- VD: 'STORAGE_STD_BASE'
    service_type  VARCHAR(32)  NOT NULL, -- Loại tài nguyên: 'STORAGE' | 'VM' | 'NETWORK_OUT'
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 3. Bảng Tier_Ranges (Cấu hình nấc giá lũy tiến / lũy kế của từng biểu giá cơ sở)
CREATE TABLE IF NOT EXISTS billing.tier_ranges (
    id              UUID PRIMARY KEY,
    tier_id         UUID NOT NULL REFERENCES billing.tiers(id) ON DELETE CASCADE, -- Liên kết với Tiers gốc
    range_start     BIGINT NOT NULL DEFAULT 0, -- Mốc bắt đầu tính bằng Megabytes (MB)
    range_end       BIGINT NOT NULL DEFAULT 0, -- Mốc kết thúc tính bằng Megabytes (MB) (Quy ước: 0 là không giới hạn)
    base_unit_price BIGINT NOT NULL DEFAULT 0,              -- Giá gốc của nấc này (USD Micro-units/MB/Hour hoặc /GB/Hour)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 4. Bảng Plans (Resource SKU Plans - Lớp vỏ Abstraction liên kết Tiers với từng Zone và hệ số nhân giá vùng)
CREATE TABLE IF NOT EXISTS billing.plans (
    id              UUID PRIMARY KEY,
    name            VARCHAR(128) NOT NULL, -- VD: 'Standard Storage - VN Zone'
    code            VARCHAR(64)  NOT NULL UNIQUE, -- VD: 'STORAGE_SKU_VN'
    service_type    VARCHAR(32)  NOT NULL,        -- Loại dịch vụ hỗ trợ query: 'STORAGE' | 'VM' | 'NETWORK_OUT'
    zone_id         UUID         NOT NULL,        -- Liên kết tới Zone UUID tương ứng
    tier_id         UUID         NOT NULL REFERENCES billing.tiers(id), -- Tham chiếu tới Tiers chứa biểu giá cơ sở
    zone_multiplier NUMERIC(4, 2) NOT NULL DEFAULT 1.00,  -- Hệ số điều chỉnh giá theo vùng (VD: 1.05 = đắt hơn 5% so với giá base)
    status          VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE', -- Trạng thái plan: 'ACTIVE' | 'DEPRECATED'
    description     TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 5. Bảng Pack_Plans (Bảng liên kết N:N giữa Pack thương mại và các Resource SKU Plan)
CREATE TABLE IF NOT EXISTS billing.pack_plans (
    id                 UUID PRIMARY KEY,
    pack_id            UUID NOT NULL REFERENCES billing.packs(id) ON DELETE CASCADE, -- Liên kết với gói thương mại
    plan_id            UUID NOT NULL REFERENCES billing.plans(id) ON DELETE RESTRICT, -- Liên kết với plan tài nguyên
    included_quota     NUMERIC(18, 4) NOT NULL DEFAULT 0, -- Hạn mức định lượng miễn phí đi kèm (VD: 10GB storage)
    overage_unit_price BIGINT         NOT NULL DEFAULT 0, -- Giá phụ trội tính khi dùng quá định lượng (USD Micro-units)
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_pack_plan UNIQUE (pack_id, plan_id)
);

-- 6. Bảng Subscriptions (Đăng ký / mua gói Pack của các Tenant hoặc tài khoản cá nhân)
CREATE TABLE IF NOT EXISTS billing.subscriptions (
    id              UUID        PRIMARY KEY,
    owner_id        UUID        NOT NULL,
    owner_type      VARCHAR(32) NOT NULL, -- 'personal' | 'tenant'
    pack_id         UUID        NOT NULL REFERENCES billing.packs(id), -- Tham chiếu đến Pack đã chọn
    status          VARCHAR(16) NOT NULL DEFAULT 'ACTIVE', -- Trạng thái gói: 'ACTIVE' | 'CANCELLED' | 'EXPIRED'
    version         INT         NOT NULL DEFAULT 1,        -- Cột dùng cho Optimistic Concurrency Control (OCC)
    idempotency_key VARCHAR(128),                           -- Lưu khóa Idempotency tránh gửi trùng lặp yêu cầu
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ,
    renewed_at      TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_active_owner_sub UNIQUE (owner_id, owner_type)
);

-- 7. Bảng Users (Danh sách nhân viên kiểm toán/kế toán nội bộ có quyền truy cập hệ thống billing)
CREATE TABLE IF NOT EXISTS billing.users (
    id              UUID PRIMARY KEY,
    employee_code   VARCHAR(64)  NOT NULL UNIQUE, -- Mã tài khoản nhân viên dùng để xác thực đăng nhập
    public_key      VARCHAR(256) NOT NULL,        -- Khóa công khai Ed25519 dùng kiểm tra chữ ký token
    fullname        VARCHAR(128) NOT NULL,
    email           VARCHAR(128) NOT NULL UNIQUE,
    role_id         VARCHAR(64)  NOT NULL DEFAULT 'billing_auditor', -- Phân quyền vai trò: 'billing_admin' | 'billing_auditor'
    level           INT          NOT NULL DEFAULT 2,
    status          VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE', -- Trạng thái: 'ACTIVE' | 'DISABLED'
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
