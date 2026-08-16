-- Rebuildable Central admission read models and durable Zone handoff.
CREATE TABLE commercial_admission_projection (
    owner_id UUID NOT NULL,
    owner_type TEXT NOT NULL,
    policy_version BIGINT NOT NULL,
    decision TEXT NOT NULL,
    restriction_reason TEXT,
    effective_at TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ,
    source_event_id UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_id, owner_type),
    CONSTRAINT ck_commercial_admission_projection_owner_type
        CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    CONSTRAINT ck_commercial_admission_projection_version
        CHECK (policy_version > 0),
    CONSTRAINT ck_commercial_admission_projection_mode
        CHECK (decision IN ('ALLOW', 'SUSPEND_BILLABLE')),
    CONSTRAINT ck_commercial_admission_projection_reason CHECK (
        (decision = 'ALLOW' AND restriction_reason IS NULL)
        OR (decision = 'SUSPEND_BILLABLE' AND restriction_reason IS NOT NULL)
    ),
    CONSTRAINT ck_commercial_admission_projection_window
        CHECK (valid_until IS NULL OR valid_until > effective_at)
);

CREATE TABLE resource_admission_projection (
    resource_id UUID NOT NULL,
    resource_name TEXT NOT NULL,
    zone_id UUID NOT NULL,
    owner_id UUID NOT NULL,
    owner_type TEXT NOT NULL,
    policy_version BIGINT NOT NULL,
    decision TEXT NOT NULL,
    restriction_reason TEXT,
    effective_at TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ,
    source_event_id UUID NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (resource_id, zone_id),
    CONSTRAINT ck_resource_admission_owner_type
        CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    CONSTRAINT ck_resource_admission_version
        CHECK (policy_version > 0),
    CONSTRAINT ck_resource_decision
        CHECK (decision IN ('ALLOW', 'SUSPEND_BILLABLE')),
    CONSTRAINT ck_resource_admission_reason CHECK (
        (decision = 'ALLOW' AND restriction_reason IS NULL)
        OR (decision = 'SUSPEND_BILLABLE' AND restriction_reason IS NOT NULL)
    ),
    CONSTRAINT ck_resource_admission_window
        CHECK (valid_until IS NULL OR valid_until > effective_at)
);

CREATE INDEX idx_resource_admission_owner
    ON resource_admission_projection(owner_id, owner_type, zone_id);

CREATE TABLE commercial_admission_zone_outbox (
    resource_id UUID NOT NULL,
    zone_id UUID NOT NULL,
    source_event_id UUID NOT NULL,
    policy_version BIGINT NOT NULL,
    payload BYTEA NOT NULL,
    claim_token UUID,
    claimed_at TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (resource_id, zone_id),
    CONSTRAINT ck_commercial_admission_zone_outbox_version
        CHECK (policy_version > 0),
    CONSTRAINT ck_commercial_admission_zone_outbox_payload
        CHECK (octet_length(payload) > 0),
    CONSTRAINT ck_commercial_admission_zone_outbox_claim
        CHECK ((claim_token IS NULL) = (claimed_at IS NULL))
);

CREATE INDEX idx_commercial_admission_zone_outbox_pending
    ON commercial_admission_zone_outbox(published_at, claimed_at, updated_at)
    WHERE published_at IS NULL;
