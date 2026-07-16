-- Billing post-migration verification queries
-- Usage:
--   psql -h 127.0.0.1 -p 5432 -U billing_admin -d billing -f migrations/verification_billing_post_migrate.sql
-- Optional schema override:
--   SET search_path TO billing,public;

-- ============================================================================
-- 1) Object existence (to_regclass)
-- ============================================================================
SELECT 'wallets' AS object_name, to_regclass('billing.wallets') AS regclass;
SELECT 'transactions' AS object_name, to_regclass('billing.transactions') AS regclass;
SELECT 'prices' AS object_name, to_regclass('billing.prices') AS regclass;

-- ============================================================================
-- 2) Index existence (pg_indexes)
-- ============================================================================
SELECT tablename, indexname
FROM pg_indexes
WHERE schemaname = 'billing'
  AND indexname IN (
    'idx_transactions_wallet_id',
    'idx_transactions_service_type',
    'uidx_prices_service_zone_tier',
    'idx_prices_effective_period'
  );

-- ============================================================================
-- 3) Seed count check
-- ============================================================================
SELECT service_type, zone_code, unit_price, tier
FROM billing.prices;
