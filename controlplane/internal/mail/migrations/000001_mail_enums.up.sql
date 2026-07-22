-- [COMMENT]: Phase 1 chỉ khai báo trạng thái bền vững; runtime state không được trộn với desired state.
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
        -- [COMMENT]: Creating/deleting là operation UI/outbox, không phải business desired state.
        CREATE TYPE mail_consumer_desired_state AS ENUM ('paused', 'enabled');
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE n.nspname = current_schema() AND t.typname = 'mail_consumer_runtime_state'
    ) THEN
        CREATE TYPE mail_consumer_runtime_state AS ENUM (
            'stopped', 'starting', 'running', 'paused', 'draining', 'error', 'degraded'
        );
    END IF;

END $$;
