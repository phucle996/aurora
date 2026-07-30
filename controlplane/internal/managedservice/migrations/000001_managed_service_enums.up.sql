DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'managed_service_catalog_state') THEN
        CREATE TYPE managed_service_catalog_state AS ENUM ('active', 'retired');
    ELSIF NOT EXISTS (
        SELECT 1 FROM pg_type type
        JOIN pg_enum enum ON enum.enumtypid = type.oid
        WHERE type.typname = 'managed_service_catalog_state' AND enum.enumlabel = 'active'
    ) THEN
        -- [COMMENT]: PostgreSQL cannot use an enum value added in the same
        -- transaction. Replace the P01 development enum atomically instead of
        -- relying on ALTER TYPE ADD VALUE followed by unsafe same-xact writes.
        CREATE TYPE managed_service_catalog_state_next AS ENUM ('active', 'retired');
        ALTER TABLE IF EXISTS service_categories ALTER COLUMN state DROP DEFAULT;
        ALTER TABLE IF EXISTS service_definitions ALTER COLUMN state DROP DEFAULT;
        ALTER TABLE IF EXISTS service_blueprints ALTER COLUMN state DROP DEFAULT;
        ALTER TABLE IF EXISTS service_categories ALTER COLUMN state TYPE managed_service_catalog_state_next
            USING (CASE WHEN state::text = 'retired' THEN 'retired' ELSE 'active' END)::managed_service_catalog_state_next;
        ALTER TABLE IF EXISTS service_definitions ALTER COLUMN state TYPE managed_service_catalog_state_next
            USING (CASE WHEN state::text = 'retired' THEN 'retired' ELSE 'active' END)::managed_service_catalog_state_next;
        ALTER TABLE IF EXISTS service_blueprints ALTER COLUMN state TYPE managed_service_catalog_state_next
            USING (CASE WHEN state::text = 'retired' THEN 'retired' ELSE 'active' END)::managed_service_catalog_state_next;
        DROP TYPE managed_service_catalog_state;
        ALTER TYPE managed_service_catalog_state_next RENAME TO managed_service_catalog_state;
    END IF;
END $$;

DO $$
BEGIN
    CREATE TYPE managed_service_version_state AS ENUM ('available', 'deprecated', 'retired');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE TYPE managed_service_blueprint_revision_state AS ENUM ('draft', 'published', 'retired');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE TYPE managed_service_instance_state AS ENUM ('provisioning', 'active', 'deleting');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE TYPE managed_service_observed_state AS ENUM ('unknown', 'progressing', 'ready', 'degraded');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE TYPE managed_service_operation_kind AS ENUM ('create', 'update', 'delete');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE TYPE managed_service_operation_state AS ENUM (
        'accepted',
        'dispatching',
        'running',
        'retrying',
        'succeeded',
        'terminal_failed'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE TYPE managed_service_result_outcome AS ENUM ('succeeded', 'retryable_failure', 'terminal_failure');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
