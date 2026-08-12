-- Local Storage admission projection fed by the durable Billing wallet event.
-- It is intentionally not a foreign key to Billing: the projection is rebuilt
-- from the versioned Shared Redis stream and must fail closed when stale.
CREATE TABLE IF NOT EXISTS wallet_admission_projection (
    owner_id             UUID NOT NULL,
    owner_type           TEXT NOT NULL,
    wallet_version       BIGINT NOT NULL,
    admission_mode       TEXT NOT NULL,
    restriction_reason   TEXT,
    effective_at         TIMESTAMPTZ NOT NULL,
    valid_until          TIMESTAMPTZ,
    source_event_id      UUID NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_id, owner_type),
    CONSTRAINT ck_wallet_admission_projection_owner_type
        CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    CONSTRAINT ck_wallet_admission_projection_mode
        CHECK (admission_mode IN ('ALLOW', 'SUSPEND_BILLABLE')),
    CONSTRAINT ck_wallet_admission_projection_reason
        CHECK ((admission_mode = 'ALLOW' AND restriction_reason IS NULL)
            OR (admission_mode = 'SUSPEND_BILLABLE' AND restriction_reason IS NOT NULL)),
    CONSTRAINT ck_wallet_admission_projection_window
        CHECK (valid_until IS NULL OR valid_until > effective_at)
);

CREATE TABLE IF NOT EXISTS resource_admission_projection (
    resource_id          UUID NOT NULL,
    resource_name        TEXT NOT NULL,
    zone_id              UUID NOT NULL,
    owner_id             UUID NOT NULL,
    owner_type           TEXT NOT NULL,
    wallet_version       BIGINT NOT NULL,
    admission_mode       TEXT NOT NULL,
    restriction_reason   TEXT,
    effective_at         TIMESTAMPTZ NOT NULL,
    valid_until          TIMESTAMPTZ,
    source_event_id      UUID NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (resource_id, zone_id),
    CONSTRAINT ck_resource_admission_owner_type
        CHECK (owner_type IN ('PERSONAL', 'TENANT')),
    CONSTRAINT ck_resource_admission_mode
        CHECK (admission_mode IN ('ALLOW', 'SUSPEND_BILLABLE')),
    CONSTRAINT ck_resource_admission_reason
        CHECK ((admission_mode = 'ALLOW' AND restriction_reason IS NULL)
            OR (admission_mode = 'SUSPEND_BILLABLE' AND restriction_reason IS NOT NULL)),
    CONSTRAINT ck_resource_admission_window
        CHECK (valid_until IS NULL OR valid_until > effective_at)
);
CREATE INDEX IF NOT EXISTS idx_resource_admission_owner
    ON resource_admission_projection(owner_id, owner_type, zone_id);
