-- Migration 000003 Down: Xóa bỏ các chỉ mục hiệu năng
DROP INDEX IF EXISTS billing.idx_unrated_usage_pending;
DROP INDEX IF EXISTS billing.idx_wallet_ledger_billing_run;
DROP INDEX IF EXISTS billing.idx_wallet_ledger_wallet_time;
DROP INDEX IF EXISTS billing.uq_resource_plan_active_resource;
DROP INDEX IF EXISTS billing.idx_credential_bindings_resource;
DROP INDEX IF EXISTS billing.uq_credential_bindings_active_access_key;
DROP INDEX IF EXISTS billing.idx_resource_ownership_lookup;
DROP INDEX IF EXISTS billing.uq_resource_ownership_active_name;
DROP INDEX IF EXISTS billing.uq_resource_ownership_active_resource;
DROP INDEX IF EXISTS billing.uq_subscriptions_active_owner;
DROP INDEX IF EXISTS billing.uq_billing_runs_active_service;
DROP INDEX IF EXISTS billing.idx_pricing_outbox_unpublished;
DROP INDEX IF EXISTS billing.idx_tier_version_ranges_version;
DROP INDEX IF EXISTS billing.uq_tier_version_ranges_one_infinity;
DROP INDEX IF EXISTS billing.idx_tier_versions_effective_lookup;
DROP INDEX IF EXISTS billing.uq_tiers_service_type;
