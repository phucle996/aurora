DROP TRIGGER IF EXISTS trg_hypervisor_outbox_updated_at
ON hypervisor_outbox_records;
DROP TRIGGER IF EXISTS trg_hypervisor_vm_delete_requires_deleting ON personal_vms;
DROP TRIGGER IF EXISTS trg_hypervisor_vm_updated_at ON personal_vms;
DROP TRIGGER IF EXISTS trg_hypervisor_image_updated_at ON image_artifacts;
