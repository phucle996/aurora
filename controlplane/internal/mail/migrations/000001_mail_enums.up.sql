-- [COMMENT]: Phase 1 chỉ khai báo trạng thái bền vững; runtime state không được trộn với desired state.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE n.nspname = current_schema() AND t.typname = 'mail_source_type'
    ) THEN
        CREATE TYPE mail_source_type AS ENUM ('kafka', 'redis_stream', 'rabbitmq', 'nats');
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE n.nspname = current_schema() AND t.typname = 'mail_consumer_desired_state'
    ) THEN
        CREATE TYPE mail_consumer_desired_state AS ENUM ('paused', 'enabled', 'deleting', 'deleted');
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

    IF NOT EXISTS (
        SELECT 1 FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE n.nspname = current_schema() AND t.typname = 'mail_execution_status'
    ) THEN
        CREATE TYPE mail_execution_status AS ENUM (
            'CONSUMED', 'RENDERED', 'SUBMITTING', 'RETRY_SCHEDULED',
            'SUBMITTED', 'REJECTED', 'FAILED', 'AMBIGUOUS'
        );
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_type t
        JOIN pg_namespace n ON n.oid = t.typnamespace
        WHERE n.nspname = current_schema() AND t.typname = 'mail_result_inbox_status'
    ) THEN
        CREATE TYPE mail_result_inbox_status AS ENUM ('PENDING', 'APPLIED', 'REJECTED');
    END IF;

END $$;
