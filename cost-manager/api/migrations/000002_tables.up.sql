-- Migration 000002: Khởi tạo toàn bộ cấu trúc các bảng cơ sở dữ liệu (DDL Schema) cho billing module

-- 1. Bảng Packs (Gói giải pháp thương mại)
CREATE TABLE IF NOT EXISTS billing.packs (
    id            UUID PRIMARY KEY,
    name          VARCHAR(128) NOT NULL,
    code          VARCHAR(64)  NOT NULL UNIQUE,
    tier_target   VARCHAR(32)  NOT NULL DEFAULT 'FREE_TIER',
    monthly_price BIGINT       NOT NULL DEFAULT 0,
    currency      CHAR(3)      NOT NULL DEFAULT 'USD',
    discount_rate NUMERIC(5, 2) NOT NULL DEFAULT 0.00,
    status        VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE',
    description   TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 2. Bảng Tiers (Biểu giá cơ sở chung cho tài nguyên)
CREATE TABLE IF NOT EXISTS billing.tiers (
    id               UUID PRIMARY KEY,
    name             VARCHAR(128) NOT NULL,
    code             VARCHAR(64)  NOT NULL,
    service_type     billing.service_type NOT NULL,
    metadata_version INT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_tier_code_service UNIQUE (code, service_type)
);

-- 3. Bảng Plans (Resource SKU Plans)
CREATE TABLE IF NOT EXISTS billing.plans (
    id              UUID PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    code            VARCHAR(64)  NOT NULL UNIQUE,
    service_type    billing.service_type NOT NULL,
    zone_id         UUID         NOT NULL,
    tier_id         UUID         NOT NULL REFERENCES billing.tiers(id),
    zone_multiplier NUMERIC(4, 2) NOT NULL DEFAULT 1.00,
    monthly_price   BIGINT       NOT NULL DEFAULT 0,
    currency        CHAR(3)      NOT NULL DEFAULT 'USD',
    status          VARCHAR(16)  NOT NULL DEFAULT 'ACTIVE',
    description     TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 4. Bảng Pack_Plans (Bảng liên kết N:N giữa Pack và Resource Plan)
CREATE TABLE IF NOT EXISTS billing.pack_plans (
    id                 UUID PRIMARY KEY,
    pack_id            UUID NOT NULL REFERENCES billing.packs(id) ON DELETE CASCADE,
    plan_id            UUID NOT NULL REFERENCES billing.plans(id) ON DELETE RESTRICT,
    included_quota     NUMERIC(18, 4) NOT NULL DEFAULT 0,
    overage_unit_price BIGINT         NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_pack_plan UNIQUE (pack_id, plan_id)
);

-- 5. Bảng Subscriptions (Đăng ký Pack của Tenant / Personal)
CREATE TABLE IF NOT EXISTS billing.subscriptions (
    id              UUID        PRIMARY KEY,
    owner_id        UUID        NOT NULL,
    owner_type      billing.owner_type NOT NULL,
    pack_id         UUID        NOT NULL REFERENCES billing.packs(id),
    status          VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    version         INT         NOT NULL DEFAULT 1,
    idempotency_key VARCHAR(128),
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ,
    renewed_at      TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_subscription_owner_idempotency UNIQUE (owner_id, owner_type, idempotency_key),
    CONSTRAINT ck_subscription_window CHECK (expires_at IS NULL OR expires_at > started_at)
);

-- 6. Bảng tier_versions (Immutable Tier pricing versions)
CREATE TABLE IF NOT EXISTS billing.tier_versions (
    id              UUID PRIMARY KEY,
    tier_id         UUID NOT NULL REFERENCES billing.tiers(id) ON DELETE RESTRICT,
    version_number  INT NOT NULL,
    status          VARCHAR(16) NOT NULL,
    effective_from  TIMESTAMPTZ NOT NULL,
    effective_to    TIMESTAMPTZ,
    checksum        VARCHAR(64) NOT NULL,
    change_reason   TEXT NOT NULL,
    created_by      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_tier_version_number UNIQUE (tier_id, version_number),
    CONSTRAINT ck_tier_version_number_positive CHECK (version_number > 0),
    CONSTRAINT ck_tier_version_status CHECK (status IN ('SCHEDULED', 'ACTIVE', 'SUPERSEDED', 'CANCELLED')),
    CONSTRAINT ck_tier_version_effective_window CHECK (effective_to IS NULL OR effective_to > effective_from)
);

-- 7. Bảng tier_version_ranges (Ranges cho từng pricing version)
CREATE TABLE IF NOT EXISTS billing.tier_version_ranges (
    id              UUID PRIMARY KEY,
    tier_version_id UUID NOT NULL REFERENCES billing.tier_versions(id) ON DELETE RESTRICT,
    range_start     BIGINT NOT NULL,
    range_end       BIGINT NOT NULL,
    base_unit_price BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_tier_version_ranges_start_non_negative CHECK (range_start >= 0),
    CONSTRAINT ck_tier_version_ranges_end_after_start CHECK (range_end = 0 OR range_end > range_start),
    CONSTRAINT ck_tier_version_ranges_price_non_negative CHECK (base_unit_price >= 0),
    CONSTRAINT uq_tier_version_range_start UNIQUE (tier_version_id, range_start)
);

-- 8. Bảng pricing_outbox (Outbox cho pricing updates)
CREATE TABLE IF NOT EXISTS billing.pricing_outbox (
    id              UUID PRIMARY KEY,
    event_type      VARCHAR(64) NOT NULL,
    tier_id         UUID NOT NULL REFERENCES billing.tiers(id) ON DELETE RESTRICT,
    tier_version_id UUID NOT NULL REFERENCES billing.tier_versions(id) ON DELETE RESTRICT,
    version_number  INT NOT NULL,
    service_type    billing.service_type NOT NULL,
    effective_from  TIMESTAMPTZ NOT NULL,
    checksum        VARCHAR(64) NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ,
    retry_count     INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    CONSTRAINT ck_pricing_outbox_retry_non_negative CHECK (retry_count >= 0)
);

-- 9. Bảng billing_runs (Nhật ký chu kỳ chạy cước)
CREATE TABLE IF NOT EXISTS billing.billing_runs (
    id                UUID PRIMARY KEY,
    service_type      billing.service_type NOT NULL,
    tier_version_id   UUID NOT NULL REFERENCES billing.tier_versions(id) ON DELETE RESTRICT,
    window_start      TIMESTAMPTZ NOT NULL,
    window_end        TIMESTAMPTZ NOT NULL,
    status            VARCHAR(16) NOT NULL DEFAULT 'RUNNING',
    fencing_token     BIGINT NOT NULL,
    checkpoint        TIMESTAMPTZ,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at      TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_billing_run_window CHECK (window_end > window_start),
    CONSTRAINT ck_billing_run_status CHECK (status IN ('RUNNING', 'RETRYING', 'COMPLETED', 'FAILED'))
);

-- 10. Projection resource ownership
CREATE TABLE IF NOT EXISTS billing.resource_ownership_projection (
    id                UUID PRIMARY KEY,
    resource_type     VARCHAR(32) NOT NULL,
    resource_id       UUID NOT NULL,
    resource_name     VARCHAR(255) NOT NULL,
    owner_id          UUID NOT NULL,
    owner_type        billing.owner_type NOT NULL,
    zone_id           UUID NOT NULL,
    ownership_version INT NOT NULL DEFAULT 1,
    effective_from    TIMESTAMPTZ NOT NULL,
    effective_to      TIMESTAMPTZ,
    source_updated_at TIMESTAMPTZ NOT NULL,
    reconciled_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_resource_ownership_version CHECK (ownership_version > 0),
    CONSTRAINT ck_resource_ownership_window CHECK (effective_to IS NULL OR effective_to > effective_from)
);

-- 11. Bảng credential_bindings
CREATE TABLE IF NOT EXISTS billing.credential_bindings (
    id             UUID PRIMARY KEY,
    access_key     VARCHAR(255) NOT NULL,
    credential_kind billing.credential_kind NOT NULL DEFAULT 'STATIC',
    resource_type  VARCHAR(32) NOT NULL,
    resource_id    UUID NOT NULL,
    valid_from     TIMESTAMPTZ NOT NULL,
    valid_to       TIMESTAMPTZ,
    status         VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    source_updated_at TIMESTAMPTZ NOT NULL,
    reconciled_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_credential_binding_window CHECK (valid_to IS NULL OR valid_to > valid_from),
    CONSTRAINT ck_credential_binding_status CHECK (status IN ('ACTIVE', 'REVOKED', 'EXPIRED'))
);

-- 12. Bảng Wallets (Quản lý số dư tiền ví cá nhân & tenant)
CREATE TABLE IF NOT EXISTS billing.wallets (
    id                    UUID PRIMARY KEY,
    owner_id              UUID NOT NULL,
    owner_type            billing.owner_type NOT NULL,
    currency              CHAR(3) NOT NULL DEFAULT 'USD',
    cash_balance          BIGINT NOT NULL DEFAULT 0,
    promotional_balance   BIGINT NOT NULL DEFAULT 0,
    overdraft_limit       BIGINT NOT NULL DEFAULT 0,
    status                billing.wallet_lifecycle_status NOT NULL DEFAULT 'PENDING_ACTIVATION',
    version               BIGINT NOT NULL DEFAULT 1,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_wallet_owner_currency UNIQUE (owner_id, owner_type, currency),
    CONSTRAINT ck_wallet_currency_upper CHECK (currency = UPPER(currency)),
    CONSTRAINT ck_wallet_promo_non_negative CHECK (promotional_balance >= 0),
    CONSTRAINT ck_wallet_overdraft_non_negative CHECK (overdraft_limit >= 0),
    CONSTRAINT ck_wallet_version_positive CHECK (version > 0)
);

-- 13. Bảng promotion_campaigns (Chiến dịch ưu đãi / referral)
CREATE TABLE IF NOT EXISTS billing.promotion_campaigns (
    id                         UUID PRIMARY KEY,
    code                       VARCHAR(64) NOT NULL UNIQUE,
    name                       VARCHAR(128) NOT NULL,
    amount_micro_units         BIGINT NOT NULL,
    currency                   CHAR(3) NOT NULL DEFAULT 'USD',
    service_scope              billing.service_type,
    campaign_type              VARCHAR(32) NOT NULL DEFAULT 'LEGACY',
    minimum_top_up_micro_units BIGINT NOT NULL DEFAULT 0,
    max_redemptions            BIGINT,
    version                    BIGINT NOT NULL DEFAULT 1,
    starts_at                  TIMESTAMPTZ NOT NULL,
    ends_at                    TIMESTAMPTZ,
    status                     VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_promotion_amount_positive CHECK (amount_micro_units > 0),
    CONSTRAINT ck_promotion_window CHECK (ends_at IS NULL OR ends_at > starts_at),
    CONSTRAINT ck_promotion_status CHECK (status IN ('ACTIVE', 'PAUSED', 'ENDED')),
    CONSTRAINT ck_promotion_campaign_type CHECK (campaign_type IN ('LEGACY', 'ONBOARDING_REFERRAL', 'EXTENSION')),
    CONSTRAINT ck_promotion_minimum_top_up CHECK (minimum_top_up_micro_units >= 0),
    CONSTRAINT ck_promotion_max_redemptions CHECK (max_redemptions IS NULL OR max_redemptions > 0),
    CONSTRAINT ck_promotion_version_positive CHECK (version > 0)
);

-- 14. Bảng credit_grants (Cấp khoản tín dụng ưu đãi)
CREATE TABLE IF NOT EXISTS billing.credit_grants (
    id                 UUID PRIMARY KEY,
    campaign_id        UUID NOT NULL REFERENCES billing.promotion_campaigns(id) ON DELETE RESTRICT,
    wallet_id          UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id           UUID NOT NULL,
    owner_type         billing.owner_type NOT NULL,
    amount_micro_units BIGINT NOT NULL,
    currency           CHAR(3) NOT NULL,
    expires_at         TIMESTAMPTZ,
    idempotency_key    VARCHAR(128) NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_credit_grant_campaign_owner UNIQUE (campaign_id, owner_id, owner_type),
    CONSTRAINT uq_credit_grant_owner_idempotency UNIQUE (owner_id, owner_type, idempotency_key),
    CONSTRAINT ck_credit_grant_amount_positive CHECK (amount_micro_units > 0)
);

-- 15. Bảng resource_plan_assignments
CREATE TABLE IF NOT EXISTS billing.resource_plan_assignments (
    id                  UUID PRIMARY KEY,
    resource_type       VARCHAR(32) NOT NULL,
    resource_id         UUID NOT NULL,
    subscription_id     UUID NOT NULL REFERENCES billing.subscriptions(id) ON DELETE RESTRICT,
    plan_id             UUID NOT NULL REFERENCES billing.plans(id) ON DELETE RESTRICT,
    entitlement_version INT NOT NULL DEFAULT 1,
    effective_from      TIMESTAMPTZ NOT NULL,
    effective_to        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_resource_plan_version CHECK (entitlement_version > 0),
    CONSTRAINT ck_resource_plan_window CHECK (effective_to IS NULL OR effective_to > effective_from)
);

-- 16. Bảng wallet_ledger_entries (Sổ cái giao dịch ví)
CREATE TABLE IF NOT EXISTS billing.wallet_ledger_entries (
    id                       UUID PRIMARY KEY,
    wallet_id                UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id                 UUID NOT NULL,
    owner_type               billing.owner_type NOT NULL,
    actor_user_id            UUID,
    amount_micro_units       BIGINT NOT NULL,
    cash_balance_after       BIGINT NOT NULL,
    promotional_balance_after BIGINT NOT NULL,
    currency                 CHAR(3) NOT NULL,
    entry_type               billing.ledger_entry_type NOT NULL,
    service_type             billing.service_type,
    reference_id             VARCHAR(255) NOT NULL,
    billing_run_id           UUID REFERENCES billing.billing_runs(id) ON DELETE RESTRICT,
    tier_version_id          UUID REFERENCES billing.tier_versions(id) ON DELETE RESTRICT,
    resource_id              UUID,
    resource_type            VARCHAR(32),
    usage_quantity           BIGINT,
    usage_unit               VARCHAR(16),
    description              TEXT NOT NULL,
    occurred_at              TIMESTAMPTZ NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_ledger_amount_non_zero CHECK (amount_micro_units <> 0),
    CONSTRAINT ck_ledger_promo_after_non_negative CHECK (promotional_balance_after >= 0),
    CONSTRAINT ck_ledger_usage_pair CHECK ((usage_quantity IS NULL) = (usage_unit IS NULL))
);

-- 17. Bảng unrated_usage
CREATE TABLE IF NOT EXISTS billing.unrated_usage (
    id                 UUID PRIMARY KEY,
    service_type       billing.service_type NOT NULL,
    resource_type      VARCHAR(32) NOT NULL,
    resource_id        UUID,
    resource_name      VARCHAR(255) NOT NULL,
    access_key         VARCHAR(255),
    metering_hour      TIMESTAMPTZ NOT NULL,
    usage_quantity     BIGINT NOT NULL,
    usage_unit         VARCHAR(16) NOT NULL,
    reason             VARCHAR(64) NOT NULL,
    status             VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    retry_count        INT NOT NULL DEFAULT 0,
    last_error         TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_unrated_usage_non_negative CHECK (usage_quantity >= 0),
    CONSTRAINT ck_unrated_retry_non_negative CHECK (retry_count >= 0),
    CONSTRAINT ck_unrated_status CHECK (status IN ('PENDING', 'PROCESSING', 'RESOLVED', 'DEAD'))
);

-- 18. Bảng ownership_event_inbox
CREATE TABLE IF NOT EXISTS billing.ownership_event_inbox (
    event_id        UUID PRIMARY KEY,
    event_type      VARCHAR(32) NOT NULL,
    schema_version  INT NOT NULL DEFAULT 1,
    payload_hash    VARCHAR(64) NOT NULL,
    resource_id     UUID NOT NULL,
    source_version  BIGINT NOT NULL DEFAULT 1,
    status          VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',
    error_message   TEXT,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ,
    CONSTRAINT ck_inbox_status CHECK (status IN ('RECEIVED', 'APPLIED', 'DEAD'))
);

-- 19. Bảng resource_ownership_head
CREATE TABLE IF NOT EXISTS billing.resource_ownership_head (
    resource_id         UUID PRIMARY KEY,
    last_source_version BIGINT NOT NULL,
    resource_state      VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_resource_ownership_state CHECK (resource_state IN ('ACTIVE', 'DELETED'))
);

-- 20. Bảng personal_wallet_provision_inbox
CREATE TABLE IF NOT EXISTS billing.personal_wallet_provision_inbox (
    event_id        UUID PRIMARY KEY,
    schema_version  INT NOT NULL CHECK (schema_version = 1),
    user_id         UUID NOT NULL,
    payload_hash    CHAR(64) NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ,
    CONSTRAINT ck_personal_wallet_provision_status
        CHECK (status IN ('RECEIVED', 'APPLIED', 'DEAD'))
);

-- 21. Bảng personal_referral_reservations
CREATE TABLE IF NOT EXISTS billing.personal_referral_reservations (
    id                           UUID PRIMARY KEY,
    campaign_id                  UUID NOT NULL REFERENCES billing.promotion_campaigns(id) ON DELETE RESTRICT,
    wallet_id                    UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    user_id                      UUID NOT NULL,
    redemption_kind              VARCHAR(32) NOT NULL DEFAULT 'ONBOARDING',
    status                       VARCHAR(16) NOT NULL DEFAULT 'RESERVED',
    campaign_version             BIGINT NOT NULL,
    code_snapshot                VARCHAR(64) NOT NULL,
    grant_amount_micro_units     BIGINT NOT NULL,
    minimum_top_up_micro_units   BIGINT NOT NULL,
    currency                     CHAR(3) NOT NULL,
    grant_expires_at             TIMESTAMPTZ,
    idempotency_key              VARCHAR(128) NOT NULL,
    expires_at                   TIMESTAMPTZ NOT NULL,
    redeemed_at                  TIMESTAMPTZ,
    rejection_reason             VARCHAR(128),
    created_at                   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_personal_referral_reservation_idempotency
        UNIQUE (user_id, idempotency_key),
    CONSTRAINT ck_personal_referral_reservation_kind
        CHECK (redemption_kind IN ('ONBOARDING', 'EXTENSION')),
    CONSTRAINT ck_personal_referral_reservation_status
        CHECK (status IN ('RESERVED', 'REDEEMED', 'REJECTED', 'CANCELLED')),
    CONSTRAINT ck_personal_referral_reservation_amount
        CHECK (grant_amount_micro_units > 0 AND minimum_top_up_micro_units >= 0),
    CONSTRAINT ck_personal_referral_reservation_window
        CHECK (expires_at > created_at),
    CONSTRAINT ck_personal_referral_reservation_terminal_time
        CHECK ((status = 'REDEEMED') = (redeemed_at IS NOT NULL))
);

-- 22. Bảng tenant_wallet_provision_inbox
CREATE TABLE IF NOT EXISTS billing.tenant_wallet_provision_inbox (
    event_id        UUID PRIMARY KEY,
    schema_version  INT NOT NULL CHECK (schema_version = 1),
    tenant_id       UUID NOT NULL,
    actor_user_id   UUID NOT NULL,
    payload_hash    CHAR(64) NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ,
    CONSTRAINT ck_tenant_wallet_provision_status
        CHECK (status IN ('RECEIVED', 'APPLIED', 'DEAD'))
);

-- 23. Bảng payment_intents
CREATE TABLE IF NOT EXISTS billing.payment_intents (
    id                               UUID PRIMARY KEY,
    wallet_id                        UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id                         UUID NOT NULL,
    owner_type                       billing.owner_type NOT NULL,
    actor_user_id                    UUID NOT NULL,
    amount_micro_units               BIGINT NOT NULL,
    currency                         CHAR(3) NOT NULL,
    provider                         VARCHAR(32) NOT NULL,
    provider_payment_id              VARCHAR(128),
    status                           VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    activates_wallet                 BOOLEAN NOT NULL,
    personal_referral_reservation_id UUID REFERENCES billing.personal_referral_reservations(id) ON DELETE RESTRICT,
    idempotency_key                  VARCHAR(128) NOT NULL,
    expires_at                       TIMESTAMPTZ NOT NULL,
    settled_at                       TIMESTAMPTZ,
    created_at                       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_payment_intent_owner_idempotency
        UNIQUE (owner_id, owner_type, idempotency_key),
    CONSTRAINT uq_payment_provider_reference
        UNIQUE (provider, provider_payment_id),
    CONSTRAINT ck_payment_intent_amount
        CHECK (amount_micro_units > 0),
    CONSTRAINT ck_payment_intent_status
        CHECK (status IN ('PENDING', 'SETTLED', 'EXPIRED', 'CANCELLED')),
    CONSTRAINT ck_payment_intent_window
        CHECK (expires_at > created_at),
    CONSTRAINT ck_payment_intent_settled_time
        CHECK ((status='SETTLED') = (settled_at IS NOT NULL)),
    CONSTRAINT ck_payment_personal_actor
        CHECK (owner_type <> 'PERSONAL' OR actor_user_id=owner_id),
    CONSTRAINT ck_payment_tenant_has_no_referral
        CHECK (owner_type <> 'TENANT' OR personal_referral_reservation_id IS NULL)
);

-- 24. Bảng payment_webhook_inbox
CREATE TABLE IF NOT EXISTS billing.payment_webhook_inbox (
    provider                   VARCHAR(32) NOT NULL,
    provider_event_id          VARCHAR(128) NOT NULL,
    owner_type                 billing.owner_type NOT NULL,
    payload_hash               CHAR(64) NOT NULL,
    payment_intent_id          UUID,
    status                     VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',
    error_code                 VARCHAR(64),
    received_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at               TIMESTAMPTZ,
    PRIMARY KEY (provider, provider_event_id),
    CONSTRAINT ck_payment_webhook_status
        CHECK (status IN ('RECEIVED', 'APPLIED', 'REJECTED'))
);

-- 25. Bảng personal_referral_redemptions
CREATE TABLE IF NOT EXISTS billing.personal_referral_redemptions (
    id                         UUID PRIMARY KEY,
    reservation_id             UUID NOT NULL UNIQUE REFERENCES billing.personal_referral_reservations(id) ON DELETE RESTRICT,
    campaign_id                UUID NOT NULL REFERENCES billing.promotion_campaigns(id) ON DELETE RESTRICT,
    wallet_id                  UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    user_id                    UUID NOT NULL,
    redemption_kind            VARCHAR(32) NOT NULL,
    payment_intent_id          UUID NOT NULL UNIQUE REFERENCES billing.payment_intents(id) ON DELETE RESTRICT,
    credit_grant_id            UUID NOT NULL UNIQUE REFERENCES billing.credit_grants(id) ON DELETE RESTRICT,
    amount_micro_units         BIGINT NOT NULL,
    currency                   CHAR(3) NOT NULL,
    redeemed_at                TIMESTAMPTZ NOT NULL,
    CONSTRAINT uq_personal_referral_redemption_user_kind
        UNIQUE (user_id, redemption_kind),
    CONSTRAINT ck_personal_referral_redemption_kind
        CHECK (redemption_kind IN ('ONBOARDING', 'EXTENSION')),
    CONSTRAINT ck_personal_referral_redemption_amount
        CHECK (amount_micro_units > 0)
);
