DELETE FROM billing.pricing_schedule_scalar_brackets
WHERE id IN (
    '755b2b3d-de1d-fe8f-1171-365216565645',
    '9d43c699-6dfa-a17e-32ca-08b67e41b411',
    'c67f0739-1907-6080-56b0-6b89c6fbe387',
    '5b9a51cf-8327-e7c1-17b0-a28d1defe8ef',
    '2b910002-53af-531a-dd81-7bd7b71d465b'
);
DELETE FROM billing.pricing_schedule_versions
WHERE id IN (
    'b33aa15e-0421-4185-658b-f0b8132c1723',
    'c33aa15e-0421-4185-658b-f0b8132c1723',
    'd33aa15e-0421-4185-658b-f0b8132c1723'
);
DELETE FROM billing.pricing_schedules
WHERE id IN (
    '019f3d3e-998a-7894-9236-c5122634cb5a',
    '019f3d3e-998d-7894-9236-c5122634cb5d',
    '019f3d3e-9990-7894-9236-c5122634cb60'
);
DELETE FROM billing.charge_kind_catalog
WHERE code IN ('storage.network_in.byte', 'storage.network_out.byte', 'storage.capacity.gb_hour', 'hypervisor.vm_shape.duration');
