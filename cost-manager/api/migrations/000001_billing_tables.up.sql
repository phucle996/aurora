-- Migration 000001: Khởi tạo 3 bảng dữ liệu cốt lõi: packs, plans, subscriptions

CREATE SCHEMA IF NOT EXISTS billing;

-- 1. Bảng Packs (Gói thương mại giải pháp: Student, Free Tier, Enterprise)
CREATE TABLE IF NOT EXISTS billing.packs (
    id            UUID PRIMARY KEY,
    name          VARCHAR(128) NOT NULL,
    code          VARCHAR(64)  NOT NULL UNIQUE, -- VD: 'STUDENT_PACK', 'FREE_TIER', 'ENTERPRISE_PACK'
    tier_target   VARCHAR(32)  NOT NULL DEFAULT 'FREE_TIER', -- 'STUDENT', 'FREE_TIER', 'ENTERPRISE'
    monthly_price NUMERIC(16, 4) NOT NULL DEFAULT 0.0000, -- Phí trọn gói hàng tháng (VND)
    currency      CHAR(3)      NOT NULL DEFAULT 'VND',
    discount_rate NUMERIC(5, 2)  NOT NULL DEFAULT 0.00,  -- Tỷ lệ giảm giá (%)
    status        VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE' | 'DEPRECATED'
    description   TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 2. Bảng Plans (Resource SKU Plans: Storage 10GB, VPS 2C-4G)
CREATE TABLE IF NOT EXISTS billing.plans (
    id            UUID PRIMARY KEY,
    name          VARCHAR(128) NOT NULL,
    code          VARCHAR(64)  NOT NULL UNIQUE, -- VD: 'STORAGE_SKU_10GB', 'VM_SKU_2C4G'
    service_type  VARCHAR(32)  NOT NULL,        -- 'STORAGE' | 'VM' | 'MAIL'
    zone_code     VARCHAR(32)  NOT NULL DEFAULT 'global',
    monthly_price NUMERIC(16, 4) NOT NULL DEFAULT 0.0000, -- Đơn giá gốc lẻ SKU (VND)
    currency      CHAR(3)      NOT NULL DEFAULT 'VND',
    status        VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE' | 'DEPRECATED'
    description   TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 3. Bảng Pack_Plans (Liên kết N:N giữa Pack và Resource SKU Plan)
CREATE TABLE IF NOT EXISTS billing.pack_plans (
    id                 UUID PRIMARY KEY,
    pack_id            UUID NOT NULL REFERENCES billing.packs(id) ON DELETE CASCADE,
    plan_id            UUID NOT NULL REFERENCES billing.plans(id) ON DELETE RESTRICT,
    included_quota     NUMERIC(18, 4) NOT NULL DEFAULT 0, -- Quota đi kèm trong gói
    overage_unit_price NUMERIC(16, 6) NOT NULL DEFAULT 0, -- Đơn giá phụ trội khi dùng vượt mốc
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_pack_plan UNIQUE (pack_id, plan_id)
);

-- 4. Bảng Subscriptions (Đăng ký gói Pack của Tenant/User)
CREATE TABLE IF NOT EXISTS billing.subscriptions (
    id              UUID        PRIMARY KEY,
    owner_id        UUID        NOT NULL,
    owner_type      VARCHAR(32) NOT NULL, -- 'personal' | 'tenant'
    pack_id         UUID        NOT NULL REFERENCES billing.packs(id), -- Tham chiếu đến Pack
    status          VARCHAR(16) NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE' | 'CANCELLED' | 'EXPIRED'
    version         INT         NOT NULL DEFAULT 1,        -- Version phòng Race Condition (OCC)
    idempotency_key VARCHAR(128),                           -- Khóa Idempotency Key
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ,
    renewed_at      TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_active_owner_sub UNIQUE (owner_id, owner_type)
);
