-- Migration 000003: Tạo các chỉ mục hiệu năng (Indexes) và ràng buộc bổ sung (Constraints)

-- Indexes cho Tiers
CREATE UNIQUE INDEX IF NOT EXISTS uq_tiers_service_type
    ON billing.tiers(service_type);

-- Indexes cho Tier Versions
CREATE INDEX IF NOT EXISTS idx_tier_versions_effective_lookup
    ON billing.tier_versions(tier_id, effective_from DESC);

-- Indexes và Constraints cho Tier Version Ranges
CREATE UNIQUE INDEX IF NOT EXISTS uq_tier_version_ranges_one_infinity
    ON billing.tier_version_ranges(tier_version_id)
    WHERE range_end = 0;

CREATE INDEX IF NOT EXISTS idx_tier_version_ranges_version
    ON billing.tier_version_ranges(tier_version_id, range_start);

-- Indexes cho Pricing Outbox
CREATE INDEX IF NOT EXISTS idx_pricing_outbox_unpublished
    ON billing.pricing_outbox(occurred_at, id)
    WHERE published_at IS NULL;

-- Indexes và Constraints cho Billing Runs
CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_runs_active_service
    ON billing.billing_runs(service_type)
    WHERE status IN ('RUNNING', 'RETRYING');

-- Chỉ một subscription ACTIVE; subscription lịch sử vẫn được giữ lại.
CREATE UNIQUE INDEX IF NOT EXISTS uq_subscriptions_active_owner
    ON billing.subscriptions(owner_id, owner_type)
    WHERE status = 'ACTIVE';

CREATE UNIQUE INDEX IF NOT EXISTS uq_resource_ownership_active_resource
    ON billing.resource_ownership_projection(resource_type, resource_id)
    WHERE effective_to IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_resource_ownership_active_name
    ON billing.resource_ownership_projection(resource_type, resource_name)
    WHERE effective_to IS NULL;

CREATE INDEX IF NOT EXISTS idx_resource_ownership_lookup
    ON billing.resource_ownership_projection(resource_name, effective_from DESC, effective_to);

CREATE UNIQUE INDEX IF NOT EXISTS uq_credential_bindings_active_access_key
    ON billing.credential_bindings(access_key)
    WHERE valid_to IS NULL AND status = 'ACTIVE';

CREATE INDEX IF NOT EXISTS idx_credential_bindings_resource
    ON billing.credential_bindings(resource_type, resource_id, valid_from DESC);

CREATE UNIQUE INDEX IF NOT EXISTS uq_resource_plan_active_resource
    ON billing.resource_plan_assignments(resource_type, resource_id)
    WHERE effective_to IS NULL;

CREATE INDEX IF NOT EXISTS idx_wallet_ledger_wallet_time
    ON billing.wallet_ledger_entries(wallet_id, occurred_at DESC, id);

CREATE INDEX IF NOT EXISTS idx_wallet_ledger_billing_run
    ON billing.wallet_ledger_entries(billing_run_id)
    WHERE billing_run_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_unrated_usage_pending
    ON billing.unrated_usage(metering_hour, id)
    WHERE status = 'PENDING';
