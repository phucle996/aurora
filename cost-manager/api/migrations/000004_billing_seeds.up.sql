-- [COMMENT]: Chèn các đơn giá dịch vụ mẫu theo zone phù hợp với frontend và hệ thống thực tế
INSERT INTO billing.prices (id, service_type, zone_code, unit_price, currency, tier) VALUES
('019f3d3e-0001-7894-9236-c5122634cb4f', 'STORAGE_GB_MONTH', 'vn-n1', 500.000000, 'VND', 'STANDARD'),
('019f3d3e-0002-7894-9236-c5122634cb4f', 'STORAGE_GB_MONTH', 'vn-n2', 450.000000, 'VND', 'STANDARD'),
('019f3d3e-0003-7894-9236-c5122634cb4f', 'TRAFFIC_EGRESS_GB', 'global', 1000.000000, 'VND', 'STANDARD'),
('019f3d3e-0004-7894-9236-c5122634cb4f', 'MAIL_SENT', 'global', 10.000000, 'VND', 'STANDARD'),
('019f3d3e-0005-7894-9236-c5122634cb4f', 'VM_CORE_HOUR', 'vn-n1', 200.000000, 'VND', 'STANDARD'),
('019f3d3e-0006-7894-9236-c5122634cb4f', 'VM_CORE_HOUR', 'vn-n3', 180.000000, 'VND', 'STANDARD')
ON CONFLICT (id) DO NOTHING;
