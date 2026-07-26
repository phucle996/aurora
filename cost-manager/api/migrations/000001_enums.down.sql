-- Migration 000001 Down: Hủy các Enum types và schema billing
DROP TYPE IF EXISTS billing.ledger_entry_type;
DROP TYPE IF EXISTS billing.wallet_lifecycle_status;
DROP TYPE IF EXISTS billing.credential_kind;
DROP TYPE IF EXISTS billing.owner_type;
DROP TYPE IF EXISTS billing.service_type;
DROP SCHEMA IF EXISTS billing CASCADE;
