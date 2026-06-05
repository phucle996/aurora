-- 000001_mail_enums.up.sql
-- Define any mail-specific enums here.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 
        FROM pg_type t 
        JOIN pg_namespace n ON t.typnamespace = n.oid 
        WHERE t.typname = 'mail_source_type' 
          AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE mail_source_type AS ENUM ('kafka', 'redis_stream', 'rabbitmq', 'nats');
    END IF;

    IF NOT EXISTS (
        SELECT 1 
        FROM pg_type t 
        JOIN pg_namespace n ON t.typnamespace = n.oid 
        WHERE t.typname = 'mail_consumer_status' 
          AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE mail_consumer_status AS ENUM ('enabled', 'paused', 'error', 'draining');
    END IF;

    IF NOT EXISTS (
        SELECT 1 
        FROM pg_type t 
        JOIN pg_namespace n ON t.typnamespace = n.oid 
        WHERE t.typname = 'mail_endpoint_status' 
          AND n.nspname = current_schema()
    ) THEN
        CREATE TYPE mail_endpoint_status AS ENUM ('planned', 'active', 'disabled');
    END IF;
END$$;
