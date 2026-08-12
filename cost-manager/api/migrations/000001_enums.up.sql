-- Billing schema primitives. This baseline is greenfield: every later table
-- is created in its final PAYG shape and does not overlay a legacy catalog.
CREATE SCHEMA IF NOT EXISTS billing;
CREATE EXTENSION IF NOT EXISTS btree_gist;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typnamespace = 'billing'::regnamespace AND typname = 'service_type') THEN
        CREATE TYPE billing.service_type AS ENUM ('STORAGE', 'NETWORK_IN', 'NETWORK_OUT', 'VM');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typnamespace = 'billing'::regnamespace AND typname = 'owner_type') THEN
        CREATE TYPE billing.owner_type AS ENUM ('PERSONAL', 'TENANT');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typnamespace = 'billing'::regnamespace AND typname = 'credential_kind') THEN
        CREATE TYPE billing.credential_kind AS ENUM ('STATIC', 'STS');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typnamespace = 'billing'::regnamespace AND typname = 'wallet_lifecycle_status') THEN
        CREATE TYPE billing.wallet_lifecycle_status AS ENUM ('PENDING_ACTIVATION', 'ACTIVE', 'SUSPENDED', 'CLOSED');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typnamespace = 'billing'::regnamespace AND typname = 'ledger_entry_type') THEN
        CREATE TYPE billing.ledger_entry_type AS ENUM ('TOP_UP', 'PROMO_CREDIT', 'USAGE_CHARGE', 'REFUND', 'ADJUSTMENT', 'PROMO_EXPIRED');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typnamespace = 'billing'::regnamespace AND typname = 'pricing_model') THEN
        CREATE TYPE billing.pricing_model AS ENUM ('PROGRESSIVE_UNIT', 'FIXED_BUNDLE');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typnamespace = 'billing'::regnamespace AND typname = 'pricing_scope') THEN
        CREATE TYPE billing.pricing_scope AS ENUM ('GLOBAL', 'ZONE');
    END IF;
END $$;
