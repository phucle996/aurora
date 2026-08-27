-- [COMMENT]: PostgreSQL chỉ khai báo business configuration enums. Runtime state là Redis
-- soft state theo watch lease nên không tạo database type hoặc table.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE n.nspname = current_schema() AND t.typname = 'mail_source_type'
    ) THEN
        CREATE TYPE mail_source_type AS ENUM ('kafka', 'redis_stream', 'nats_jetstream', 'rabbitmq');
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE n.nspname = current_schema() AND t.typname = 'mail_consumer_desired_state'
    ) THEN
        CREATE TYPE mail_consumer_desired_state AS ENUM ('paused', 'enabled', 'draining', 'drained', 'deleting');
    END IF;
END $$;
