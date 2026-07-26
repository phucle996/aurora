-- Wallet onboarding replaces automatic Free Tier grants. Existing ACTIVE wallets
-- remain active; newly provisioned personal wallets wait for a settled top-up.
CREATE TYPE billing.wallet_lifecycle_status AS ENUM
    ('PENDING_ACTIVATION', 'ACTIVE', 'SUSPENDED', 'CLOSED');

ALTER TABLE billing.wallets
    ALTER COLUMN status DROP DEFAULT,
    ALTER COLUMN status TYPE billing.wallet_lifecycle_status
        USING status::text::billing.wallet_lifecycle_status;

ALTER TABLE billing.wallets
    ALTER COLUMN status SET DEFAULT 'PENDING_ACTIVATION'::billing.wallet_lifecycle_status;

ALTER TABLE billing.promotion_campaigns
    ADD COLUMN campaign_type VARCHAR(32) NOT NULL DEFAULT 'LEGACY',
    ADD COLUMN minimum_top_up_micro_units BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN max_redemptions BIGINT,
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD CONSTRAINT ck_promotion_campaign_type
        CHECK (campaign_type IN ('LEGACY', 'ONBOARDING_REFERRAL', 'EXTENSION')),
    ADD CONSTRAINT ck_promotion_minimum_top_up
        CHECK (minimum_top_up_micro_units >= 0),
    ADD CONSTRAINT ck_promotion_max_redemptions
        CHECK (max_redemptions IS NULL OR max_redemptions > 0),
    ADD CONSTRAINT ck_promotion_version_positive
        CHECK (version > 0);

-- Legacy seed remains immutable in migration history but is no longer eligible
-- for new activation after this rollout.
UPDATE billing.packs
SET status='DEPRECATED', updated_at=NOW()
WHERE code='FREE_TIER' AND status='ACTIVE';

UPDATE billing.promotion_campaigns
SET status='ENDED', updated_at=NOW()
WHERE code='FREE_TIER_100_USD' AND status='ACTIVE';

CREATE TABLE billing.referral_reservations (
    id                           UUID PRIMARY KEY,
    campaign_id                  UUID NOT NULL REFERENCES billing.promotion_campaigns(id) ON DELETE RESTRICT,
    wallet_id                    UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id                     UUID NOT NULL,
    owner_type                   billing.owner_type NOT NULL,
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
    CONSTRAINT uq_referral_reservation_owner_idempotency
        UNIQUE (owner_id, owner_type, idempotency_key),
    CONSTRAINT ck_referral_reservation_kind
        CHECK (redemption_kind IN ('ONBOARDING', 'EXTENSION')),
    CONSTRAINT ck_referral_reservation_status
        CHECK (status IN ('RESERVED', 'REDEEMED', 'REJECTED', 'CANCELLED')),
    CONSTRAINT ck_referral_reservation_amount
        CHECK (grant_amount_micro_units > 0 AND minimum_top_up_micro_units >= 0),
    CONSTRAINT ck_referral_reservation_window
        CHECK (expires_at > created_at),
    CONSTRAINT ck_referral_reservation_terminal_time
        CHECK ((status = 'REDEEMED') = (redeemed_at IS NOT NULL))
);

CREATE TABLE billing.payment_intents (
    id                         UUID PRIMARY KEY,
    wallet_id                  UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id                   UUID NOT NULL,
    owner_type                 billing.owner_type NOT NULL,
    actor_user_id              UUID NOT NULL,
    amount_micro_units         BIGINT NOT NULL,
    currency                   CHAR(3) NOT NULL,
    provider                   VARCHAR(32) NOT NULL,
    provider_payment_id        VARCHAR(128),
    status                     VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    activates_wallet           BOOLEAN NOT NULL,
    referral_reservation_id    UUID REFERENCES billing.referral_reservations(id) ON DELETE RESTRICT,
    idempotency_key            VARCHAR(128) NOT NULL,
    expires_at                 TIMESTAMPTZ NOT NULL,
    settled_at                 TIMESTAMPTZ,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
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
        CHECK ((status = 'SETTLED') = (settled_at IS NOT NULL))
);

-- Actor is separate from tenant owner so immutable money history can answer
-- which authorized human initiated a tenant payment.
ALTER TABLE billing.wallet_ledger_entries
    ADD COLUMN IF NOT EXISTS actor_user_id UUID;

-- Provision inbox is shared delivery infrastructure, but owner type and actor
-- are part of the replay fence. Tenant delivery must never replay as personal.
ALTER TABLE billing.wallet_provision_inbox
    ADD COLUMN IF NOT EXISTS owner_type billing.owner_type NOT NULL DEFAULT 'PERSONAL',
    ADD COLUMN IF NOT EXISTS actor_user_id UUID;

ALTER TABLE billing.wallet_provision_inbox
    ALTER COLUMN owner_type DROP DEFAULT;

CREATE TABLE billing.payment_webhook_inbox (
    provider                   VARCHAR(32) NOT NULL,
    provider_event_id          VARCHAR(128) NOT NULL,
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

CREATE TABLE billing.referral_redemptions (
    id                         UUID PRIMARY KEY,
    reservation_id             UUID NOT NULL UNIQUE REFERENCES billing.referral_reservations(id) ON DELETE RESTRICT,
    campaign_id                UUID NOT NULL REFERENCES billing.promotion_campaigns(id) ON DELETE RESTRICT,
    wallet_id                  UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id                   UUID NOT NULL,
    owner_type                 billing.owner_type NOT NULL,
    redemption_kind            VARCHAR(32) NOT NULL,
    payment_intent_id          UUID NOT NULL UNIQUE REFERENCES billing.payment_intents(id) ON DELETE RESTRICT,
    credit_grant_id            UUID NOT NULL UNIQUE REFERENCES billing.credit_grants(id) ON DELETE RESTRICT,
    amount_micro_units         BIGINT NOT NULL,
    currency                   CHAR(3) NOT NULL,
    redeemed_at                TIMESTAMPTZ NOT NULL,
    CONSTRAINT uq_referral_redemption_owner_kind
        UNIQUE (owner_id, owner_type, redemption_kind),
    CONSTRAINT ck_referral_redemption_kind
        CHECK (redemption_kind IN ('ONBOARDING', 'EXTENSION')),
    CONSTRAINT ck_referral_redemption_amount
        CHECK (amount_micro_units > 0)
);

CREATE INDEX idx_referral_reservations_campaign_capacity
    ON billing.referral_reservations (campaign_id, status, expires_at);
CREATE UNIQUE INDEX uq_referral_reservation_live_owner
    ON billing.referral_reservations (owner_id, owner_type, redemption_kind)
    WHERE status = 'RESERVED';
CREATE INDEX idx_payment_intents_owner_created
    ON billing.payment_intents (owner_id, owner_type, created_at DESC);
CREATE INDEX idx_payment_intents_actor_created
    ON billing.payment_intents (actor_user_id, created_at DESC);
CREATE INDEX idx_payment_intents_pending_expiry
    ON billing.payment_intents (expires_at)
    WHERE status = 'PENDING';
CREATE INDEX idx_referral_campaign_catalog
    ON billing.promotion_campaigns (campaign_type, status, starts_at, ends_at);
