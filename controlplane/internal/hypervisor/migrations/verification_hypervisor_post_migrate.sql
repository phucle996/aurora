DO $$
DECLARE
    shared_outbox_count INT;
BEGIN
    IF to_regclass('hypervisor.image_artifacts') IS NULL
       OR to_regclass('hypervisor.personal_vms') IS NULL
       OR to_regclass('hypervisor.hypervisor_outbox_records') IS NULL THEN
        RAISE EXCEPTION 'hypervisor resource or outbox table is missing';
    END IF;

    IF to_regclass('hypervisor.nodes') IS NOT NULL
       OR to_regclass('hypervisor.vm_outbox_records') IS NOT NULL
       OR to_regclass('hypervisor.image_outbox_records') IS NOT NULL THEN
        RAISE EXCEPTION 'legacy hypervisor node/split-outbox table still exists';
    END IF;

    SELECT count(*)
    INTO shared_outbox_count
    FROM pg_class relation
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = 'hypervisor'
      AND relation.relkind = 'r'
      AND relation.relname LIKE '%outbox_records';

    IF shared_outbox_count <> 1 THEN
        RAISE EXCEPTION 'hypervisor must have exactly one outbox table, found %',
            shared_outbox_count;
    END IF;

    IF (
        SELECT array_agg(enum_value.enumlabel::text ORDER BY enum_value.enumsortorder)
        FROM pg_enum enum_value
        JOIN pg_type enum_type ON enum_type.oid = enum_value.enumtypid
        JOIN pg_namespace namespace ON namespace.oid = enum_type.typnamespace
        WHERE namespace.nspname = 'hypervisor'
          AND enum_type.typname = 'hypervisor_vm_status'
    ) IS DISTINCT FROM ARRAY['PROVISIONING', 'READY', 'DELETING', 'FAILED']::text[] THEN
        RAISE EXCEPTION 'personal VM enum must contain exactly PROVISIONING, READY, DELETING and FAILED';
    END IF;

    IF (
        SELECT array_agg(enum_value.enumlabel::text ORDER BY enum_value.enumsortorder)
        FROM pg_enum enum_value
        JOIN pg_type enum_type ON enum_type.oid = enum_value.enumtypid
        JOIN pg_namespace namespace ON namespace.oid = enum_type.typnamespace
        WHERE namespace.nspname = 'hypervisor'
          AND enum_type.typname = 'hypervisor_image_state'
    ) IS DISTINCT FROM ARRAY[
        'UPLOADING',
        'IMPORTING',
        'AVAILABLE',
        'QUARANTINED',
        'FAILED',
        'DELETING'
    ]::text[] THEN
        RAISE EXCEPTION 'hypervisor image enum labels are not the clean baseline';
    END IF;

    IF (
        SELECT array_agg(enum_value.enumlabel::text ORDER BY enum_value.enumsortorder)
        FROM pg_enum enum_value
        JOIN pg_type enum_type ON enum_type.oid = enum_value.enumtypid
        JOIN pg_namespace namespace ON namespace.oid = enum_type.typnamespace
        WHERE namespace.nspname = 'hypervisor'
          AND enum_type.typname = 'hypervisor_outbox_status'
    ) IS DISTINCT FROM ARRAY[
        'PENDING',
        'PROCESSING',
        'SUCCEEDED',
        'FAILED',
        'DEAD'
    ]::text[] THEN
        RAISE EXCEPTION 'hypervisor outbox enum labels are not the clean baseline';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'hypervisor'
          AND (
              (table_name = 'image_artifacts'
               AND column_name IN (
                   'provider_node', 'provider_storage', 'deleted_at'
               ))
              OR
              (table_name = 'personal_vms'
               AND column_name IN (
                   'provider_node', 'error_code', 'error_message'
               ))
          )
    ) THEN
        RAISE EXCEPTION 'legacy provider topology or soft-delete column exists';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_trigger trigger_record
        WHERE trigger_record.tgrelid = 'hypervisor.personal_vms'::regclass
          AND trigger_record.tgname = 'trg_hypervisor_vm_delete_requires_deleting'
          AND trigger_record.tgenabled <> 'D'
          AND NOT trigger_record.tgisinternal
    ) THEN
        RAISE EXCEPTION 'personal VM deletion status guard trigger is missing or disabled';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_trigger trigger_record
        WHERE trigger_record.tgrelid = 'hypervisor.image_artifacts'::regclass
          AND trigger_record.tgname = 'trg_hypervisor_image_delete_requires_deleting'
          AND trigger_record.tgenabled <> 'D'
          AND NOT trigger_record.tgisinternal
    ) THEN
        RAISE EXCEPTION 'hypervisor image deletion state guard trigger is missing or disabled';
    END IF;
END
$$;
