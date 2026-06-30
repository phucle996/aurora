-- Trigger hàm tự động ghi nhận thay đổi cấu hình vào iam.iam_outbox_records để CDC đẩy sang Redis L1
CREATE OR REPLACE FUNCTION hierarchy.notify_zone_metadata_change()
RETURNS TRIGGER AS $$
DECLARE
    v_event_id UUID := gen_random_uuid();
    v_payload JSONB;
    v_payload_bytes BYTEA;
    v_zone_id UUID;
BEGIN
    -- Xác định zone_id dựa trên bảng bị thay đổi
    IF TG_TABLE_NAME = 'zones' THEN
        v_zone_id := NEW.id;
        v_payload := jsonb_build_object(
            'event_type', 'zone_status_changed',
            'zone_id', v_zone_id,
            'status', NEW.status
        );
    ELSIF TG_TABLE_NAME = 'zone_services' THEN
        v_zone_id := NEW.zone_id;
        v_payload := jsonb_build_object(
            'event_type', 'service_status_changed',
            'zone_id', v_zone_id,
            'service', NEW.service_type,
            'enabled', NEW.enabled
        );
    END IF;

    -- Chuyển đổi JSON text thành BYTEA nhị phân thô
    v_payload_bytes := decode(encode(v_payload::text::bytea, 'escape'), 'escape');

    -- Chèn bản ghi outbox vào schema iam để CDC bắt được
    INSERT INTO iam.iam_outbox_records (
        event_id,
        routing_scope,
        job_topic,
        payload,
        user_id,
        status,
        job_version,
        resource_id,
        payload_schema_version
    ) VALUES (
        v_event_id,
        'global',
        'zone.metadata.update',
        v_payload_bytes,
        'system',
        'PENDING',
        1,
        v_zone_id::text,
        1
    );

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Tạo triggers cho bảng zones
DROP TRIGGER IF EXISTS trg_zone_metadata_change ON hierarchy.zones;
CREATE TRIGGER trg_zone_metadata_change
AFTER INSERT OR UPDATE OF status ON hierarchy.zones
FOR EACH ROW
EXECUTE FUNCTION hierarchy.notify_zone_metadata_change();

-- Tạo triggers cho bảng zone_services
DROP TRIGGER IF EXISTS trg_zone_service_change ON hierarchy.zone_services;
CREATE TRIGGER trg_zone_service_change
AFTER INSERT OR UPDATE OF enabled ON hierarchy.zone_services
FOR EACH ROW
EXECUTE FUNCTION hierarchy.notify_zone_metadata_change();
