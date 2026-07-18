CREATE TABLE IF NOT EXISTS billing.wallet_provision_inbox (
    event_id UUID PRIMARY KEY,
    schema_version INT NOT NULL CHECK (schema_version = 1),
    owner_id UUID NOT NULL,
    payload_hash VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'RECEIVED' CHECK (status IN ('RECEIVED', 'APPLIED', 'DEAD')),
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);
