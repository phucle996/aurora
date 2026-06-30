-- Tạo kiểu ENUM cho trạng thái sức khỏe vận hành
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'zone_service_status') THEN
        CREATE TYPE zone_service_status AS ENUM ('unknown', 'healthy', 'degraded', 'unhealthy', 'down');
    END IF;
END
$$;

-- Bổ sung các cột động vào bảng zone_services
ALTER TABLE hierarchy.zone_services
ADD COLUMN IF NOT EXISTS status zone_service_status NOT NULL DEFAULT 'unknown',
ADD COLUMN IF NOT EXISTS capacity INT NOT NULL DEFAULT 0,
ADD COLUMN IF NOT EXISTS last_heartbeat_at TIMESTAMPTZ;

COMMENT ON COLUMN hierarchy.zone_services.status IS 'Trạng thái sức khỏe vận hành thực tế của dịch vụ tại Zone.';
COMMENT ON COLUMN hierarchy.zone_services.capacity IS 'Năng lực xử lý thực tế của dịch vụ trong Zone (0-100%).';
COMMENT ON COLUMN hierarchy.zone_services.last_heartbeat_at IS 'Thời điểm gần nhất nhận được báo cáo sức khỏe từ Dataplane.';
