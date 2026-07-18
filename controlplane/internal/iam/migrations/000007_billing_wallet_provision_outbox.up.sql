-- [COMMENT]: Domain outbox tách khỏi iam_outbox_records vốn dành cho provisioning/mail jobs.
CREATE TABLE IF NOT EXISTS billing_wallet_provision_outbox (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    owner_id UUID NOT NULL,
    schema_version INT NOT NULL CHECK (schema_version = 1),
    payload BYTEA NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PUBLISHING', 'PUBLISHED', 'DEAD')),
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_billing_wallet_provision_outbox_claim
ON billing_wallet_provision_outbox (available_at, id)
WHERE status IN ('PENDING', 'PUBLISHING');
