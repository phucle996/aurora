-- [SEEDS] Đơn giá Storage thực tế — 4 metric types × 2 zones
-- service_type=STORAGE, metric_type chi tiết theo chiều đo lường

INSERT INTO billing.prices (id, service_type, metric_type, zone_code, unit, unit_price, currency, tier, free_quota)
VALUES
  -- Storage at rest (GB/giờ, billing theo giờ, convert ra tháng khi hiển thị)
  ('019f3d3e-1001-7894-9236-c5122634cb4f', 'STORAGE', 'STORAGE_AT_REST',   'vn-n1',  'GB_HOUR', 0.694444, 'VND', 'STANDARD', 5.0),
  ('019f3d3e-1002-7894-9236-c5122634cb4f', 'STORAGE', 'STORAGE_AT_REST',   'vn-n2',  'GB_HOUR', 0.625000, 'VND', 'STANDARD', 5.0),

  -- Egress ra Internet (tính theo GB, không miễn phí)
  ('019f3d3e-1003-7894-9236-c5122634cb4f', 'STORAGE', 'EGRESS_INTERNET',   'global', 'GB',      1000.000000, 'VND', 'STANDARD', 1.0),

  -- Egress cross-zone (nội bộ Aurora giữa các zone, giá thấp hơn)
  ('019f3d3e-1004-7894-9236-c5122634cb4f', 'STORAGE', 'EGRESS_CROSS_ZONE', 'global', 'GB',      200.000000, 'VND', 'STANDARD', 0.0),

  -- Request Write (PUT/COPY/POST/LIST) per 1000 ops
  ('019f3d3e-1005-7894-9236-c5122634cb4f', 'STORAGE', 'REQUEST_WRITE',     'global', 'PER_1K_OPS', 50.000000, 'VND', 'STANDARD', 10.0),

  -- Request Read (GET/HEAD) per 1000 ops — rẻ hơn write
  ('019f3d3e-1006-7894-9236-c5122634cb4f', 'STORAGE', 'REQUEST_READ',      'global', 'PER_1K_OPS', 5.000000,  'VND', 'STANDARD', 10.0)

ON CONFLICT (service_type, metric_type, zone_code, tier) DO NOTHING;

-- [SEEDS] Gói cước Storage subscription

-- Gói Basic Storage vn-n1: 50GB + 10GB egress/tháng → 99,000 VND/tháng
INSERT INTO billing.plans (id, name, code, service_type, zone_code, monthly_price, currency, status, description)
VALUES
  ('019f3d4a-0001-7894-9236-c5122634cb4f',
   'Basic Storage',
   'STORAGE_BASIC_VN1',
   'STORAGE', 'vn-n1',
   99000.0000, 'VND', 'ACTIVE',
   'Gói lưu trữ cơ bản: 50 GB dung lượng + 10 GB egress/tháng. Phù hợp cho cá nhân và dự án nhỏ.'),

  ('019f3d4a-0002-7894-9236-c5122634cb4f',
   'Pro Storage',
   'STORAGE_PRO_VN1',
   'STORAGE', 'vn-n1',
   499000.0000, 'VND', 'ACTIVE',
   'Gói lưu trữ chuyên nghiệp: 500 GB dung lượng + 100 GB egress/tháng. Phù hợp cho doanh nghiệp vừa.')

ON CONFLICT (code) DO NOTHING;

-- Quota của từng gói
INSERT INTO billing.plan_metrics (id, plan_id, metric_type, quota, unit)
VALUES
  -- Basic: 50 GB storage, 10 GB egress internet, 50K write ops, 500K read ops
  ('019f3d4b-0001-7894-9236-c5122634cb4f', '019f3d4a-0001-7894-9236-c5122634cb4f', 'STORAGE_AT_REST',   50.0,    'GB'),
  ('019f3d4b-0002-7894-9236-c5122634cb4f', '019f3d4a-0001-7894-9236-c5122634cb4f', 'EGRESS_INTERNET',   10.0,    'GB'),
  ('019f3d4b-0003-7894-9236-c5122634cb4f', '019f3d4a-0001-7894-9236-c5122634cb4f', 'REQUEST_WRITE',     50.0,    'PER_1K_OPS'),
  ('019f3d4b-0004-7894-9236-c5122634cb4f', '019f3d4a-0001-7894-9236-c5122634cb4f', 'REQUEST_READ',      500.0,   'PER_1K_OPS'),

  -- Pro: 500 GB storage, 100 GB egress internet, 500K write ops, 5M read ops
  ('019f3d4b-0005-7894-9236-c5122634cb4f', '019f3d4a-0002-7894-9236-c5122634cb4f', 'STORAGE_AT_REST',   500.0,   'GB'),
  ('019f3d4b-0006-7894-9236-c5122634cb4f', '019f3d4a-0002-7894-9236-c5122634cb4f', 'EGRESS_INTERNET',   100.0,   'GB'),
  ('019f3d4b-0007-7894-9236-c5122634cb4f', '019f3d4a-0002-7894-9236-c5122634cb4f', 'REQUEST_WRITE',     500.0,   'PER_1K_OPS'),
  ('019f3d4b-0008-7894-9236-c5122634cb4f', '019f3d4a-0002-7894-9236-c5122634cb4f', 'REQUEST_READ',      5000.0,  'PER_1K_OPS')
ON CONFLICT DO NOTHING;
