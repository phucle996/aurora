-- 1. Quản lý ví tiền của khách hàng
CREATE TABLE IF NOT EXISTS billing.wallets (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL,          -- ID của User hoặc Tenant/Workspace
    owner_type VARCHAR(32) NOT NULL, -- 'personal' hoặc 'tenant'
    balance NUMERIC(16, 4) NOT NULL DEFAULT 0.0000, -- Số dư ví tiền thực tế (VND)
    currency VARCHAR(3) NOT NULL DEFAULT 'VND',
    overdraft_limit NUMERIC(16, 4) NOT NULL DEFAULT 0.0000, -- Hạn mức nợ cho phép
    status VARCHAR(32) NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE', 'SUSPENDED' (Hết tiền/Bị khóa)
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_owner UNIQUE (owner_id, owner_type)
);

-- 2. Nhật ký giao dịch ví tiền (Double-Entry Ledger)
CREATE TABLE IF NOT EXISTS billing.transactions (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL REFERENCES billing.wallets(id),
    amount NUMERIC(16, 4) NOT NULL,    -- Giá trị giao dịch (Dương là Nạp tiền, Âm là Trừ cước)
    tx_type VARCHAR(32) NOT NULL,      -- 'DEPOSIT', 'USAGE_CHARGE', 'REFUND'
    service_type VARCHAR(32) NOT NULL, -- 'STORAGE', 'MAIL', 'VM', 'SYSTEM'
    reference_id VARCHAR(128),         -- Mã tham chiếu
    description TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 3. Bảng cấu hình đơn giá các dịch vụ (Pricing Table)
CREATE TABLE IF NOT EXISTS billing.prices (
    id           UUID PRIMARY KEY,
    service_type VARCHAR(32) NOT NULL,  -- 'STORAGE', 'VM', 'MAIL'
    metric_type  VARCHAR(32) NOT NULL,  -- 'STORAGE_AT_REST' | 'EGRESS_INTERNET' | 'EGRESS_CROSS_ZONE' | 'REQUEST_WRITE' | 'REQUEST_READ'
    zone_code    VARCHAR(32) NOT NULL DEFAULT 'global', -- vn-n1, vn-n2, global
    unit         VARCHAR(32) NOT NULL DEFAULT 'GB_HOUR', -- 'GB_HOUR' | 'GB' | 'PER_1K_OPS'
    unit_price   NUMERIC(16, 6) NOT NULL,  -- Đơn giá (VND)
    currency     VARCHAR(3)  NOT NULL DEFAULT 'VND',
    tier         VARCHAR(32) NOT NULL DEFAULT 'STANDARD', -- 'STANDARD' | 'COLD' | 'ARCHIVE'
    free_quota   NUMERIC(18, 4) NOT NULL DEFAULT 0, -- Quota miễn phí (pay-as-you-go free tier)
    effective_from TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    effective_to   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    -- Unique: 1 đơn giá cho mỗi tổ hợp (service_type, metric_type, zone_code, tier)
    CONSTRAINT uq_price UNIQUE (service_type, metric_type, zone_code, tier)
);
