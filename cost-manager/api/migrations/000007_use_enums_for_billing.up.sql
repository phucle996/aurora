-- 1. Định nghĩa các kiểu ENUM trong schema billing
CREATE TYPE billing.owner_type AS ENUM ('personal', 'tenant');
CREATE TYPE billing.wallet_status AS ENUM ('ACTIVE', 'SUSPENDED');
CREATE TYPE billing.tx_type AS ENUM ('DEPOSIT', 'USAGE_CHARGE', 'REFUND');
CREATE TYPE billing.service_type AS ENUM ('STORAGE', 'VM', 'MAIL', 'SYSTEM');
CREATE TYPE billing.metric_type AS ENUM ('STORAGE_AT_REST', 'EGRESS_INTERNET', 'EGRESS_CROSS_ZONE', 'REQUEST_WRITE', 'REQUEST_READ', 'VCPU_USAGE', 'RAM_USAGE');
CREATE TYPE billing.unit_type AS ENUM ('GB', 'GB_HOUR', 'PER_1K_OPS', 'CORE_HOUR', 'RAM_GB_HOUR');
CREATE TYPE billing.tier_type AS ENUM ('STANDARD', 'COLD', 'ARCHIVE');
CREATE TYPE billing.plan_status AS ENUM ('ACTIVE', 'DEPRECATED');
CREATE TYPE billing.sub_status AS ENUM ('ACTIVE', 'CANCELLED', 'EXPIRED');

-- 2. Chuyển đổi các cột trong bảng wallets sang ENUM
ALTER TABLE billing.wallets ALTER COLUMN owner_type TYPE billing.owner_type USING owner_type::billing.owner_type;
ALTER TABLE billing.wallets ALTER COLUMN status DROP DEFAULT;
ALTER TABLE billing.wallets ALTER COLUMN status TYPE billing.wallet_status USING status::billing.wallet_status;
ALTER TABLE billing.wallets ALTER COLUMN status SET DEFAULT 'ACTIVE'::billing.wallet_status;

-- 3. Chuyển đổi các cột trong bảng transactions sang ENUM
ALTER TABLE billing.transactions ALTER COLUMN tx_type TYPE billing.tx_type USING tx_type::billing.tx_type;
ALTER TABLE billing.transactions ALTER COLUMN service_type TYPE billing.service_type USING service_type::billing.service_type;

-- 4. Chuyển đổi các cột trong bảng prices sang ENUM
ALTER TABLE billing.prices ALTER COLUMN service_type TYPE billing.service_type USING service_type::billing.service_type;

-- metric_type drop default before alter to avoid cast error
ALTER TABLE billing.prices ALTER COLUMN metric_type DROP DEFAULT;
ALTER TABLE billing.prices ALTER COLUMN metric_type TYPE billing.metric_type USING metric_type::billing.metric_type;
ALTER TABLE billing.prices ALTER COLUMN metric_type SET DEFAULT 'STORAGE_AT_REST'::billing.metric_type;

ALTER TABLE billing.prices ALTER COLUMN unit DROP DEFAULT;
ALTER TABLE billing.prices ALTER COLUMN unit TYPE billing.unit_type USING unit::billing.unit_type;
ALTER TABLE billing.prices ALTER COLUMN unit SET DEFAULT 'GB_HOUR'::billing.unit_type;

ALTER TABLE billing.prices ALTER COLUMN tier DROP DEFAULT;
ALTER TABLE billing.prices ALTER COLUMN tier TYPE billing.tier_type USING tier::billing.tier_type;
ALTER TABLE billing.prices ALTER COLUMN tier SET DEFAULT 'STANDARD'::billing.tier_type;

-- 5. Chuyển đổi các cột trong bảng plans sang ENUM
ALTER TABLE billing.plans ALTER COLUMN service_type TYPE billing.service_type USING service_type::billing.service_type;
ALTER TABLE billing.plans ALTER COLUMN status DROP DEFAULT;
ALTER TABLE billing.plans ALTER COLUMN status TYPE billing.plan_status USING status::billing.plan_status;
ALTER TABLE billing.plans ALTER COLUMN status SET DEFAULT 'ACTIVE'::billing.plan_status;

-- 6. Chuyển đổi các cột trong bảng plan_metrics sang ENUM
ALTER TABLE billing.plan_metrics ALTER COLUMN metric_type TYPE billing.metric_type USING metric_type::billing.metric_type;
ALTER TABLE billing.plan_metrics ALTER COLUMN unit TYPE billing.unit_type USING unit::billing.unit_type;

-- 7. Chuyển đổi các cột trong bảng subscriptions sang ENUM
ALTER TABLE billing.subscriptions ALTER COLUMN owner_type TYPE billing.owner_type USING owner_type::billing.owner_type;
ALTER TABLE billing.subscriptions ALTER COLUMN status DROP DEFAULT;
ALTER TABLE billing.subscriptions ALTER COLUMN status TYPE billing.sub_status USING status::billing.sub_status;
ALTER TABLE billing.subscriptions ALTER COLUMN status SET DEFAULT 'ACTIVE'::billing.sub_status;
