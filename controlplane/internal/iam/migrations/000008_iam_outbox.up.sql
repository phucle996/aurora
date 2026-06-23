-- IAM migration layer 000008
-- Bảng outbox lưu trữ các sự kiện/tác vụ bất đồng bộ của module IAM để CDC đồng bộ sang Redis/Kafka

CREATE TABLE IF NOT EXISTS iam_outbox_records (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID UNIQUE NOT NULL, -- UUID định danh duy nhất của sự kiện (Idempotency Key)
    routing_scope VARCHAR(100) NOT NULL, -- Phạm vi định tuyến và thực thi (e.g. platform, zone:vn)
    job_topic VARCHAR(100) NOT NULL, -- Tên topic/tác vụ (e.g. mail.system.verify_account)
    payload BYTEA NOT NULL, -- Dữ liệu nhị phân serialized dạng Protobuf
    user_id VARCHAR(64) NOT NULL, -- ID người dùng kích hoạt hành động này
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PUBLISHED', 'PROCESSING', 'COMPLETED', 'SUCCEEDED', 'FAILED')),
    completed_at TIMESTAMP WITH TIME ZONE, -- Thời gian hoàn tất tác vụ

    -- CÁC CỘT ĐỒNG BỘ CONTRACT:
    job_version INT NOT NULL DEFAULT 1,
    resource_id VARCHAR(64),
    payload_schema_version INT NOT NULL DEFAULT 1,
    trace_id BYTEA, -- Trích xuất OpenTelemetry trace parent để liên kết vết
    idle INT, -- Hạn mức timeout cho tác vụ tính bằng giây

    -- CÁC CỘT PHẢN HỒI KẾT QUẢ:
    error_code VARCHAR(100),
    error_message TEXT
);

-- Tạo Index tối ưu hóa việc quét các bản ghi PENDING nhanh hơn (CDC query)
CREATE INDEX IF NOT EXISTS idx_iam_outbox_pending 
ON iam_outbox_records (status, id ASC) 
WHERE status = 'PENDING';
