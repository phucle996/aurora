-- Hierarchy migration layer 000001
-- Schema-aware ENUM creation. Mỗi enum được tạo trong current_schema() để có
-- thể test với nhiều schema song song mà không đụng namespace public.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'zone_status' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE zone_status AS ENUM ('planned', 'active', 'draining', 'maintenance', 'disabled');
    END IF;
END
$$;
DO $$
BEGIN
    -- add new zone status planned, active, draining, maintenance, disabled
    EXECUTE format('ALTER TYPE %I.zone_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'planned');
    EXECUTE format('ALTER TYPE %I.zone_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'active');
    EXECUTE format('ALTER TYPE %I.zone_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'draining');
    EXECUTE format('ALTER TYPE %I.zone_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'maintenance');
    EXECUTE format('ALTER TYPE %I.zone_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'disabled');
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'zone_service_type' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE zone_service_type AS ENUM ('mail', 'hypervisor', 'kubernetes', 'ai', 'storage', 'database');
    END IF;
END
$$;
DO $$
BEGIN
    EXECUTE format('ALTER TYPE %I.zone_service_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'mail');
    EXECUTE format('ALTER TYPE %I.zone_service_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'hypervisor');
    EXECUTE format('ALTER TYPE %I.zone_service_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'kubernetes');
    EXECUTE format('ALTER TYPE %I.zone_service_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'ai');
    EXECUTE format('ALTER TYPE %I.zone_service_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'storage');
    EXECUTE format('ALTER TYPE %I.zone_service_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'database');
END
$$;

-- [COMMENT]: Tạo kiểu ENUM cho trạng thái sức khỏe vận hành thực tế của dịch vụ (actual_state)
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'zone_service_status' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE zone_service_status AS ENUM ('unknown', 'healthy', 'degraded', 'unhealthy', 'down');
    END IF;
END
$$;
DO $$
BEGIN
    EXECUTE format('ALTER TYPE %I.zone_service_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'unknown');
    EXECUTE format('ALTER TYPE %I.zone_service_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'healthy');
    EXECUTE format('ALTER TYPE %I.zone_service_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'degraded');
    EXECUTE format('ALTER TYPE %I.zone_service_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'unhealthy');
    EXECUTE format('ALTER TYPE %I.zone_service_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'down');
END
$$;


