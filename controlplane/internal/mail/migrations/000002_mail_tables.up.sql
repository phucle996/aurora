-- Mail migration layer 000002
-- Source of truth tables for mail consumers, templates, gateways, and endpoints.

CREATE TABLE IF NOT EXISTS mail_consumers (
    id VARCHAR(64) PRIMARY KEY, -- ID consumer duy nhất
    tenant_id VARCHAR(64) NOT NULL, -- ID tenant sở hữu consumer để phân tách dữ liệu (Tenant Isolation)
    name VARCHAR(255) NOT NULL, -- Tên hiển thị của consumer
    source_type mail_source_type NOT NULL, -- Loại nguồn dữ liệu job (ví dụ: kafka, redis_stream)
    source_config_ref VARCHAR(255) NOT NULL, -- Tham chiếu tới cấu hình kết nối nguồn dữ liệu trong secret store
    status mail_consumer_status NOT NULL DEFAULT 'paused', -- Trạng thái hoạt động của consumer
    parallelism INT NOT NULL DEFAULT 1, -- Số lượng worker xử lý đồng thời
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP, -- Thời điểm tạo consumer
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP -- Thời điểm cập nhật consumer gần nhất
);

COMMENT ON TABLE mail_consumers IS 'Stores background queue/stream consumers and their concurrency control parameters.';
COMMENT ON COLUMN mail_consumers.id IS 'Primary key of the mail consumer record.';
COMMENT ON COLUMN mail_consumers.tenant_id IS 'Unique identifier for the tenant scope. Ensures multi-tenant isolation.';
COMMENT ON COLUMN mail_consumers.name IS 'Human-readable name of the consumer.';
COMMENT ON COLUMN mail_consumers.source_type IS 'Underlying message broker or stream technology (kafka, redis_stream, rabbitmq, nats).';
COMMENT ON COLUMN mail_consumers.source_config_ref IS 'Reference to credentials/connection details inside secure vault or configuration system.';
COMMENT ON COLUMN mail_consumers.status IS 'Current activation status: enabled, paused, error, draining.';
COMMENT ON COLUMN mail_consumers.parallelism IS 'Concurrency degree defining the maximum parallel active consumer worker processes.';
COMMENT ON COLUMN mail_consumers.created_at IS 'Timestamp when the consumer record was created.';
COMMENT ON COLUMN mail_consumers.updated_at IS 'Timestamp when the consumer record was last updated.';

CREATE TABLE IF NOT EXISTS mail_templates (
    id VARCHAR(64) PRIMARY KEY, -- ID template duy nhất
    tenant_id VARCHAR(64) NOT NULL, -- ID tenant sở hữu template
    name VARCHAR(255) NOT NULL, -- Tên định danh của template
    subject VARCHAR(255) NOT NULL, -- Tiêu đề email mẫu, hỗ trợ placeholder biến động
    body_html TEXT NOT NULL, -- Nội dung email định dạng HTML hỗ trợ template engines
    body_text TEXT, -- Nội dung email fallback dạng text thuần (nullable)
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP, -- Thời điểm tạo template
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP -- Thời điểm cập nhật template gần nhất
);

COMMENT ON TABLE mail_templates IS 'Stores email templates containing both HTML and fallback plain text with variable interpolation placeholders.';
COMMENT ON COLUMN mail_templates.id IS 'Primary key of the mail template record.';
COMMENT ON COLUMN mail_templates.tenant_id IS 'Unique identifier for the tenant scope. Ensures multi-tenant isolation.';
COMMENT ON COLUMN mail_templates.name IS 'Unique name identifier for the template within the tenant scope.';
COMMENT ON COLUMN mail_templates.subject IS 'Email subject line template containing dynamic variables.';
COMMENT ON COLUMN mail_templates.body_html IS 'HTML body content with template syntax.';
COMMENT ON COLUMN mail_templates.body_text IS 'Optional fallback text body content for simple mail clients.';
COMMENT ON COLUMN mail_templates.created_at IS 'Timestamp when the template record was created.';
COMMENT ON COLUMN mail_templates.updated_at IS 'Timestamp when the template record was last updated.';

CREATE TABLE IF NOT EXISTS mail_gateways (
    id VARCHAR(64) PRIMARY KEY, -- ID gateway duy nhất
    tenant_id VARCHAR(64) NOT NULL, -- ID tenant sở hữu gateway
    name VARCHAR(255) NOT NULL, -- Tên hiển thị của gateway
    route_policy VARCHAR(100) NOT NULL, -- Luật định tuyến chọn endpoint vật lý
    is_active BOOLEAN NOT NULL DEFAULT TRUE, -- Trạng thái hoạt động của gateway
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP, -- Thời điểm tạo gateway
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP -- Thời điểm cập nhật gateway gần nhất
);

COMMENT ON TABLE mail_gateways IS 'Stores mail gateways that direct emails to endpoints using defined route policies.';
COMMENT ON COLUMN mail_gateways.id IS 'Primary key of the mail gateway record.';
COMMENT ON COLUMN mail_gateways.tenant_id IS 'Unique identifier for the tenant scope. Ensures multi-tenant isolation.';
COMMENT ON COLUMN mail_gateways.name IS 'Human-readable name of the gateway.';
COMMENT ON COLUMN mail_gateways.route_policy IS 'Routing policy configuration or key determining physical endpoint target prioritization.';
COMMENT ON COLUMN mail_gateways.is_active IS 'Indicates if this routing gateway is enabled and actively forwarding jobs.';
COMMENT ON COLUMN mail_gateways.created_at IS 'Timestamp when the gateway record was created.';
COMMENT ON COLUMN mail_gateways.updated_at IS 'Timestamp when the gateway record was last updated.';


