-- Delivery epoch separates a manual replay from an earlier outer attempt while
-- preserving the exact protected command bytes and the execution fence.
ALTER TABLE personal_managed_service_operations
    ADD COLUMN IF NOT EXISTS delivery_epoch BIGINT NOT NULL DEFAULT 0
        CHECK (delivery_epoch >= 0);
ALTER TABLE tenant_managed_service_operations
    ADD COLUMN IF NOT EXISTS delivery_epoch BIGINT NOT NULL DEFAULT 0
        CHECK (delivery_epoch >= 0);
ALTER TABLE managed_service_outbox_records
    ADD COLUMN IF NOT EXISTS delivery_epoch BIGINT NOT NULL DEFAULT 0
        CHECK (delivery_epoch >= 0);

CREATE OR REPLACE FUNCTION enforce_managed_service_outbox_delivery_epoch()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.delivery_epoch < OLD.delivery_epoch
       OR NEW.delivery_epoch > OLD.delivery_epoch + 1 THEN
        RAISE EXCEPTION 'managed service outbox delivery epoch must be monotonic';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_managed_service_outbox_delivery_epoch ON managed_service_outbox_records;
CREATE TRIGGER trg_managed_service_outbox_delivery_epoch
BEFORE UPDATE ON managed_service_outbox_records
FOR EACH ROW EXECUTE FUNCTION enforce_managed_service_outbox_delivery_epoch();
