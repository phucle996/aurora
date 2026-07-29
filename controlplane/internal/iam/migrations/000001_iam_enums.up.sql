-- IAM migration layer 000001
-- Shared extension + enum types for IAM auth core.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- [COMMENT]: Trạng thái user account
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'user_status' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE user_status AS ENUM ('pending-active', 'active', 'suspended', 'disabled');
    END IF;
END
$$;

DO $$
BEGIN
    EXECUTE format('ALTER TYPE %I.user_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'pending-active');
    EXECUTE format('ALTER TYPE %I.user_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'active');
    EXECUTE format('ALTER TYPE %I.user_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'suspended');
    EXECUTE format('ALTER TYPE %I.user_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'disabled');
END
$$;

-- [COMMENT]: Billing owner là contract liên-domain; enum chặn producer ghi sai loại ví ngay tại PostgreSQL.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'billing_owner_type' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE billing_owner_type AS ENUM ('PERSONAL', 'TENANT');
    END IF;
END
$$;

-- [COMMENT]: Role scope type cho RBAC
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'role_scope_type' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE role_scope_type AS ENUM ('platform', 'tenant', 'workspace');
    END IF;
END
$$;

DO $$
BEGIN
    EXECUTE format('ALTER TYPE %I.role_scope_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'platform');
    EXECUTE format('ALTER TYPE %I.role_scope_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'tenant');
    EXECUTE format('ALTER TYPE %I.role_scope_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'workspace');
END
$$;

