-- Remove the unused Aurora-as-OAuth-authorization-server schema.
--
-- These tables never had a repository, service, transport, or route consumer.
-- The guard is intentionally inside the migration transaction: an unexpected
-- row aborts startup before any destructive DDL can commit.
DO $$
DECLARE
    relation_name text;
    type_name text;
    has_rows boolean;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY[
        'oauth_tokens',
        'oauth_grants',
        'oauth_authorization_codes',
        'oauth_client_secrets',
        'oauth_clients'
    ]
    LOOP
        IF to_regclass(format('%I.%I', current_schema(), relation_name)) IS NULL THEN
            CONTINUE;
        END IF;

        EXECUTE format(
            'SELECT EXISTS (SELECT 1 FROM %I.%I LIMIT 1)',
            current_schema(),
            relation_name
        ) INTO has_rows;

        IF has_rows THEN
            RAISE EXCEPTION
                'refusing to drop non-empty dead-code table %.%',
                current_schema(),
                relation_name
                USING HINT = 'Audit and explicitly archive or remove the unexpected rows before retrying the IAM migration.';
        END IF;
    END LOOP;

    -- Drop dependants before parents; indexes and the updated_at trigger
    -- disappear with their owning tables. Schema-qualified dynamic DDL must
    -- not fall through to an equally named relation in the public schema.
    FOREACH relation_name IN ARRAY ARRAY[
        'oauth_tokens',
        'oauth_grants',
        'oauth_authorization_codes',
        'oauth_client_secrets',
        'oauth_clients'
    ]
    LOOP
        EXECUTE format(
            'DROP TABLE IF EXISTS %I.%I',
            current_schema(),
            relation_name
        );
    END LOOP;

    FOREACH type_name IN ARRAY ARRAY[
        'oauth_client_status',
        'oauth_client_type'
    ]
    LOOP
        IF EXISTS (
            SELECT 1
            FROM pg_type AS t
            JOIN pg_namespace AS n ON n.oid = t.typnamespace
            WHERE n.nspname = current_schema()
              AND t.typname = type_name
        ) THEN
            EXECUTE format(
                'DROP TYPE %I.%I',
                current_schema(),
                type_name
            );
        END IF;
    END LOOP;
END
$$;
