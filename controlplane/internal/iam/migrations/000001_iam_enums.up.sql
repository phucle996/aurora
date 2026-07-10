-- IAM migration layer 000001
-- Shared extension + enum types for IAM auth core.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

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


DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'challenge_status' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE challenge_status AS ENUM ('pending', 'verified', 'expired', 'failed', 'consumed');
    END IF;
END
$$;
DO $$
BEGIN
    EXECUTE format('ALTER TYPE %I.challenge_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'pending');
    EXECUTE format('ALTER TYPE %I.challenge_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'verified');
    EXECUTE format('ALTER TYPE %I.challenge_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'expired');
    EXECUTE format('ALTER TYPE %I.challenge_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'failed');
    EXECUTE format('ALTER TYPE %I.challenge_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'consumed');
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'mfa_type' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE mfa_type AS ENUM ('totp', 'recovery_code');
    END IF;
END
$$;
DO $$
BEGIN
    EXECUTE format('ALTER TYPE %I.mfa_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'totp');
    EXECUTE format('ALTER TYPE %I.mfa_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'recovery_code');
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'mfa_status' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE mfa_status AS ENUM ('pending', 'enabled', 'disabled');
    END IF;
END
$$;
DO $$
BEGIN
    EXECUTE format('ALTER TYPE %I.mfa_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'pending');
    EXECUTE format('ALTER TYPE %I.mfa_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'enabled');
    EXECUTE format('ALTER TYPE %I.mfa_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'disabled');
END
$$;

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

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'oauth_client_type' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE oauth_client_type AS ENUM ('public', 'confidential');
    END IF;
END
$$;
DO $$
BEGIN
    EXECUTE format('ALTER TYPE %I.oauth_client_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'public');
    EXECUTE format('ALTER TYPE %I.oauth_client_type ADD VALUE IF NOT EXISTS %L', current_schema(), 'confidential');
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE t.typname = 'oauth_client_status' AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE oauth_client_status AS ENUM ('active', 'disabled', 'revoked');
    END IF;
END
$$;
DO $$
BEGIN
    EXECUTE format('ALTER TYPE %I.oauth_client_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'active');
    EXECUTE format('ALTER TYPE %I.oauth_client_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'disabled');
    EXECUTE format('ALTER TYPE %I.oauth_client_status ADD VALUE IF NOT EXISTS %L', current_schema(), 'revoked');
END
$$;



