CREATE INDEX IF NOT EXISTS idx_hypervisor_image_catalog
    ON image_artifacts (zone_id, code, revision DESC)
    WHERE state = 'AVAILABLE';

CREATE INDEX IF NOT EXISTS idx_hypervisor_image_state
    ON image_artifacts (state, updated_at ASC);

CREATE INDEX IF NOT EXISTS idx_hypervisor_personal_vms_scope
    ON personal_vms (workspace_id, zone_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_hypervisor_personal_vms_image
    ON personal_vms (image_id);

CREATE INDEX IF NOT EXISTS idx_hypervisor_outbox_claim
    ON hypervisor_outbox_records (created_at, id)
    WHERE status IN ('PENDING', 'PROCESSING');

CREATE INDEX IF NOT EXISTS idx_hypervisor_outbox_resource
    ON hypervisor_outbox_records (resource_id, job_version DESC);

CREATE INDEX IF NOT EXISTS idx_hypervisor_outbox_terminal_cleanup
    ON hypervisor_outbox_records (completed_at, id)
    WHERE status IN ('SUCCEEDED', 'FAILED', 'DEAD')
      AND completed_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_hypervisor_allocation_export_pending
    ON hypervisor_allocation_outbox (effective_at, id)
    WHERE published_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_hypervisor_allocation_export_resource
    ON hypervisor_allocation_outbox (resource_id, source_version DESC);
