-- Only generic charge kinds and PAYG schedules are seeded. No free/pro plan,
-- subscription, user wallet or environment-specific commercial entitlement is
-- part of the billing baseline.

INSERT INTO billing.charge_kind_catalog
    (code, module_code, pricing_model, raw_input_unit, observation_semantics, metering_contract)
VALUES
    ('storage.network_in.byte', 'storage', 'PROGRESSIVE_UNIT', 'BYTE', 'CLOSED_DELTA', 'StorageUsageReportV1'),
    ('storage.network_out.byte', 'storage', 'PROGRESSIVE_UNIT', 'BYTE', 'CLOSED_DELTA', 'StorageUsageReportV1'),
    ('storage.capacity.gb_hour', 'storage', 'PROGRESSIVE_UNIT', 'BYTE_HOUR', 'CLOSED_INTEGRAL', 'StorageUsageReportV1'),
    ('hypervisor.vcpu.allocated_second', 'hypervisor', 'PROGRESSIVE_UNIT', 'CORE_SECOND', 'ALLOCATED_DURATION', 'HypervisorAllocationChangedV1'),
    ('hypervisor.memory_mib.allocated_second', 'hypervisor', 'PROGRESSIVE_UNIT', 'MIB_SECOND', 'ALLOCATED_DURATION', 'HypervisorAllocationChangedV1'),
    ('hypervisor.disk_gib.allocated_second', 'hypervisor', 'PROGRESSIVE_UNIT', 'GIB_SECOND', 'ALLOCATED_DURATION', 'HypervisorAllocationChangedV1'),
    ('hypervisor.gpu.allocated_second', 'hypervisor', 'PROGRESSIVE_UNIT', 'GPU_SECOND', 'ALLOCATED_DURATION', 'HypervisorAllocationChangedV1'),
    ('hypervisor.network_in.byte', 'hypervisor', 'PROGRESSIVE_UNIT', 'BYTE', 'CLOSED_DELTA', 'HypervisorNetworkUsageReportV1'),
    ('hypervisor.network_out.byte', 'hypervisor', 'PROGRESSIVE_UNIT', 'BYTE', 'CLOSED_DELTA', 'HypervisorNetworkUsageReportV1'),
    ('mail.delivery.accepted_recipient', 'mail', 'PROGRESSIVE_UNIT', 'RECIPIENT', 'ACCEPTED_EVENT', 'MailAcceptedUsageV1')
ON CONFLICT (code) DO NOTHING;

-- GPU stays fail-closed until provider enforcement is shipped. Network and
-- CPU/RAM/disk have complete evidence workflows and are enabled for operator
-- publication through the immutable Global schedule workflow.
UPDATE billing.charge_kind_catalog
SET status = 'DISABLED'
WHERE code IN (
    'hypervisor.gpu.allocated_second'
);

INSERT INTO billing.pricing_schedules
    (id, code, display_name, charge_kind_code, pricing_model, currency)
VALUES
    ('019f3d3e-998a-7894-9236-c5122634cb5a', 'storage-capacity-payg', 'Storage capacity PAYG', 'storage.capacity.gb_hour', 'PROGRESSIVE_UNIT', 'USD'),
    ('019f3d3e-998d-7894-9236-c5122634cb5d', 'storage-network-in-payg', 'Storage network in PAYG', 'storage.network_in.byte', 'PROGRESSIVE_UNIT', 'USD'),
    ('019f3d3e-9990-7894-9236-c5122634cb60', 'storage-network-out-payg', 'Storage network out PAYG', 'storage.network_out.byte', 'PROGRESSIVE_UNIT', 'USD'),
    ('019f3d3e-9993-7894-9236-c5122634cb63', 'hypervisor-vcpu-payg', 'Hypervisor allocated vCPU PAYG', 'hypervisor.vcpu.allocated_second', 'PROGRESSIVE_UNIT', 'USD'),
    ('019f3d3e-9996-7894-9236-c5122634cb66', 'hypervisor-memory-payg', 'Hypervisor allocated memory PAYG', 'hypervisor.memory_mib.allocated_second', 'PROGRESSIVE_UNIT', 'USD'),
    ('019f3d3e-9999-7894-9236-c5122634cb69', 'hypervisor-disk-payg', 'Hypervisor allocated disk PAYG', 'hypervisor.disk_gib.allocated_second', 'PROGRESSIVE_UNIT', 'USD'),
    ('019f3d3e-999c-7894-9236-c5122634cb6c', 'hypervisor-network-in-payg', 'Hypervisor network in PAYG', 'hypervisor.network_in.byte', 'PROGRESSIVE_UNIT', 'USD'),
    ('019f3d3e-999f-7894-9236-c5122634cb6f', 'hypervisor-network-out-payg', 'Hypervisor network out PAYG', 'hypervisor.network_out.byte', 'PROGRESSIVE_UNIT', 'USD'),
    ('019f3d3e-99a2-7894-9236-c5122634cb72', 'mail-accepted-recipient-payg', 'Mail accepted recipient PAYG', 'mail.delivery.accepted_recipient', 'PROGRESSIVE_UNIT', 'USD')
ON CONFLICT (id) DO NOTHING;

INSERT INTO billing.pricing_schedule_versions
    (id, pricing_schedule_id, pricing_model, version_number, status, effective_from, checksum, change_reason)
VALUES
    ('b33aa15e-0421-4185-658b-f0b8132c1723', '019f3d3e-998a-7894-9236-c5122634cb5a', 'PROGRESSIVE_UNIT', 1, 'ACTIVE', '2026-07-18 10:11:25.589234+00', '625d0d50cce646f5fbd2f988226d69f9d50f55c99cee9262990e060ba3f702d9', 'Initial PAYG storage schedule'),
    ('c33aa15e-0421-4185-658b-f0b8132c1723', '019f3d3e-998d-7894-9236-c5122634cb5d', 'PROGRESSIVE_UNIT', 1, 'ACTIVE', '2026-07-18 10:11:25.589234+00', '2d63d2e6bf1ae0e7a1a3eff896d1b6b629bf6bfcb3af9c44c3384fe4a613a80d', 'Initial PAYG storage schedule'),
    ('d33aa15e-0421-4185-658b-f0b8132c1723', '019f3d3e-9990-7894-9236-c5122634cb60', 'PROGRESSIVE_UNIT', 1, 'ACTIVE', '2026-07-18 10:11:25.589234+00', '4c6b95161b4d9b7254355bb97b04d6fe62d3c82bd1f832b7d82161d471d71401', 'Initial PAYG storage schedule')
ON CONFLICT (id) DO NOTHING;

INSERT INTO billing.pricing_schedule_scalar_brackets
    (id, pricing_schedule_version_id, range_start_quantity, range_end_quantity, price_numerator_micro_units, price_denominator_quantity)
VALUES
    ('755b2b3d-de1d-fe8f-1171-365216565645', 'b33aa15e-0421-4185-658b-f0b8132c1723', 0, 50000000000, 15000, 1000000000),
    ('9d43c699-6dfa-a17e-32ca-08b67e41b411', 'b33aa15e-0421-4185-658b-f0b8132c1723', 50000000000, NULL, 12000, 1000000000),
    ('c67f0739-1907-6080-56b0-6b89c6fbe387', 'c33aa15e-0421-4185-658b-f0b8132c1723', 0, 107374182400, 0, 1048576),
    ('5b9a51cf-8327-e7c1-17b0-a28d1defe8ef', 'c33aa15e-0421-4185-658b-f0b8132c1723', 107374182400, NULL, 5000, 1048576),
    ('2b910002-53af-531a-dd81-7bd7b71d465b', 'd33aa15e-0421-4185-658b-f0b8132c1723', 0, NULL, 90, 1048576)
ON CONFLICT (id) DO NOTHING;
