-- Core ownership, wallet, payment and owner-specific onboarding tables.
-- These tables contain the final PAYG wallet admission contract.

CREATE TABLE billing.resource_ownership_projection (
    id                  UUID PRIMARY KEY,
    resource_type       VARCHAR(32) NOT NULL,
    resource_id         UUID NOT NULL,
    resource_name       VARCHAR(255) NOT NULL,
    owner_id            UUID NOT NULL,
    owner_type          billing.owner_type NOT NULL,
    zone_id             UUID NOT NULL,
    ownership_version   INT NOT NULL DEFAULT 1,
    effective_from      TIMESTAMPTZ NOT NULL,
    effective_to        TIMESTAMPTZ,
    source_updated_at   TIMESTAMPTZ NOT NULL,
    reconciled_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_resource_ownership_version CHECK (ownership_version > 0),
    CONSTRAINT ck_resource_ownership_window CHECK (effective_to IS NULL OR effective_to > effective_from)
);

CREATE TABLE billing.credential_bindings (
    id                  UUID PRIMARY KEY,
    access_key          VARCHAR(255) NOT NULL,
    credential_kind     billing.credential_kind NOT NULL DEFAULT 'STATIC',
    resource_type       VARCHAR(32) NOT NULL,
    resource_id         UUID NOT NULL,
    valid_from          TIMESTAMPTZ NOT NULL,
    valid_to            TIMESTAMPTZ,
    status              VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    source_updated_at   TIMESTAMPTZ NOT NULL,
    reconciled_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_credential_binding_window CHECK (valid_to IS NULL OR valid_to > valid_from),
    CONSTRAINT ck_credential_binding_status CHECK (status IN ('ACTIVE', 'REVOKED', 'EXPIRED'))
);

CREATE TABLE billing.ownership_event_inbox (
    event_id            UUID PRIMARY KEY,
    event_type          VARCHAR(32) NOT NULL,
    schema_version      INT NOT NULL DEFAULT 1,
    payload_hash        VARCHAR(64) NOT NULL,
    resource_id         UUID NOT NULL,
    source_version      BIGINT NOT NULL DEFAULT 1,
    status              VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',
    error_message       TEXT,
    received_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at        TIMESTAMPTZ,
    CONSTRAINT ck_ownership_inbox_status CHECK (status IN ('RECEIVED', 'APPLIED', 'DEAD'))
);

CREATE TABLE billing.resource_ownership_head (
    resource_id         UUID PRIMARY KEY,
    last_source_version BIGINT NOT NULL,
    resource_state      VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_resource_ownership_state CHECK (resource_state IN ('ACTIVE', 'DELETED'))
);

CREATE TABLE billing.wallets (
    id                    UUID PRIMARY KEY,
    owner_id              UUID NOT NULL,
    owner_type            billing.owner_type NOT NULL,
    currency              CHAR(3) NOT NULL DEFAULT 'USD',
    cash_balance          BIGINT NOT NULL DEFAULT 0,
    promotional_balance   BIGINT NOT NULL DEFAULT 0,
    overdraft_limit       BIGINT NOT NULL DEFAULT 0,
    status                billing.wallet_lifecycle_status NOT NULL DEFAULT 'PENDING_ACTIVATION',
    restriction_reason    VARCHAR(32),
    status_changed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at             TIMESTAMPTZ,
    version               BIGINT NOT NULL DEFAULT 1,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_wallet_owner_currency UNIQUE (owner_id, owner_type, currency),
    CONSTRAINT ck_wallet_currency_upper CHECK (currency = UPPER(currency)),
    CONSTRAINT ck_wallet_promo_non_negative CHECK (promotional_balance >= 0),
    CONSTRAINT ck_wallet_overdraft_non_negative CHECK (overdraft_limit >= 0),
    CONSTRAINT ck_wallet_version_positive CHECK (version > 0),
    CONSTRAINT ck_wallet_admission_state CHECK (
        (status = 'PENDING_ACTIVATION' AND restriction_reason = 'NOT_ACTIVATED' AND closed_at IS NULL)
        OR (status = 'ACTIVE' AND restriction_reason IS NULL AND closed_at IS NULL)
        OR (status = 'SUSPENDED' AND restriction_reason IN ('CREDIT_EXHAUSTED', 'ADMINISTRATIVE', 'COMPLIANCE') AND closed_at IS NULL)
        OR (status = 'CLOSED' AND restriction_reason = 'CLOSED' AND closed_at IS NOT NULL)
    )
);

CREATE TABLE billing.promotion_campaigns (
    id                          UUID PRIMARY KEY,
    code                        VARCHAR(64) NOT NULL UNIQUE,
    name                        VARCHAR(128) NOT NULL,
    amount_micro_units          BIGINT NOT NULL,
    currency                    CHAR(3) NOT NULL DEFAULT 'USD',
    service_scope               billing.service_type,
    campaign_type               VARCHAR(32) NOT NULL DEFAULT 'LEGACY',
    minimum_top_up_micro_units  BIGINT NOT NULL DEFAULT 0,
    max_redemptions             BIGINT,
    version                     BIGINT NOT NULL DEFAULT 1,
    starts_at                   TIMESTAMPTZ NOT NULL,
    ends_at                     TIMESTAMPTZ,
    status                      VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_promotion_amount_positive CHECK (amount_micro_units > 0),
    CONSTRAINT ck_promotion_window CHECK (ends_at IS NULL OR ends_at > starts_at),
    CONSTRAINT ck_promotion_status CHECK (status IN ('ACTIVE', 'PAUSED', 'ENDED')),
    CONSTRAINT ck_promotion_campaign_type CHECK (campaign_type IN ('LEGACY', 'ONBOARDING_REFERRAL', 'EXTENSION')),
    CONSTRAINT ck_promotion_minimum_top_up CHECK (minimum_top_up_micro_units >= 0),
    CONSTRAINT ck_promotion_max_redemptions CHECK (max_redemptions IS NULL OR max_redemptions > 0),
    CONSTRAINT ck_promotion_version_positive CHECK (version > 0)
);

CREATE TABLE billing.credit_grants (
    id                  UUID PRIMARY KEY,
    campaign_id         UUID NOT NULL REFERENCES billing.promotion_campaigns(id) ON DELETE RESTRICT,
    wallet_id           UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id            UUID NOT NULL,
    owner_type          billing.owner_type NOT NULL,
    amount_micro_units  BIGINT NOT NULL,
    currency            CHAR(3) NOT NULL,
    expires_at          TIMESTAMPTZ,
    idempotency_key     VARCHAR(128) NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_credit_grant_campaign_owner UNIQUE (campaign_id, owner_id, owner_type),
    CONSTRAINT uq_credit_grant_owner_idempotency UNIQUE (owner_id, owner_type, idempotency_key),
    CONSTRAINT ck_credit_grant_amount_positive CHECK (amount_micro_units > 0)
);

CREATE TABLE billing.personal_wallet_provision_inbox (
    event_id        UUID PRIMARY KEY,
    schema_version  INT NOT NULL CHECK (schema_version = 1),
    user_id         UUID NOT NULL,
    payload_hash    CHAR(64) NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ,
    CONSTRAINT ck_personal_wallet_provision_status CHECK (status IN ('RECEIVED', 'APPLIED', 'DEAD'))
);

CREATE TABLE billing.personal_referral_reservations (
    id                          UUID PRIMARY KEY,
    campaign_id                 UUID NOT NULL REFERENCES billing.promotion_campaigns(id) ON DELETE RESTRICT,
    wallet_id                   UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    user_id                     UUID NOT NULL,
    redemption_kind             VARCHAR(32) NOT NULL DEFAULT 'ONBOARDING',
    status                      VARCHAR(16) NOT NULL DEFAULT 'RESERVED',
    campaign_version            BIGINT NOT NULL,
    code_snapshot               VARCHAR(64) NOT NULL,
    grant_amount_micro_units    BIGINT NOT NULL,
    minimum_top_up_micro_units  BIGINT NOT NULL,
    currency                    CHAR(3) NOT NULL,
    grant_expires_at            TIMESTAMPTZ,
    idempotency_key             VARCHAR(128) NOT NULL,
    expires_at                  TIMESTAMPTZ NOT NULL,
    redeemed_at                 TIMESTAMPTZ,
    rejection_reason            VARCHAR(128),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_personal_referral_reservation_idempotency UNIQUE (user_id, idempotency_key),
    CONSTRAINT ck_personal_referral_reservation_kind CHECK (redemption_kind IN ('ONBOARDING', 'EXTENSION')),
    CONSTRAINT ck_personal_referral_reservation_status CHECK (status IN ('RESERVED', 'REDEEMED', 'REJECTED', 'CANCELLED')),
    CONSTRAINT ck_personal_referral_reservation_amount CHECK (grant_amount_micro_units > 0 AND minimum_top_up_micro_units >= 0),
    CONSTRAINT ck_personal_referral_reservation_window CHECK (expires_at > created_at),
    CONSTRAINT ck_personal_referral_reservation_terminal_time CHECK ((status = 'REDEEMED') = (redeemed_at IS NOT NULL))
);

CREATE TABLE billing.tenant_wallet_provision_inbox (
    event_id        UUID PRIMARY KEY,
    schema_version  INT NOT NULL CHECK (schema_version = 1),
    tenant_id       UUID NOT NULL,
    actor_user_id   UUID NOT NULL,
    payload_hash    CHAR(64) NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ,
    CONSTRAINT ck_tenant_wallet_provision_status CHECK (status IN ('RECEIVED', 'APPLIED', 'DEAD'))
);

CREATE TABLE billing.payment_intents (
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
    CONSTRAINT uq_payment_intent_owner_idempotency UNIQUE (owner_id, owner_type, idempotency_key),
    CONSTRAINT uq_payment_provider_reference UNIQUE (provider, provider_payment_id),
    CONSTRAINT ck_payment_intent_amount CHECK (amount_micro_units > 0),
    CONSTRAINT ck_payment_intent_status CHECK (status IN ('PENDING', 'SETTLED', 'EXPIRED', 'CANCELLED')),
    CONSTRAINT ck_payment_intent_window CHECK (expires_at > created_at),
    CONSTRAINT ck_payment_intent_settled_time CHECK ((status = 'SETTLED') = (settled_at IS NOT NULL)),
    CONSTRAINT ck_payment_personal_actor CHECK (owner_type <> 'PERSONAL' OR actor_user_id = owner_id),
    CONSTRAINT ck_payment_tenant_has_no_referral CHECK (owner_type <> 'TENANT' OR personal_referral_reservation_id IS NULL)
);

CREATE TABLE billing.payment_webhook_inbox (
    provider           VARCHAR(32) NOT NULL,
    provider_event_id  VARCHAR(128) NOT NULL,
    owner_type         billing.owner_type NOT NULL,
    payload_hash       CHAR(64) NOT NULL,
    payment_intent_id  UUID,
    status             VARCHAR(16) NOT NULL DEFAULT 'RECEIVED',
    error_code         VARCHAR(64),
    received_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at       TIMESTAMPTZ,
    PRIMARY KEY (provider, provider_event_id),
    CONSTRAINT ck_payment_webhook_status CHECK (status IN ('RECEIVED', 'APPLIED', 'REJECTED'))
);

CREATE TABLE billing.personal_referral_redemptions (
    id                  UUID PRIMARY KEY,
    reservation_id      UUID NOT NULL UNIQUE REFERENCES billing.personal_referral_reservations(id) ON DELETE RESTRICT,
    campaign_id         UUID NOT NULL REFERENCES billing.promotion_campaigns(id) ON DELETE RESTRICT,
    wallet_id           UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    user_id             UUID NOT NULL,
    redemption_kind     VARCHAR(32) NOT NULL,
    payment_intent_id   UUID NOT NULL UNIQUE REFERENCES billing.payment_intents(id) ON DELETE RESTRICT,
    credit_grant_id     UUID NOT NULL UNIQUE REFERENCES billing.credit_grants(id) ON DELETE RESTRICT,
    amount_micro_units  BIGINT NOT NULL,
    currency            CHAR(3) NOT NULL,
    redeemed_at         TIMESTAMPTZ NOT NULL,
    CONSTRAINT uq_personal_referral_redemption_user_kind UNIQUE (user_id, redemption_kind),
    CONSTRAINT ck_personal_referral_redemption_kind CHECK (redemption_kind IN ('ONBOARDING', 'EXTENSION')),
    CONSTRAINT ck_personal_referral_redemption_amount CHECK (amount_micro_units > 0)
);

CREATE TABLE billing.wallet_admission_outbox (
    event_id            UUID PRIMARY KEY,
    wallet_id           UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id            UUID NOT NULL,
    owner_type          billing.owner_type NOT NULL,
    wallet_version      BIGINT NOT NULL,
    admission_mode      VARCHAR(24) NOT NULL,
    restriction_reason  VARCHAR(32),
    effective_at        TIMESTAMPTZ NOT NULL,
    valid_until         TIMESTAMPTZ,
    occurred_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at        TIMESTAMPTZ,
    claim_token         UUID,
    claimed_at          TIMESTAMPTZ,
    retry_count         INT NOT NULL DEFAULT 0,
    last_error          TEXT,
    CONSTRAINT ck_wallet_admission_mode CHECK (admission_mode IN ('ALLOW', 'SUSPEND_BILLABLE')),
    CONSTRAINT ck_wallet_admission_reason CHECK (
        (admission_mode = 'ALLOW' AND restriction_reason IS NULL)
        OR (admission_mode = 'SUSPEND_BILLABLE' AND restriction_reason IN ('NOT_ACTIVATED', 'CREDIT_EXHAUSTED', 'ADMINISTRATIVE', 'COMPLIANCE', 'CLOSED'))
    ),
    CONSTRAINT ck_wallet_admission_retry CHECK (retry_count >= 0),
    CONSTRAINT ck_wallet_admission_window CHECK (valid_until IS NULL OR valid_until > effective_at)
);

CREATE TABLE billing.storage_pending_activation_reconcile (
    wallet_id              UUID PRIMARY KEY REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id               UUID NOT NULL,
    owner_type             billing.owner_type NOT NULL,
    target_wallet_version  BIGINT NOT NULL,
    status                 VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    checkpoint_window_end  TIMESTAMPTZ,
    last_error             TEXT,
    retry_count            INT NOT NULL DEFAULT 0,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_storage_pending_activation_status CHECK (status IN ('PENDING', 'PROCESSING', 'BLOCKED', 'COMPLETED')),
    CONSTRAINT ck_storage_pending_activation_retry CHECK (retry_count >= 0)
);
