-- Non-table enforcement and lookup paths for the final baseline.

CREATE INDEX idx_pricing_schedule_version_lookup
    ON billing.pricing_schedule_versions(pricing_schedule_id, effective_from DESC);
CREATE UNIQUE INDEX uq_scalar_bracket_one_infinity
    ON billing.pricing_schedule_scalar_brackets(pricing_schedule_version_id)
    WHERE range_end_quantity IS NULL;
CREATE INDEX idx_scalar_bracket_lookup
    ON billing.pricing_schedule_scalar_brackets(pricing_schedule_version_id, range_start_quantity);
CREATE INDEX idx_pricing_outbox_unpublished
    ON billing.pricing_outbox(occurred_at, id)
    WHERE published_at IS NULL;
CREATE INDEX idx_usage_settlement_retry
    ON billing.usage_settlement_runs(status, updated_at, source_module, charge_kind_code);
CREATE INDEX idx_storage_zone_adjustment_lookup
    ON billing.storage_zone_price_adjustment_versions(zone_id, effective_from DESC, version_number DESC)
    WHERE status <> 'CANCELLED';
CREATE INDEX idx_hypervisor_zone_adjustment_lookup
    ON billing.hypervisor_zone_price_adjustment_versions(zone_id, effective_from DESC, version_number DESC)
    WHERE status <> 'CANCELLED';
CREATE INDEX idx_mail_zone_adjustment_lookup
    ON billing.mail_zone_price_adjustment_versions(zone_id, effective_from DESC, version_number DESC)
    WHERE status <> 'CANCELLED';

CREATE UNIQUE INDEX uq_resource_ownership_active_resource
    ON billing.resource_ownership_projection(resource_type, resource_id)
    WHERE effective_to IS NULL;
CREATE UNIQUE INDEX uq_resource_ownership_active_name
    ON billing.resource_ownership_projection(resource_type, resource_name)
    WHERE effective_to IS NULL;
CREATE INDEX idx_resource_ownership_lookup
    ON billing.resource_ownership_projection(resource_name, effective_from DESC, effective_to);
CREATE UNIQUE INDEX uq_ownership_inbox_resource_version
    ON billing.ownership_event_inbox(resource_id, source_version);
CREATE UNIQUE INDEX uq_credential_bindings_active_access_key
    ON billing.credential_bindings(access_key)
    WHERE valid_to IS NULL AND status = 'ACTIVE';
CREATE INDEX idx_credential_bindings_resource
    ON billing.credential_bindings(resource_type, resource_id, valid_from DESC);

CREATE INDEX idx_wallet_admission_outbox_claim
    ON billing.wallet_admission_outbox(published_at, claimed_at, occurred_at, event_id);
CREATE INDEX idx_storage_pending_activation_queue
    ON billing.storage_pending_activation_reconcile(status, updated_at, wallet_id)
    WHERE status IN ('PENDING', 'PROCESSING', 'BLOCKED');
CREATE INDEX idx_wallet_ledger_wallet_time
    ON billing.wallet_ledger_entries(wallet_id, occurred_at DESC, id);
CREATE INDEX idx_wallet_ledger_settlement_run
    ON billing.wallet_ledger_entries(usage_settlement_run_id)
    WHERE usage_settlement_run_id IS NOT NULL;
CREATE INDEX idx_unrated_usage_pending
    ON billing.unrated_usage(metering_hour, id)
    WHERE status = 'PENDING';

CREATE INDEX idx_referral_campaign_catalog
    ON billing.promotion_campaigns(campaign_type, status, starts_at, ends_at);
CREATE INDEX idx_personal_referral_reservations_campaign_capacity
    ON billing.personal_referral_reservations(campaign_id, status, expires_at);
CREATE UNIQUE INDEX uq_personal_referral_reservation_live_user
    ON billing.personal_referral_reservations(user_id, redemption_kind)
    WHERE status = 'RESERVED';
CREATE INDEX idx_payment_intents_owner_created
    ON billing.payment_intents(owner_id, owner_type, created_at DESC);
CREATE INDEX idx_payment_intents_actor_created
    ON billing.payment_intents(actor_user_id, created_at DESC);
CREATE INDEX idx_payment_intents_pending_expiry
    ON billing.payment_intents(expires_at)
    WHERE status = 'PENDING';

CREATE INDEX idx_storage_report_inbox_pending
    ON billing.storage_usage_report_inbox(status, received_at, report_id)
    WHERE status IN ('RECEIVED', 'PROCESSING', 'UNRATED');
CREATE INDEX idx_storage_usage_line_resource_window
    ON billing.storage_usage_line_inbox(zone_id, resource_id, created_at);
CREATE INDEX idx_storage_usage_line_resource_name
    ON billing.storage_usage_line_inbox(zone_id, resource_name, created_at)
    WHERE resource_name IS NOT NULL;
CREATE INDEX idx_hypervisor_allocation_interval_window
    ON billing.hypervisor_allocation_intervals(effective_from, effective_to, resource_id);
CREATE INDEX idx_hypervisor_allocation_window_claim
    ON billing.hypervisor_allocation_windows(status, window_start, shard_id)
    WHERE status IN ('PENDING', 'PROCESSING', 'UNRATED');
CREATE INDEX idx_hypervisor_allocation_line_pending
    ON billing.hypervisor_allocation_lines(status, window_id, resource_id)
    WHERE status IN ('PENDING', 'UNRATED');
CREATE INDEX idx_hypervisor_network_report_pending
    ON billing.hypervisor_network_usage_report_inbox(status, received_at, report_id)
    WHERE status IN ('PROCESSING', 'UNRATED');
CREATE INDEX idx_hypervisor_network_line_pending
    ON billing.hypervisor_network_usage_lines(status, report_id, resource_id)
    WHERE status IN ('PENDING', 'UNRATED');
CREATE INDEX idx_mail_accepted_usage_pending
    ON billing.mail_accepted_usage_inbox(status, accepted_at, evidence_id)
    WHERE status IN ('PROCESSING', 'UNRATED');

CREATE OR REPLACE FUNCTION billing.enforce_pricing_schedule_registry()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    registry_model billing.pricing_model;
BEGIN
    SELECT pricing_model INTO registry_model
    FROM billing.charge_kind_catalog
    WHERE code = NEW.charge_kind_code AND status = 'ENABLED';
    IF registry_model IS NULL OR registry_model <> NEW.pricing_model THEN
        RAISE EXCEPTION 'pricing schedule model does not match enabled charge kind %', NEW.charge_kind_code;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER pricing_schedule_registry_guard
BEFORE INSERT OR UPDATE OF charge_kind_code, pricing_model
ON billing.pricing_schedules
FOR EACH ROW EXECUTE FUNCTION billing.enforce_pricing_schedule_registry();

CREATE OR REPLACE FUNCTION billing.enforce_scalar_bracket_coverage()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    target_version UUID;
    pricing_model  billing.pricing_model;
    expected_start BIGINT := 0;
    saw_infinity   BOOLEAN := FALSE;
    bracket        RECORD;
BEGIN
    target_version := CASE WHEN TG_OP = 'DELETE' THEN OLD.pricing_schedule_version_id ELSE NEW.pricing_schedule_version_id END;
    SELECT v.pricing_model INTO pricing_model
    FROM billing.pricing_schedule_versions v WHERE v.id = target_version;
    IF pricing_model IS NULL THEN
        RAISE EXCEPTION 'pricing schedule version % does not exist', target_version USING ERRCODE = 'foreign_key_violation';
    END IF;
    IF pricing_model <> 'PROGRESSIVE_UNIT' THEN
        RAISE EXCEPTION 'fixed bundle version % cannot have scalar brackets', target_version USING ERRCODE = 'check_violation';
    END IF;
    FOR bracket IN
        SELECT range_start_quantity, range_end_quantity
        FROM billing.pricing_schedule_scalar_brackets
        WHERE pricing_schedule_version_id = target_version
        ORDER BY range_start_quantity
    LOOP
        IF bracket.range_start_quantity <> expected_start THEN
            RAISE EXCEPTION 'pricing version % has a gap or overlap at %', target_version, expected_start USING ERRCODE = 'check_violation';
        END IF;
        IF bracket.range_end_quantity IS NULL THEN
            IF saw_infinity THEN
                RAISE EXCEPTION 'pricing version % has more than one unbounded bracket', target_version USING ERRCODE = 'check_violation';
            END IF;
            saw_infinity := TRUE;
        ELSE
            expected_start := bracket.range_end_quantity;
        END IF;
    END LOOP;
    IF NOT saw_infinity THEN
        RAISE EXCEPTION 'pricing version % must end with an unbounded bracket', target_version USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER pricing_schedule_scalar_bracket_coverage
AFTER INSERT OR UPDATE OR DELETE ON billing.pricing_schedule_scalar_brackets
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION billing.enforce_scalar_bracket_coverage();
