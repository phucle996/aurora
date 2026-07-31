DROP TRIGGER IF EXISTS trg_managed_service_outbox_delivery_epoch ON managed_service_outbox_records;
DROP FUNCTION IF EXISTS enforce_managed_service_outbox_delivery_epoch();

ALTER TABLE personal_managed_service_operations DROP COLUMN IF EXISTS delivery_epoch;
ALTER TABLE tenant_managed_service_operations DROP COLUMN IF EXISTS delivery_epoch;
ALTER TABLE managed_service_outbox_records DROP COLUMN IF EXISTS delivery_epoch;
