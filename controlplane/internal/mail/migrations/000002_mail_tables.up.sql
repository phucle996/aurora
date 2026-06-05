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

CREATE TABLE IF NOT EXISTS mail_endpoints (
    id VARCHAR(64) PRIMARY KEY, -- ID endpoint duy nhất
    zone_id VARCHAR(64) NOT NULL, -- ID zone sở hữu endpoint
    name VARCHAR(255) NOT NULL, -- Tên hiển thị của endpoint
    host VARCHAR(255) NOT NULL, -- Địa chỉ host của mail server
    port INT NOT NULL, -- Cổng kết nối SMTP
    username VARCHAR(255), -- Tên đăng nhập SMTP
    password TEXT, -- Mật khẩu hoặc API Key (mã hóa phong bì AES-256-GCM)
    tls_mode VARCHAR(50) NOT NULL DEFAULT 'starttls', -- Chế độ bảo mật TLS (none, starttls, tls, mtls)
    status mail_endpoint_status NOT NULL DEFAULT 'planned', -- Trạng thái lập kế hoạch (planned, active, disabled)
    max_connections INT NOT NULL DEFAULT 10, -- Số lượng kết nối song song tối đa
    priority INT NOT NULL DEFAULT 100, -- Độ ưu tiên định tuyến
    weight INT NOT NULL DEFAULT 1, -- Trọng số định tuyến
    ca_cert_pem TEXT, -- Chứng chỉ CA Root (PEM)
    client_cert_pem TEXT, -- Chứng chỉ Client (PEM)
    client_key_pem TEXT, -- Khóa riêng tư Client (PEM, mã hóa phong bì AES-256-GCM)
    is_active BOOLEAN NOT NULL DEFAULT TRUE, -- Trạng thái hoạt động của endpoint
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP, -- Thời điểm tạo endpoint
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP -- Thời điểm cập nhật endpoint gần nhất
);

COMMENT ON TABLE mail_endpoints IS 'Stores connection configurations to SMTP-compatible mail senders. Sensitive fields (password, client_key_pem) must be fully encrypted.';
COMMENT ON COLUMN mail_endpoints.id IS 'Primary key of the mail endpoint record.';
COMMENT ON COLUMN mail_endpoints.zone_id IS 'Unique identifier for the zone scope. Ensures zone-based physical delivery isolation.';
COMMENT ON COLUMN mail_endpoints.name IS 'Human-readable name of the physical endpoint.';
COMMENT ON COLUMN mail_endpoints.host IS 'Mail server host address.';
COMMENT ON COLUMN mail_endpoints.port IS 'Mail server port.';
COMMENT ON COLUMN mail_endpoints.username IS 'SMTP authentication username.';
COMMENT ON COLUMN mail_endpoints.password IS 'SMTP authentication password. Encrypted using AES-256-GCM.';
COMMENT ON COLUMN mail_endpoints.tls_mode IS 'TLS mode: none, starttls, tls, mtls.';
COMMENT ON COLUMN mail_endpoints.status IS 'Lifecycle status of the endpoint: planned, active, disabled.';
COMMENT ON COLUMN mail_endpoints.max_connections IS 'Maximum concurrent connections to this endpoint.';
COMMENT ON COLUMN mail_endpoints.priority IS 'Routing priority for the endpoint.';
COMMENT ON COLUMN mail_endpoints.weight IS 'Routing weight for load balancing.';
COMMENT ON COLUMN mail_endpoints.ca_cert_pem IS 'CA root certificate PEM block.';
COMMENT ON COLUMN mail_endpoints.client_cert_pem IS 'Client certificate PEM block.';
COMMENT ON COLUMN mail_endpoints.client_key_pem IS 'Client private key PEM block. Encrypted using AES-256-GCM.';
COMMENT ON COLUMN mail_endpoints.is_active IS 'Indicates if this delivery endpoint is healthy and ready to process sent jobs.';
COMMENT ON COLUMN mail_endpoints.created_at IS 'Timestamp when the endpoint record was created.';
COMMENT ON COLUMN mail_endpoints.updated_at IS 'Timestamp when the endpoint record was last updated.';
