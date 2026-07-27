-- VM commands are routed to exactly one Zone UUID.
-- Existing installations may still have the legacy zone:<uuid> text column.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'hypervisor'
          AND table_name = 'vm_outbox_records'
          AND column_name = 'routing_scope'
    ) AND NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'hypervisor'
          AND table_name = 'vm_outbox_records'
          AND column_name = 'zone_id'
    ) THEN
        ALTER TABLE hypervisor.vm_outbox_records
            RENAME COLUMN routing_scope TO zone_id;
        ALTER TABLE hypervisor.vm_outbox_records
            ALTER COLUMN zone_id TYPE UUID USING (
                CASE
                    WHEN zone_id LIKE 'zone:%'
                        THEN substring(zone_id FROM 6)::uuid
                    ELSE zone_id::uuid
                END
            );
    ELSIF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'hypervisor'
          AND table_name = 'vm_outbox_records'
          AND column_name = 'routing_scope'
    ) THEN
        RAISE EXCEPTION 'vm_outbox_records has both routing_scope and zone_id';
    END IF;
END
$$;

-- Migration files are replayed on startup, so the constraint must be added
-- without repeatedly taking the table lock required by DROP/ADD.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'hypervisor.vm_outbox_records'::regclass
          AND conname = 'ck_hypervisor_vm_outbox_zone_id'
    ) THEN
        ALTER TABLE hypervisor.vm_outbox_records
            ADD CONSTRAINT ck_hypervisor_vm_outbox_zone_id
            CHECK (zone_id <> '00000000-0000-0000-0000-000000000000'::uuid);
    END IF;
END
$$;
