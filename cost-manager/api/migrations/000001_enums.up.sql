-- Migration 000001: Khởi tạo schema và các kiểu dữ liệu Enum cơ sở
CREATE SCHEMA IF NOT EXISTS billing;

-- [COMMENT]: Enum service_type định nghĩa các loại dịch vụ tính cước
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON t.typnamespace = n.oid WHERE t.typname = 'service_type' AND n.nspname = 'billing') THEN
        CREATE TYPE billing.service_type AS ENUM ('STORAGE', 'NETWORK_IN', 'NETWORK_OUT', 'VM');
    END IF;
END $$;

-- [COMMENT]: Enum owner_type phân định chủ sở hữu tài nguyên (PERSONAL, TENANT)
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON t.typnamespace = n.oid WHERE t.typname = 'owner_type' AND n.nspname = 'billing') THEN
        CREATE TYPE billing.owner_type AS ENUM ('PERSONAL', 'TENANT');
    END IF;
    -- [COMMENT]: Enum credential_kind định nghĩa loại credential (STATIC, STS)
    IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON t.typnamespace = n.oid WHERE t.typname = 'credential_kind' AND n.nspname = 'billing') THEN
        CREATE TYPE billing.credential_kind AS ENUM ('STATIC', 'STS');
    END IF;
    -- [COMMENT]: Enum wallet_lifecycle_status quản lý vòng đời ví (PENDING_ACTIVATION, ACTIVE, SUSPENDED, CLOSED)
    IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON t.typnamespace = n.oid WHERE t.typname = 'wallet_lifecycle_status' AND n.nspname = 'billing') THEN
        CREATE TYPE billing.wallet_lifecycle_status AS ENUM ('PENDING_ACTIVATION', 'ACTIVE', 'SUSPENDED', 'CLOSED');
    END IF;
    -- [COMMENT]: Enum ledger_entry_type ghi nhận phân loại giao dịch ví trong sổ cái
    IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON t.typnamespace = n.oid WHERE t.typname = 'ledger_entry_type' AND n.nspname = 'billing') THEN
        CREATE TYPE billing.ledger_entry_type AS ENUM ('TOP_UP', 'PROMO_CREDIT', 'USAGE_CHARGE', 'REFUND', 'ADJUSTMENT', 'PROMO_EXPIRED');
    END IF;
END $$;
