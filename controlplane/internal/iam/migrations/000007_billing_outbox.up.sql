-- [COMMENT]: Outbox dùng chung cho mọi domain event từ IAM sang Billing; event_type mới không cần tạo thêm bảng.
CREATE TABLE IF NOT EXISTS billing_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE,
    event_type VARCHAR(128) NOT NULL,
    schema_version INT NOT NULL CHECK (schema_version > 0),
    aggregate_type VARCHAR(64) NOT NULL,
    aggregate_id UUID NOT NULL,
    aggregate_version BIGINT NOT NULL CHECK (aggregate_version > 0),
    owner_id UUID NOT NULL,
    owner_type billing_owner_type NOT NULL,
    actor_user_id UUID NOT NULL,
    payload BYTEA NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'PUBLISHING', 'PUBLISHED', 'DEAD')),
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    last_error TEXT,
    trace_id BYTEA,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_billing_outbox_event_type_format
        CHECK (event_type ~ '^[a-z0-9]+([._][a-z0-9]+)*\.v[1-9][0-9]*$'),
    CONSTRAINT ck_billing_outbox_trace_id
        CHECK (trace_id IS NULL OR octet_length(trace_id) = 16)
);

-- [COMMENT]: Partial index giữ hot working set nhỏ cho nhiều relay pod dùng SKIP LOCKED.
CREATE INDEX IF NOT EXISTS idx_billing_outbox_claim
ON billing_outbox_records (available_at, id)
WHERE status IN ('PENDING', 'PUBLISHING');

CREATE INDEX IF NOT EXISTS idx_billing_outbox_owner_audit
ON billing_outbox_records (owner_type, owner_id, created_at DESC);

-- [COMMENT]: Cleanup 30 ngày chỉ scan terminal hot range thay vì quét toàn bộ outbox đang tăng trưởng.
CREATE INDEX IF NOT EXISTS idx_billing_outbox_published_cleanup
ON billing_outbox_records (published_at, id)
WHERE status = 'PUBLISHED' AND published_at IS NOT NULL;
