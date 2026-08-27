-- [COMMENT]: Migration 000004_managed_service_funcs.up.sql
-- Khởi tạo tất cả các PL/pgSQL Stored Functions bảo vệ tính bất biến và toàn vẹn dữ liệu cho Managed Service Platform.

-- 1. Hàm từ chối thay đổi nội dung Blueprint Revision khi đã Publish/Retired
CREATE OR REPLACE FUNCTION reject_blueprint_revision_rewrite()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.blueprint_id IS DISTINCT FROM OLD.blueprint_id
       OR NEW.revision IS DISTINCT FROM OLD.revision
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'blueprint revision identity is immutable';
    END IF;

    IF OLD.state <> 'draft' AND (
        NEW.template_yaml IS DISTINCT FROM OLD.template_yaml
        OR NEW.template_bundle_sha256 IS DISTINCT FROM OLD.template_bundle_sha256
        OR NEW.contract_version IS DISTINCT FROM OLD.contract_version
        OR NEW.contract_sha256 IS DISTINCT FROM OLD.contract_sha256
        OR NEW.component_contract IS DISTINCT FROM OLD.component_contract
        OR NEW.component_contract_sha256 IS DISTINCT FROM OLD.component_contract_sha256
        OR NEW.input_schema IS DISTINCT FROM OLD.input_schema
        OR NEW.input_schema_sha256 IS DISTINCT FROM OLD.input_schema_sha256
        OR NEW.ui_schema IS DISTINCT FROM OLD.ui_schema
        OR NEW.ui_schema_sha256 IS DISTINCT FROM OLD.ui_schema_sha256
        OR NEW.safe_observed_output_schema IS DISTINCT FROM OLD.safe_observed_output_schema
        OR NEW.safe_observed_output_schema_sha256 IS DISTINCT FROM OLD.safe_observed_output_schema_sha256
        OR NEW.zone_selector IS DISTINCT FROM OLD.zone_selector
        OR NEW.zone_selector_sha256 IS DISTINCT FROM OLD.zone_selector_sha256
        OR NEW.capability_requirement IS DISTINCT FROM OLD.capability_requirement
        OR NEW.capability_requirement_sha256 IS DISTINCT FROM OLD.capability_requirement_sha256
        OR NEW.row_version IS DISTINCT FROM OLD.row_version
        OR NEW.validated_row_version IS DISTINCT FROM OLD.validated_row_version
        OR NEW.validation_contract_version IS DISTINCT FROM OLD.validation_contract_version
        OR NEW.validated_bundle_sha256 IS DISTINCT FROM OLD.validated_bundle_sha256
        OR NEW.validated_contract_sha256 IS DISTINCT FROM OLD.validated_contract_sha256
        OR NEW.validated_at IS DISTINCT FROM OLD.validated_at
        OR NEW.validated_by IS DISTINCT FROM OLD.validated_by
    ) THEN
        RAISE EXCEPTION 'blueprint revision content is immutable';
    END IF;

    IF OLD.state = 'draft' THEN
        IF NEW.state NOT IN ('draft', 'published') OR NEW.retired_at IS NOT NULL OR NEW.retired_by IS NOT NULL THEN
            RAISE EXCEPTION 'draft blueprint revision may only remain draft or publish';
        END IF;
        IF NEW.state = 'draft' AND (NEW.published_at IS NOT NULL OR NEW.published_by IS NOT NULL) THEN
            RAISE EXCEPTION 'draft blueprint revision cannot have publish metadata';
        END IF;
        IF NEW.state = 'published' AND (
            NEW.published_at IS NULL
            OR NEW.published_by IS NULL
            OR NEW.validated_row_version IS DISTINCT FROM NEW.row_version
            OR NEW.validated_bundle_sha256 IS DISTINCT FROM NEW.template_bundle_sha256
            OR NEW.validated_contract_sha256 IS DISTINCT FROM NEW.contract_sha256
        ) THEN
            RAISE EXCEPTION 'published blueprint revision requires current validation evidence';
        END IF;
        RETURN NEW;
    END IF;

    IF OLD.state = 'published'
       AND NEW.state = 'retired'
       AND NEW.published_at IS NOT DISTINCT FROM OLD.published_at
       AND NEW.published_by IS NOT DISTINCT FROM OLD.published_by
       AND NEW.retired_at IS NOT NULL
       AND NEW.retired_by IS NOT NULL THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'published or retired blueprint revision transition is immutable';
END;
$$;

-- 2. Hàm kiểm tra revision được gán làm published_revision_id có thực sự thuộc Blueprint và đã Published hay chưa
CREATE OR REPLACE FUNCTION validate_service_blueprint_published_revision()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.published_revision_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM blueprint_revisions revision
        WHERE revision.id = NEW.published_revision_id
          AND revision.blueprint_id = NEW.id
          AND revision.state = 'published'
    ) THEN
        RAISE EXCEPTION 'published revision must belong to blueprint and be published';
    END IF;
    RETURN NEW;
END;
$$;

-- 3. Hàm kiểm tra revision đang làm mặc định luôn luôn phải ở trạng thái published
CREATE OR REPLACE FUNCTION validate_blueprint_revision_default_pointer()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.state <> 'published' AND EXISTS (
        SELECT 1 FROM service_blueprints blueprint
        WHERE blueprint.published_revision_id = NEW.id
    ) THEN
        RAISE EXCEPTION 'default blueprint revision must remain published';
    END IF;
    RETURN NEW;
END;
$$;

-- 4. Hàm từ chối chỉnh sửa nội dung Payload của Outbox Event
CREATE OR REPLACE FUNCTION reject_managed_service_outbox_payload_rewrite()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.event_id IS DISTINCT FROM OLD.event_id
       OR NEW.zone_id IS DISTINCT FROM OLD.zone_id
       OR NEW.job_topic IS DISTINCT FROM OLD.job_topic
       OR NEW.payload IS DISTINCT FROM OLD.payload
       OR NEW.payload_key_id IS DISTINCT FROM OLD.payload_key_id
       OR NEW.owner_id IS DISTINCT FROM OLD.owner_id
       OR NEW.owner_type IS DISTINCT FROM OLD.owner_type
       OR NEW.actor_user_id IS DISTINCT FROM OLD.actor_user_id
       OR NEW.available_at IS DISTINCT FROM OLD.available_at
       OR NEW.job_version IS DISTINCT FROM OLD.job_version
       OR NEW.resource_id IS DISTINCT FROM OLD.resource_id
       OR NEW.payload_schema_version IS DISTINCT FROM OLD.payload_schema_version
       OR NEW.trace_id IS DISTINCT FROM OLD.trace_id
       OR NEW.idle IS DISTINCT FROM OLD.idle
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'managed service outbox intent is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION require_managed_service_instance_deleting_before_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.state <> 'deleting' THEN
        RAISE EXCEPTION 'managed service instance % cannot be deleted from state %', OLD.id, OLD.state
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END;
$$;
