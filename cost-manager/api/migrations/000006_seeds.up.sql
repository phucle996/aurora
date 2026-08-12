-- Only generic charge kinds and PAYG schedules are seeded. No free/pro plan,
-- subscription, user wallet or environment-specific commercial entitlement is
-- part of the billing baseline.

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
