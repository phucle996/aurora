-- 000001_mail_enums.up.sql
-- Define any mail-specific enums here.
CREATE TYPE mail_source_type AS ENUM ('kafka', 'redis_stream', 'rabbitmq', 'nats');
CREATE TYPE mail_consumer_status AS ENUM ('enabled', 'paused', 'error', 'draining');
