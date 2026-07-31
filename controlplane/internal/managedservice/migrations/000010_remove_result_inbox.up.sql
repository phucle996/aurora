-- Result settlement is an atomic update of the source outbox and the owned
-- instance/operation rows. A second result-inbox table would create another
-- durable source of truth and can lose the notification retry boundary.
--
-- Existing installations may have received the old empty tables. Refuse to
-- destroy data if anything was written before this contract migration; the
-- operator must reconcile it explicitly instead of silently dropping evidence.
DO $$
BEGIN
    IF to_regclass('personal_managed_service_result_inbox') IS NOT NULL
       AND EXISTS (SELECT 1 FROM personal_managed_service_result_inbox) THEN
        RAISE EXCEPTION 'personal managed-service result inbox is not empty; reconcile before removal';
    END IF;
    IF to_regclass('tenant_managed_service_result_inbox') IS NOT NULL
       AND EXISTS (SELECT 1 FROM tenant_managed_service_result_inbox) THEN
        RAISE EXCEPTION 'tenant managed-service result inbox is not empty; reconcile before removal';
    END IF;
END
$$;

DROP INDEX IF EXISTS ix_personal_managed_service_result_inbox_retention;
DROP INDEX IF EXISTS ix_tenant_managed_service_result_inbox_retention;
DROP TABLE IF EXISTS personal_managed_service_result_inbox;
DROP TABLE IF EXISTS tenant_managed_service_result_inbox;
-- The database result enum was only used by the removed inbox tables. The
-- durable result outcome remains a protobuf contract and is not a PostgreSQL
-- business type, so stale installations must not retain this dead schema.
DROP TYPE IF EXISTS managed_service_result_outcome;
