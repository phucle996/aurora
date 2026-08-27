DROP TRIGGER IF EXISTS trg_hypervisor_image_updated_at ON image_artifacts;
CREATE TRIGGER trg_hypervisor_image_updated_at
BEFORE UPDATE ON image_artifacts
FOR EACH ROW
EXECUTE FUNCTION hypervisor_touch_updated_at();

DROP TRIGGER IF EXISTS trg_hypervisor_vm_updated_at ON personal_vms;
CREATE TRIGGER trg_hypervisor_vm_updated_at
BEFORE UPDATE ON personal_vms
FOR EACH ROW
EXECUTE FUNCTION hypervisor_touch_updated_at();

DROP TRIGGER IF EXISTS trg_hypervisor_vm_delete_requires_deleting ON personal_vms;
CREATE TRIGGER trg_hypervisor_vm_delete_requires_deleting
BEFORE DELETE ON personal_vms
FOR EACH ROW
EXECUTE FUNCTION hypervisor_require_vm_deleting_before_delete();

DROP TRIGGER IF EXISTS trg_hypervisor_image_delete_requires_deleting ON image_artifacts;
CREATE TRIGGER trg_hypervisor_image_delete_requires_deleting
BEFORE DELETE ON image_artifacts
FOR EACH ROW
EXECUTE FUNCTION hypervisor_require_image_deleting_before_delete();

DROP TRIGGER IF EXISTS trg_hypervisor_outbox_updated_at
ON hypervisor_outbox_records;
CREATE TRIGGER trg_hypervisor_outbox_updated_at
BEFORE UPDATE ON hypervisor_outbox_records
FOR EACH ROW
EXECUTE FUNCTION hypervisor_touch_updated_at();
