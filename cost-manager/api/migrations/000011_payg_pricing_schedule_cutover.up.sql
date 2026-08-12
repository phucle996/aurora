-- PAYG pricing-schedule cutover.
-- This migration is intentionally forward-only for a non-production baseline.
-- It refuses to destroy financial evidence if an environment already settled a
-- billing run or wrote a ledger entry with the legacy catalog.
DO $$
BEGIN
    IF to_regclass('billing.billing_runs') IS NOT NULL
       AND EXISTS (SELECT 1 FROM billing.billing_runs) THEN
        RAISE EXCEPTION
            'PAYG pricing cutover requires an empty legacy billing_runs table';
    END IF;

    IF to_regclass('billing.wallet_ledger_entries') IS NOT NULL
       AND EXISTS (SELECT 1 FROM billing.wallet_ledger_entries) THEN
        RAISE EXCEPTION
            'PAYG pricing cutover refuses to drop legacy pricing lineage after ledger activity';
    END IF;
END $$;

-- Wallet admission is a financial transition projection, not a request-time
-- wallet lookup. Existing rows are normalized before the invariant is added.
ALTER TABLE billing.wallets
    ADD COLUMN IF NOT EXISTS restriction_reason VARCHAR(32),
    ADD COLUMN IF NOT EXISTS status_changed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ;

UPDATE billing.wallets
SET status_changed_at = COALESCE(status_changed_at, updated_at, created_at),
    restriction_reason = CASE status::text
        WHEN 'PENDING_ACTIVATION' THEN 'NOT_ACTIVATED'
        WHEN 'SUSPENDED' THEN COALESCE(restriction_reason, 'ADMINISTRATIVE')
        WHEN 'CLOSED' THEN 'CLOSED'
        ELSE NULL
    END,
    closed_at = CASE
        WHEN status::text = 'CLOSED' THEN COALESCE(closed_at, updated_at, created_at)
        ELSE NULL
    END;

ALTER TABLE billing.wallets
    ALTER COLUMN status_changed_at SET NOT NULL;

ALTER TABLE billing.wallets
    DROP CONSTRAINT IF EXISTS ck_wallet_admission_state,
    ADD CONSTRAINT ck_wallet_admission_state CHECK (
        (status::text = 'PENDING_ACTIVATION' AND restriction_reason = 'NOT_ACTIVATED' AND closed_at IS NULL)
        OR (status::text = 'ACTIVE' AND restriction_reason IS NULL AND closed_at IS NULL)
        OR (status::text = 'SUSPENDED' AND restriction_reason IN ('CREDIT_EXHAUSTED', 'ADMINISTRATIVE', 'COMPLIANCE') AND closed_at IS NULL)
        OR (status::text = 'CLOSED' AND restriction_reason = 'CLOSED' AND closed_at IS NOT NULL)
    );

CREATE TABLE IF NOT EXISTS billing.wallet_admission_outbox (
    event_id             UUID PRIMARY KEY,
    wallet_id            UUID NOT NULL REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id             UUID NOT NULL,
    owner_type           billing.owner_type NOT NULL,
    wallet_version       BIGINT NOT NULL,
    admission_mode       VARCHAR(24) NOT NULL,
    restriction_reason   VARCHAR(32),
    effective_at         TIMESTAMPTZ NOT NULL,
    valid_until          TIMESTAMPTZ,
    occurred_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at         TIMESTAMPTZ,
    claim_token          UUID,
    claimed_at           TIMESTAMPTZ,
    retry_count          INT NOT NULL DEFAULT 0,
    last_error           TEXT,
    CONSTRAINT ck_wallet_admission_mode CHECK (admission_mode IN ('ALLOW', 'SUSPEND_BILLABLE')),
    CONSTRAINT ck_wallet_admission_reason CHECK ((admission_mode = 'ALLOW' AND restriction_reason IS NULL) OR (admission_mode = 'SUSPEND_BILLABLE' AND restriction_reason IN ('NOT_ACTIVATED', 'CREDIT_EXHAUSTED', 'ADMINISTRATIVE', 'COMPLIANCE', 'CLOSED'))),
    CONSTRAINT ck_wallet_admission_retry CHECK (retry_count >= 0),
    CONSTRAINT ck_wallet_admission_window CHECK (valid_until IS NULL OR valid_until > effective_at)
);
CREATE INDEX IF NOT EXISTS idx_wallet_admission_outbox_claim
    ON billing.wallet_admission_outbox(published_at, claimed_at, occurred_at, event_id);

-- Greenfield deployments normally have no wallet rows yet. If a clean dev
-- database already contains wallets but no transition outbox, backfill one
-- deterministic snapshot per wallet so Storage never remains stuck on a
-- missing admission projection after the cutover.
INSERT INTO billing.wallet_admission_outbox
    (event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode,
     restriction_reason, effective_at)
SELECT md5('wallet-admission-cutover:' || w.id::text)::uuid,
       w.id, w.owner_id, w.owner_type, w.version,
       CASE WHEN w.status::text = 'ACTIVE' THEN 'ALLOW' ELSE 'SUSPEND_BILLABLE' END,
       CASE WHEN w.status::text = 'ACTIVE' THEN NULL ELSE w.restriction_reason END,
       w.status_changed_at
FROM billing.wallets w
WHERE NOT EXISTS (
    SELECT 1 FROM billing.wallet_admission_outbox existing
    WHERE existing.wallet_id = w.id
);

CREATE TABLE IF NOT EXISTS billing.storage_pending_activation_reconcile (
    wallet_id            UUID PRIMARY KEY REFERENCES billing.wallets(id) ON DELETE RESTRICT,
    owner_id             UUID NOT NULL,
    owner_type           billing.owner_type NOT NULL,
    target_wallet_version BIGINT NOT NULL,
    status               VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    checkpoint_window_end TIMESTAMPTZ,
    last_error           TEXT,
    retry_count          INT NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_storage_pending_activation_status CHECK (status IN ('PENDING', 'PROCESSING', 'BLOCKED', 'COMPLETED')),
    CONSTRAINT ck_storage_pending_activation_retry CHECK (retry_count >= 0)
);
CREATE INDEX idx_storage_pending_activation_queue
    ON billing.storage_pending_activation_reconcile(status, updated_at, wallet_id)
    WHERE status IN ('PENDING', 'PROCESSING', 'BLOCKED');

-- Legacy commercial catalog and its dependent entitlements are not part of
-- PAYG. Drop references first so the clean replacement cannot retain hidden
-- plan/subscription authority.
ALTER TABLE billing.wallet_ledger_entries
    DROP CONSTRAINT IF EXISTS wallet_ledger_entries_billing_run_id_fkey,
    DROP CONSTRAINT IF EXISTS wallet_ledger_entries_tier_version_id_fkey;

DROP INDEX IF EXISTS billing.idx_wallet_ledger_billing_run;

DROP TABLE IF EXISTS billing.resource_plan_assignments CASCADE;
DROP TABLE IF EXISTS billing.subscriptions CASCADE;
DROP TABLE IF EXISTS billing.pack_plans CASCADE;
DROP TABLE IF EXISTS billing.plans CASCADE;
DROP TABLE IF EXISTS billing.packs CASCADE;
DROP TABLE IF EXISTS billing.billing_runs CASCADE;
DROP TABLE IF EXISTS billing.pricing_outbox CASCADE;
DROP TABLE IF EXISTS billing.tier_version_ranges CASCADE;
DROP TABLE IF EXISTS billing.tier_versions CASCADE;
DROP TABLE IF EXISTS billing.tiers CASCADE;

DELETE FROM billing.promotion_campaigns
WHERE code = 'FREE_TIER_100_USD';

-- Controlled registry: a schedule can reference only a reviewed charge kind.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typnamespace = 'billing'::regnamespace AND typname = 'pricing_model') THEN
        CREATE TYPE billing.pricing_model AS ENUM ('PROGRESSIVE_UNIT', 'FIXED_BUNDLE');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typnamespace = 'billing'::regnamespace AND typname = 'pricing_scope') THEN
        CREATE TYPE billing.pricing_scope AS ENUM ('GLOBAL', 'ZONE');
    END IF;
END $$;

CREATE TABLE billing.charge_kind_catalog (
    code                  TEXT PRIMARY KEY,
    module_code           TEXT NOT NULL,
    pricing_model         billing.pricing_model NOT NULL,
    raw_input_unit        TEXT NOT NULL,
    observation_semantics TEXT NOT NULL,
    metering_contract     TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'ENABLED',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_charge_kind_status CHECK (status IN ('ENABLED', 'DISABLED')),
    CONSTRAINT uq_charge_kind_module_code UNIQUE (module_code, code)
);

INSERT INTO billing.charge_kind_catalog
    (code, module_code, pricing_model, raw_input_unit, observation_semantics, metering_contract)
VALUES
    ('storage.network_in.byte', 'storage', 'PROGRESSIVE_UNIT', 'BYTE', 'CLOSED_DELTA', 'StorageUsageReportV1'),
    ('storage.network_out.byte', 'storage', 'PROGRESSIVE_UNIT', 'BYTE', 'CLOSED_DELTA', 'StorageUsageReportV1'),
    ('storage.capacity.gb_hour', 'storage', 'PROGRESSIVE_UNIT', 'GB_HOUR_MICRO', 'CLOSED_INTEGRAL', 'StorageUsageReportV1'),
    ('hypervisor.vm_shape.duration', 'hypervisor', 'FIXED_BUNDLE', 'SECOND', 'BUNDLE_DURATION', 'HypervisorUsageReportV1')
ON CONFLICT (code) DO NOTHING;

UPDATE billing.charge_kind_catalog
SET status = 'DISABLED'
WHERE code = 'hypervisor.vm_shape.duration';

CREATE TABLE billing.pricing_schedules (
    id                UUID PRIMARY KEY,
    code              TEXT NOT NULL,
    display_name      TEXT NOT NULL,
    charge_kind_code  TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    pricing_model     billing.pricing_model NOT NULL,
    scope_type        billing.pricing_scope NOT NULL,
    zone_id           UUID,
    currency          CHAR(3) NOT NULL DEFAULT 'USD',
    metadata_version  INT NOT NULL DEFAULT 1,
    status            TEXT NOT NULL DEFAULT 'ACTIVE',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_pricing_schedule_code UNIQUE (code),
    CONSTRAINT uq_pricing_schedule_id_model UNIQUE (id, pricing_model),
    CONSTRAINT uq_pricing_schedule_kind_scope UNIQUE (id, charge_kind_code, pricing_model),
    CONSTRAINT ck_pricing_schedule_scope CHECK ((scope_type = 'GLOBAL' AND zone_id IS NULL) OR (scope_type = 'ZONE' AND zone_id IS NOT NULL)),
    CONSTRAINT ck_pricing_schedule_currency CHECK (currency = UPPER(currency)),
    CONSTRAINT ck_pricing_schedule_status CHECK (status IN ('ACTIVE', 'DISABLED'))
);

CREATE UNIQUE INDEX uq_pricing_schedule_global_kind
    ON billing.pricing_schedules(charge_kind_code)
    WHERE scope_type = 'GLOBAL';
CREATE UNIQUE INDEX uq_pricing_schedule_zone_kind
    ON billing.pricing_schedules(charge_kind_code, zone_id)
    WHERE scope_type = 'ZONE';

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

CREATE TABLE billing.pricing_schedule_versions (
    id                    UUID PRIMARY KEY,
    pricing_schedule_id   UUID NOT NULL REFERENCES billing.pricing_schedules(id) ON DELETE RESTRICT,
    pricing_model         billing.pricing_model NOT NULL,
    version_number        INT NOT NULL,
    status                TEXT NOT NULL,
    effective_from        TIMESTAMPTZ NOT NULL,
    effective_to          TIMESTAMPTZ,
    definition_schema     TEXT,
    definition_json       JSONB,
    checksum              CHAR(64) NOT NULL,
    change_reason         TEXT NOT NULL,
    created_by            UUID,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_pricing_schedule_version_number UNIQUE (pricing_schedule_id, version_number),
    CONSTRAINT uq_pricing_schedule_version_model UNIQUE (id, pricing_schedule_id, pricing_model),
    CONSTRAINT ck_pricing_schedule_version_positive CHECK (version_number > 0),
    CONSTRAINT ck_pricing_schedule_version_status CHECK (status IN ('SCHEDULED', 'ACTIVE', 'SUPERSEDED', 'CANCELLED')),
    CONSTRAINT ck_pricing_schedule_version_window CHECK (effective_to IS NULL OR effective_to > effective_from),
    CONSTRAINT ck_pricing_schedule_definition_shape CHECK (
        (pricing_model = 'PROGRESSIVE_UNIT' AND definition_schema IS NULL AND definition_json IS NULL)
        OR (pricing_model = 'FIXED_BUNDLE' AND definition_schema IS NOT NULL AND definition_json IS NOT NULL)
    ),
    CONSTRAINT fk_pricing_schedule_version_model
        FOREIGN KEY (pricing_schedule_id, pricing_model)
        REFERENCES billing.pricing_schedules(id, pricing_model)
        ON DELETE RESTRICT
);

CREATE INDEX idx_pricing_schedule_version_lookup
    ON billing.pricing_schedule_versions(pricing_schedule_id, effective_from DESC);

CREATE EXTENSION IF NOT EXISTS btree_gist;
ALTER TABLE billing.pricing_schedule_versions
    ADD CONSTRAINT ex_pricing_schedule_version_effective_window
    EXCLUDE USING gist (
        pricing_schedule_id WITH =,
        tstzrange(effective_from, COALESCE(effective_to, 'infinity'::timestamptz), '[)') WITH &&
    );

CREATE TABLE billing.pricing_schedule_scalar_brackets (
    id                              UUID PRIMARY KEY,
    pricing_schedule_version_id    UUID NOT NULL REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    range_start_quantity            BIGINT NOT NULL,
    range_end_quantity              BIGINT,
    price_numerator_micro_units     BIGINT NOT NULL,
    price_denominator_quantity      BIGINT NOT NULL,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_scalar_range_start_non_negative CHECK (range_start_quantity >= 0),
    CONSTRAINT ck_scalar_range_end_after_start CHECK (range_end_quantity IS NULL OR range_end_quantity > range_start_quantity),
    CONSTRAINT ck_scalar_price_numerator_non_negative CHECK (price_numerator_micro_units >= 0),
    CONSTRAINT ck_scalar_price_denominator_positive CHECK (price_denominator_quantity > 0),
    CONSTRAINT uq_scalar_bracket_start UNIQUE (pricing_schedule_version_id, range_start_quantity)
);

CREATE UNIQUE INDEX uq_scalar_bracket_one_infinity
    ON billing.pricing_schedule_scalar_brackets(pricing_schedule_version_id)
    WHERE range_end_quantity IS NULL;
CREATE INDEX idx_scalar_bracket_lookup
    ON billing.pricing_schedule_scalar_brackets(pricing_schedule_version_id, range_start_quantity);

-- API validation is not the database authority. A direct operator write,
-- replayed migration, or future admin path must still leave every scalar
-- version as one contiguous [0, infinity) partition. The deferred trigger
-- lets one transaction insert bracket rows in any order, then validates the
-- complete version at commit time.
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

    SELECT v.pricing_model
      INTO pricing_model
      FROM billing.pricing_schedule_versions v
     WHERE v.id = target_version;

    IF pricing_model IS NULL THEN
        RAISE EXCEPTION 'pricing schedule version % does not exist', target_version
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    IF pricing_model <> 'PROGRESSIVE_UNIT' THEN
        RAISE EXCEPTION 'fixed bundle version % cannot have scalar brackets', target_version
            USING ERRCODE = 'check_violation';
    END IF;

    FOR bracket IN
        SELECT range_start_quantity, range_end_quantity
          FROM billing.pricing_schedule_scalar_brackets
         WHERE pricing_schedule_version_id = target_version
         ORDER BY range_start_quantity
    LOOP
        IF bracket.range_start_quantity <> expected_start THEN
            RAISE EXCEPTION 'pricing version % has a gap or overlap at %', target_version, expected_start
                USING ERRCODE = 'check_violation';
        END IF;
        IF bracket.range_end_quantity IS NULL THEN
            IF saw_infinity THEN
                RAISE EXCEPTION 'pricing version % has more than one unbounded bracket', target_version
                    USING ERRCODE = 'check_violation';
            END IF;
            saw_infinity := TRUE;
        ELSE
            expected_start := bracket.range_end_quantity;
        END IF;
    END LOOP;

    IF NOT saw_infinity THEN
        RAISE EXCEPTION 'pricing version % must end with an unbounded bracket', target_version
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER pricing_schedule_scalar_bracket_coverage
AFTER INSERT OR UPDATE OR DELETE ON billing.pricing_schedule_scalar_brackets
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION billing.enforce_scalar_bracket_coverage();

CREATE TABLE billing.pricing_outbox (
    id                    UUID PRIMARY KEY,
    event_type            VARCHAR(64) NOT NULL,
    pricing_schedule_id   UUID NOT NULL REFERENCES billing.pricing_schedules(id) ON DELETE RESTRICT,
    version_id            UUID NOT NULL REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    module_code           TEXT NOT NULL,
    charge_kind_code      TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    scope_type            billing.pricing_scope NOT NULL,
    zone_id               UUID,
    effective_from        TIMESTAMPTZ NOT NULL,
    checksum              CHAR(64) NOT NULL,
    occurred_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at          TIMESTAMPTZ,
    retry_count           INT NOT NULL DEFAULT 0,
    last_error            TEXT,
    CONSTRAINT ck_schedule_outbox_retry CHECK (retry_count >= 0),
    CONSTRAINT ck_schedule_outbox_scope CHECK ((scope_type = 'GLOBAL' AND zone_id IS NULL) OR (scope_type = 'ZONE' AND zone_id IS NOT NULL))
);
CREATE INDEX idx_pricing_outbox_unpublished
    ON billing.pricing_outbox(occurred_at, id)
    WHERE published_at IS NULL;

CREATE TABLE billing.usage_settlement_runs (
    id                            UUID PRIMARY KEY,
    source_module                 TEXT NOT NULL,
    source_report_id              UUID NOT NULL,
    charge_kind_code              TEXT NOT NULL REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    zone_id                       UUID NOT NULL,
    window_start                  TIMESTAMPTZ NOT NULL,
    window_end                    TIMESTAMPTZ NOT NULL,
    pricing_schedule_id           UUID NOT NULL REFERENCES billing.pricing_schedules(id) ON DELETE RESTRICT,
    pricing_schedule_version_id   UUID NOT NULL REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    pricing_checksum              CHAR(64) NOT NULL,
    fencing_token                 BIGINT NOT NULL,
    status                        VARCHAR(16) NOT NULL DEFAULT 'RUNNING',
    retry_count                   INT NOT NULL DEFAULT 0,
    checkpoint                    TIMESTAMPTZ,
    started_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at                  TIMESTAMPTZ,
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_usage_settlement_report_kind UNIQUE (source_module, source_report_id, charge_kind_code),
    CONSTRAINT ck_usage_settlement_window CHECK (window_end > window_start),
    CONSTRAINT ck_usage_settlement_status CHECK (status IN ('RUNNING', 'RETRYING', 'COMPLETED', 'FAILED')),
    CONSTRAINT ck_usage_settlement_retry CHECK (retry_count >= 0)
);
CREATE INDEX idx_usage_settlement_retry
    ON billing.usage_settlement_runs(status, updated_at, source_module, charge_kind_code);

ALTER TABLE billing.wallet_ledger_entries
    ADD COLUMN module_code TEXT,
    ADD COLUMN charge_kind_code TEXT REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    ADD COLUMN usage_settlement_run_id UUID REFERENCES billing.usage_settlement_runs(id) ON DELETE RESTRICT,
    ADD COLUMN pricing_schedule_id UUID REFERENCES billing.pricing_schedules(id) ON DELETE RESTRICT,
    ADD COLUMN pricing_schedule_version_id UUID REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    ADD COLUMN pricing_checksum CHAR(64),
    ADD COLUMN adjustment_of_ledger_entry_id UUID REFERENCES billing.wallet_ledger_entries(id) ON DELETE RESTRICT,
    ADD COLUMN adjustment_reason VARCHAR(64),
    ADD COLUMN source_evidence_hash CHAR(64);

ALTER TABLE billing.wallet_ledger_entries
    DROP COLUMN IF EXISTS billing_run_id,
    DROP COLUMN IF EXISTS tier_version_id,
    DROP COLUMN IF EXISTS service_type;

ALTER TABLE billing.unrated_usage
    ADD COLUMN module_code TEXT,
    ADD COLUMN charge_kind_code TEXT REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    ADD COLUMN source_report_id UUID,
    ADD COLUMN source_evidence_hash CHAR(64),
    ADD COLUMN pricing_schedule_version_id UUID REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT;

ALTER TABLE billing.unrated_usage
    DROP COLUMN IF EXISTS service_type;

ALTER TABLE billing.storage_usage_line_inbox
    ADD COLUMN IF NOT EXISTS module_code TEXT,
    ADD COLUMN IF NOT EXISTS charge_kind_code TEXT REFERENCES billing.charge_kind_catalog(code) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS usage_settlement_run_id UUID REFERENCES billing.usage_settlement_runs(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS pricing_schedule_version_id UUID REFERENCES billing.pricing_schedule_versions(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS pricing_checksum CHAR(64);

-- Exactly three Global Storage schedules are seeded. Rates preserve the old
-- baseline while making raw units explicit: bytes use a MiB denominator and
-- storage uses fixed-point decimal GB-hour micros.
INSERT INTO billing.pricing_schedules
    (id, code, display_name, charge_kind_code, pricing_model, scope_type, currency)
VALUES
    ('019f3d3e-998a-7894-9236-c5122634cb5a', 'storage-capacity-payg', 'Storage capacity PAYG', 'storage.capacity.gb_hour', 'PROGRESSIVE_UNIT', 'GLOBAL', 'USD'),
    ('019f3d3e-998d-7894-9236-c5122634cb5d', 'storage-network-in-payg', 'Storage network in PAYG', 'storage.network_in.byte', 'PROGRESSIVE_UNIT', 'GLOBAL', 'USD'),
    ('019f3d3e-9990-7894-9236-c5122634cb60', 'storage-network-out-payg', 'Storage network out PAYG', 'storage.network_out.byte', 'PROGRESSIVE_UNIT', 'GLOBAL', 'USD')
ON CONFLICT (id) DO NOTHING;

INSERT INTO billing.pricing_schedule_versions
    (id, pricing_schedule_id, pricing_model, version_number, status, effective_from, checksum, change_reason)
VALUES
    ('b33aa15e-0421-4185-658b-f0b8132c1723', '019f3d3e-998a-7894-9236-c5122634cb5a', 'PROGRESSIVE_UNIT', 1, 'ACTIVE', '2026-07-18 10:11:25.589234+00', '2f79fe64793380e9fba8753146ff2e84711a6ed8fe1199a98101a94c7c8b9170', 'Initial PAYG storage schedule'),
    ('c33aa15e-0421-4185-658b-f0b8132c1723', '019f3d3e-998d-7894-9236-c5122634cb5d', 'PROGRESSIVE_UNIT', 1, 'ACTIVE', '2026-07-18 10:11:25.589234+00', '2944042fdef5d3a42df0516fc3374a6d0ab446383187111e1d6291081fcacbf4', 'Initial PAYG storage schedule'),
    ('d33aa15e-0421-4185-658b-f0b8132c1723', '019f3d3e-9990-7894-9236-c5122634cb60', 'PROGRESSIVE_UNIT', 1, 'ACTIVE', '2026-07-18 10:11:25.589234+00', 'eb89424de9f9aa2ec58d61b2bcd952a1125dab94a4a9ef2f072692bf180258d4', 'Initial PAYG storage schedule')
ON CONFLICT (id) DO NOTHING;

INSERT INTO billing.pricing_schedule_scalar_brackets
    (id, pricing_schedule_version_id, range_start_quantity, range_end_quantity, price_numerator_micro_units, price_denominator_quantity)
VALUES
    ('755b2b3d-de1d-fe8f-1171-365216565645', 'b33aa15e-0421-4185-658b-f0b8132c1723', 0, 50000000, 15000, 1000000),
    ('9d43c699-6dfa-a17e-32ca-08b67e41b411', 'b33aa15e-0421-4185-658b-f0b8132c1723', 50000000, NULL, 12000, 1000000),
    ('c67f0739-1907-6080-56b0-6b89c6fbe387', 'c33aa15e-0421-4185-658b-f0b8132c1723', 0, 107374182400, 0, 1048576),
    ('5b9a51cf-8327-e7c1-17b0-a28d1defe8ef', 'c33aa15e-0421-4185-658b-f0b8132c1723', 107374182400, NULL, 5000, 1048576),
    ('2b910002-53af-531a-dd81-7bd7b71d465b', 'd33aa15e-0421-4185-658b-f0b8132c1723', 0, NULL, 90, 1048576)
ON CONFLICT (id) DO NOTHING;
