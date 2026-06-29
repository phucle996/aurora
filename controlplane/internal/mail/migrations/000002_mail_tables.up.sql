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
    id VARCHAR(64) PRIMARY KEY, -- ID template duy nhất (ví dụ: platform/verify_account hoặc workspace_abc/verify_account)
    name VARCHAR(255) NOT NULL, -- Tên định danh của template
    subject VARCHAR(255) NOT NULL, -- Tiêu đề email mẫu, hỗ trợ placeholder biến động
    body TEXT NOT NULL, -- Nội dung email định dạng HTML/Text hỗ trợ template engines
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP, -- Thời điểm tạo template
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP -- Thời điểm cập nhật template gần nhất
);

COMMENT ON TABLE mail_templates IS 'Stores email templates containing body template content with variable interpolation placeholders.';
COMMENT ON COLUMN mail_templates.id IS 'Primary key of the mail template record.';
COMMENT ON COLUMN mail_templates.name IS 'Unique name identifier for the template within the tenant scope.';
COMMENT ON COLUMN mail_templates.subject IS 'Email subject line template containing dynamic variables.';
COMMENT ON COLUMN mail_templates.body IS 'Body template content with template syntax (HTML or text).';
COMMENT ON COLUMN mail_templates.created_at IS 'Timestamp when the template record was created.';
COMMENT ON COLUMN mail_templates.updated_at IS 'Timestamp when the template record was last updated.';


