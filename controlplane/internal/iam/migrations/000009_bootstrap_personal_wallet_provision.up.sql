-- Provision Cost-owned personal wallets for the five canonical IAM bootstrap
-- identities through the same durable lifecycle boundary used by account
-- verification. This migration never writes Billing state directly.

DO $$
DECLARE
    active_bootstrap_count INTEGER;
BEGIN
    SELECT COUNT(*)::integer
    INTO active_bootstrap_count
    FROM users
    WHERE status = 'active'
      AND id IN (
          '00000000-0000-0000-0000-000000000001'::uuid,
          '00000000-0000-0000-0000-000000000002'::uuid,
          '00000000-0000-0000-0000-000000000003'::uuid,
          '00000000-0000-0000-0000-000000000004'::uuid,
          '00000000-0000-0000-0000-000000000005'::uuid
      );
    IF active_bootstrap_count <> 5 THEN
        RAISE EXCEPTION 'IAM bootstrap wallet provision requires five active canonical identities, found %',
            active_bootstrap_count;
    END IF;
END;
$$;

-- [COMMENT]: The wire payload is migration-local because PostgreSQL owns the
-- durable outbox insert while Cost owns Protobuf validation and wallet apply.
-- Every encoded field has a fixed one-byte length in this bounded command:
-- event_id(16), schema_version(1), owner_id(16), PERSONAL(8), USD(3), RFC3339(20).
CREATE FUNCTION iam_bootstrap_personal_wallet_payload(
    input_event_id UUID,
    input_owner_id UUID,
    input_occurred_at TEXT
)
RETURNS BYTEA
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
    SELECT decode('0a10', 'hex') || uuid_send(input_event_id)
        || decode('1001', 'hex')
        || decode('1a10', 'hex') || uuid_send(input_owner_id)
        || decode('2208', 'hex') || convert_to('PERSONAL', 'UTF8')
        || decode('2a03', 'hex') || convert_to('USD', 'UTF8')
        || decode('3214', 'hex') || convert_to(input_occurred_at, 'UTF8');
$$;

WITH bootstrap_wallet(owner_id, event_id) AS (
    VALUES
        ('00000000-0000-0000-0000-000000000001'::uuid, '59002562-bee4-5f1a-aa66-6a0a9e0efcfa'::uuid),
        ('00000000-0000-0000-0000-000000000002'::uuid, 'cb70c09c-dfee-5a92-b224-85eaf583117e'::uuid),
        ('00000000-0000-0000-0000-000000000003'::uuid, '5d112cc7-128d-5598-a6c6-376e9c73642f'::uuid),
        ('00000000-0000-0000-0000-000000000004'::uuid, '66191a81-8a5a-590d-a961-5fa0716c7fbc'::uuid),
        ('00000000-0000-0000-0000-000000000005'::uuid, '81b33b0c-65a1-538f-9b36-62eeb548b471'::uuid)
), eligible_command AS MATERIALIZED (
    SELECT bootstrap.owner_id,
           bootstrap.event_id,
           '2026-08-28T00:00:00Z'::text AS occurred_at
    FROM bootstrap_wallet AS bootstrap
    JOIN users AS user_account ON user_account.id = bootstrap.owner_id
    WHERE user_account.status = 'active'
)
INSERT INTO lifecycle_fact_outbox_records (
    event_id,
    event_type,
    schema_version,
    aggregate_type,
    aggregate_id,
    aggregate_version,
    owner_id,
    owner_type,
    actor_user_id,
    payload,
    occurred_at
)
SELECT command.event_id,
       'billing.personal_wallet.provision.requested.v1',
       1,
       'IAM_USER',
       command.owner_id,
       1,
       command.owner_id,
       'PERSONAL',
       command.owner_id,
       iam_bootstrap_personal_wallet_payload(
           command.event_id,
           command.owner_id,
           command.occurred_at
       ),
       command.occurred_at::timestamptz
FROM eligible_command AS command
ON CONFLICT (event_id) DO NOTHING;

DROP FUNCTION iam_bootstrap_personal_wallet_payload(UUID, UUID, TEXT);
