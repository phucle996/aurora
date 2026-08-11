-- Migration 000003: Tạo các chỉ mục hiệu năng (Indexes) và ràng buộc bổ sung (Constraints) cho billing module

-- 1. Indexes cho Tiers
CREATE UNIQUE INDEX IF NOT EXISTS uq_tiers_service_type
    ON billing.tiers(service_type);

-- 2. Indexes cho Tier Versions
CREATE INDEX IF NOT EXISTS idx_tier_versions_effective_lookup
    ON billing.tier_versions(tier_id, effective_from DESC);

-- 3. Indexes cho Tier Version Ranges
CREATE UNIQUE INDEX IF NOT EXISTS uq_tier_version_ranges_one_infinity
    ON billing.tier_version_ranges(tier_version_id)
    WHERE range_end = 0;

CREATE INDEX IF NOT EXISTS idx_tier_version_ranges_version
    ON billing.tier_version_ranges(tier_version_id, range_start);

-- 4. Indexes cho Pricing Outbox
CREATE INDEX IF NOT EXISTS idx_pricing_outbox_unpublished
    ON billing.pricing_outbox(occurred_at, id)
    WHERE published_at IS NULL;

-- 5. Indexes cho Billing Runs
CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_runs_active_service
    ON billing.billing_runs(service_type)
    WHERE status IN ('RUNNING', 'RETRYING');

-- 6. Indexes cho Subscriptions
CREATE UNIQUE INDEX IF NOT EXISTS uq_subscriptions_active_owner
    ON billing.subscriptions(owner_id, owner_type)
    WHERE status = 'ACTIVE';

-- 7. Indexes cho Resource Ownership Projection & Head
CREATE UNIQUE INDEX IF NOT EXISTS uq_resource_ownership_active_resource
    ON billing.resource_ownership_projection(resource_type, resource_id)
    WHERE effective_to IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_resource_ownership_active_name
    ON billing.resource_ownership_projection(resource_type, resource_name)
    WHERE effective_to IS NULL;

CREATE INDEX IF NOT EXISTS idx_resource_ownership_lookup
    ON billing.resource_ownership_projection(resource_name, effective_from DESC, effective_to);

CREATE UNIQUE INDEX IF NOT EXISTS uq_ownership_inbox_resource_version
    ON billing.ownership_event_inbox (resource_id, source_version);

-- 8. Indexes cho Credential Bindings
CREATE UNIQUE INDEX IF NOT EXISTS uq_credential_bindings_active_access_key
    ON billing.credential_bindings(access_key)
    WHERE valid_to IS NULL AND status = 'ACTIVE';

CREATE INDEX IF NOT EXISTS idx_credential_bindings_resource
    ON billing.credential_bindings(resource_type, resource_id, valid_from DESC);

-- 9. Indexes cho Resource Plan Assignments
CREATE UNIQUE INDEX IF NOT EXISTS uq_resource_plan_active_resource
    ON billing.resource_plan_assignments(resource_type, resource_id)
    WHERE effective_to IS NULL;

-- 10. Indexes cho Wallet Ledger Entries
CREATE INDEX IF NOT EXISTS idx_wallet_ledger_wallet_time
    ON billing.wallet_ledger_entries(wallet_id, occurred_at DESC, id);

CREATE INDEX IF NOT EXISTS idx_wallet_ledger_billing_run
    ON billing.wallet_ledger_entries(billing_run_id)
    WHERE billing_run_id IS NOT NULL;

-- 11. Indexes cho Unrated Usage Queue
CREATE INDEX IF NOT EXISTS idx_unrated_usage_pending
    ON billing.unrated_usage(metering_hour, id)
    WHERE status = 'PENDING';

-- 12. Indexes cho Promotion Campaigns & Personal Referral Reservations
CREATE INDEX IF NOT EXISTS idx_referral_campaign_catalog
    ON billing.promotion_campaigns (campaign_type, status, starts_at, ends_at);

CREATE INDEX IF NOT EXISTS idx_personal_referral_reservations_campaign_capacity
    ON billing.personal_referral_reservations (campaign_id, status, expires_at);

CREATE UNIQUE INDEX IF NOT EXISTS uq_personal_referral_reservation_live_user
    ON billing.personal_referral_reservations (user_id, redemption_kind)
    WHERE status='RESERVED';

-- 13. Indexes cho Payment Intents
CREATE INDEX IF NOT EXISTS idx_payment_intents_owner_created
    ON billing.payment_intents (owner_id, owner_type, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_payment_intents_actor_created
    ON billing.payment_intents (actor_user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_payment_intents_pending_expiry
    ON billing.payment_intents (expires_at)
    WHERE status='PENDING';

-- 14. Indexes cho storage usage settlement inboxes
CREATE INDEX IF NOT EXISTS idx_storage_report_inbox_pending
    ON billing.storage_usage_report_inbox(status, received_at, report_id)
    WHERE status IN ('RECEIVED', 'PROCESSING', 'UNRATED');

CREATE INDEX IF NOT EXISTS idx_storage_usage_line_resource_window
    ON billing.storage_usage_line_inbox(zone_id, resource_id, created_at);

CREATE INDEX IF NOT EXISTS idx_storage_usage_line_resource_name
    ON billing.storage_usage_line_inbox(zone_id, resource_name, created_at)
    WHERE resource_name IS NOT NULL;
