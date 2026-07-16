-- Migration: plans, plan_metrics, subscriptions
-- Các bảng hỗ trợ mô hình subscription (gói cước tháng)

-- 1. Bảng định nghĩa gói cước
CREATE TABLE IF NOT EXISTS billing.plans (
    id            UUID PRIMARY KEY,
    name          VARCHAR(128) NOT NULL,
    code          VARCHAR(64)  NOT NULL UNIQUE, -- 'STORAGE_BASIC_VN1', 'STORAGE_PRO_VN1'
    service_type  VARCHAR(32)  NOT NULL,        -- 'STORAGE' | 'VM' | 'MAIL'
    zone_code     VARCHAR(32)  NOT NULL DEFAULT 'global',
    monthly_price NUMERIC(16, 4) NOT NULL,      -- Phí gói cố định mỗi tháng (VND)
    currency      CHAR(3)      NOT NULL DEFAULT 'VND',
    status        VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE' | 'DEPRECATED'
    description   TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 2. Quota của từng metric trong gói
--    Mỗi plan có nhiều dòng plan_metrics (storage, egress, requests,...)
CREATE TABLE IF NOT EXISTS billing.plan_metrics (
    id          UUID PRIMARY KEY,
    plan_id     UUID        NOT NULL REFERENCES billing.plans(id) ON DELETE CASCADE,
    metric_type VARCHAR(32) NOT NULL, -- 'STORAGE_AT_REST' | 'EGRESS_INTERNET' | 'REQUEST_WRITE' | ...
    quota       NUMERIC(18, 4) NOT NULL, -- Số lượng quota theo đơn vị (vd: 50 GB, 10 GB, 10000 ops)
    unit        VARCHAR(32) NOT NULL     -- 'GB' | 'GB_HOUR' | 'PER_1K_OPS'
);

-- 3. Bảng subscription — owner đăng ký gói nào
CREATE TABLE IF NOT EXISTS billing.subscriptions (
    id          UUID        PRIMARY KEY,
    owner_id    UUID        NOT NULL,
    owner_type  VARCHAR(32) NOT NULL,  -- 'personal' | 'tenant'
    plan_id     UUID        NOT NULL REFERENCES billing.plans(id),
    status      VARCHAR(16) NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE' | 'CANCELLED' | 'EXPIRED'
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ,           -- NULL = không tự hết hạn
    renewed_at  TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Mỗi owner chỉ có 1 subscription active mỗi lúc
    CONSTRAINT uq_active_sub UNIQUE (owner_id, owner_type)
);
