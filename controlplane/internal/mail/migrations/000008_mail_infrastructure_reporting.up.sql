-- [COMMENT]: Consumer slot vẫn là relational current read model, nhưng gắn thêm physical process
-- để Admin có thể lần từ logical slot về Dataplane node mà không lộ thông tin này cho customer API.
ALTER TABLE mail_consumer_runtime_reports
    ADD COLUMN IF NOT EXISTS runtime_node_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS runtime_boot_id UUID;

-- [COMMENT]: Infrastructure là một atomic current snapshot cho mỗi Zone. JSONB tránh N row writes
-- mỗi chu kỳ health và giữ inventory nhất quán: UI không bao giờ thấy nửa snapshot cũ, nửa snapshot mới.
CREATE TABLE IF NOT EXISTS mail_infrastructure_reports (
    zone_id UUID PRIMARY KEY REFERENCES hierarchy.zones(id) ON DELETE CASCADE,
    event_id UUID NOT NULL,
    report_generation BIGINT NOT NULL CHECK (report_generation > 0),
    report_sequence BIGINT NOT NULL CHECK (report_sequence > 0),
    service_state VARCHAR(16) NOT NULL CHECK (service_state IN ('healthy','degraded','unhealthy','down')),
    capacity INTEGER NOT NULL CHECK (capacity BETWEEN 0 AND 100),
    pending_items BIGINT NOT NULL CHECK (pending_items >= 0),
    in_flight_batches BIGINT NOT NULL CHECK (in_flight_batches >= 0),
    probe_node_id VARCHAR(255) NOT NULL,
    dataplane_nodes JSONB NOT NULL DEFAULT '[]'::jsonb,
    stalwart_nodes JSONB NOT NULL DEFAULT '[]'::jsonb,
    inventory_truncated BOOLEAN NOT NULL DEFAULT false,
    error_code VARCHAR(100),
    reported_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_mail_infrastructure_expiry CHECK (expires_at > reported_at),
    CONSTRAINT ck_mail_infrastructure_dataplane_nodes CHECK (jsonb_typeof(dataplane_nodes) = 'array'),
    CONSTRAINT ck_mail_infrastructure_stalwart_nodes CHECK (jsonb_typeof(stalwart_nodes) = 'array')
);

CREATE INDEX IF NOT EXISTS idx_mail_infrastructure_reports_expiry
ON mail_infrastructure_reports (expires_at, zone_id);

COMMENT ON TABLE mail_infrastructure_reports IS
'Reporter-owned current Mail infrastructure snapshot. This projection is read-only to Controlplane HTTP flows and contains no credential or customer mail payload.';
