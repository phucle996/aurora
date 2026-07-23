-- Migration 000006: Seed dữ liệu mẫu cho billing schema

-- 1. Seed biểu giá cơ sở Tiers. Production admin phải được provision qua IAM,
-- không seed credential có private key biết trước vào baseline migration.
INSERT INTO billing.tiers (id, name, code, service_type, metadata_version) VALUES
('019f3d3e-998a-7894-9236-c5122634cb5a', 'Standard Storage Base Tier', 'STORAGE_STD_BASE', 'STORAGE', 1),
('019f3d3e-998d-7894-9236-c5122634cb5d', 'Inbound Network Base Tier', 'NETWORK_IN_BASE', 'NETWORK_IN', 1),
('019f3d3e-9990-7894-9236-c5122634cb60', 'Outbound Network Base Tier', 'NETWORK_OUT_BASE', 'NETWORK_OUT', 1)
ON CONFLICT (id) DO NOTHING;

-- 2. Seed Pricing Versions
INSERT INTO billing.tier_versions (id, tier_id, version_number, status, effective_from, checksum, change_reason, created_by) VALUES
('b33aa15e-0421-4185-658b-f0b8132c1723', '019f3d3e-998a-7894-9236-c5122634cb5a', 1, 'ACTIVE', '2026-07-18 10:11:25.589234+00', 'a4f31566a87f657cf0781b7b92f7aa9ccbb081d269dc66590cc7e2bbc0e8476e', 'Initial seeding', NULL),
('c33aa15e-0421-4185-658b-f0b8132c1723', '019f3d3e-998d-7894-9236-c5122634cb5d', 1, 'ACTIVE', '2026-07-18 10:11:25.589234+00', '79b64a2f51706bc5bf1050e367ec709f9d5ea2c0d6a8d4c761dffca4ffce8a6c', 'Initial seeding', NULL),
('d33aa15e-0421-4185-658b-f0b8132c1723', '019f3d3e-9990-7894-9236-c5122634cb60', 1, 'ACTIVE', '2026-07-18 10:11:25.589234+00', '60f4293c62a9e6a46766d661a8cf739e125ffc4426419a57bf4a2f911c324924', 'Initial seeding', NULL)
ON CONFLICT (id) DO NOTHING;

-- 3. Seed Pricing Version Ranges (MB format)
INSERT INTO billing.tier_version_ranges (id, tier_version_id, range_start, range_end, base_unit_price) VALUES
-- STORAGE Ranges (0 - 50GB @15000, >50GB @12000)
('755b2b3d-de1d-fe8f-1171-365216565645', 'b33aa15e-0421-4185-658b-f0b8132c1723', 0, 51200, 15000),
('9d43c699-6dfa-a17e-32ca-08b67e41b411', 'b33aa15e-0421-4185-658b-f0b8132c1723', 51200, 0, 12000),

-- NETWORK_IN Ranges (0 - 100GB @0, >100GB @5000)
('c67f0739-1907-6080-56b0-6b89c6fbe387', 'c33aa15e-0421-4185-658b-f0b8132c1723', 0, 102400, 0),
('5b9a51cf-8327-e7c1-17b0-a28d1defe8ef', 'c33aa15e-0421-4185-658b-f0b8132c1723', 102400, 0, 5000),

-- NETWORK_OUT: Free Tier dùng promotional credit, không reset quota trên từng metering row.
('2b910002-53af-531a-dd81-7bd7b71d465b', 'd33aa15e-0421-4185-658b-f0b8132c1723', 0, 0, 90)
ON CONFLICT (id) DO NOTHING;

-- 4. Seed Plans (Resource SKU Plans)
INSERT INTO billing.plans (id, name, code, service_type, zone_id, tier_id, zone_multiplier, status) VALUES
('019f3d3e-998f-7894-9236-c5122634cb99', 'Standard Storage - VN Zone', 'STORAGE_SKU_VN', 'STORAGE', '019f3d3e-0000-7894-9236-c5122634cb01', '019f3d3e-998a-7894-9236-c5122634cb5a', 1.00, 'ACTIVE')
ON CONFLICT (id) DO NOTHING;

-- 5. Seed Packs
INSERT INTO billing.packs (id, name, code, tier_target, monthly_price, currency, discount_rate, status) VALUES
('019f3d3e-998f-7894-9236-c5122634cb02', 'Free Tier', 'FREE_TIER', 'FREE_TIER', 0, 'USD', 0.00, 'ACTIVE')
ON CONFLICT (id) DO NOTHING;

-- 6. Seed Pack Plans
INSERT INTO billing.pack_plans (id, pack_id, plan_id, included_quota, overage_unit_price) VALUES
('019f3d3e-998f-7894-9236-c5122634cb03', '019f3d3e-998f-7894-9236-c5122634cb02', '019f3d3e-998f-7894-9236-c5122634cb99', 0, 0)
ON CONFLICT (id) DO NOTHING;

-- 7. Campaign $100 Free Tier. Grant chỉ phát sinh khi owner activate subscription thành công.
INSERT INTO billing.promotion_campaigns
    (id, code, name, amount_micro_units, currency, starts_at, status)
VALUES
    ('019f3d3e-998f-7894-9236-c5122634cb04', 'FREE_TIER_100_USD', 'Free Tier USD 100 promotional credit', 100000000, 'USD', '2026-01-01 00:00:00+00', 'ACTIVE')
ON CONFLICT (id) DO NOTHING;

-- 8. [COMMENT]: Seed ví Personal Wallet mặc định cho Root IAM User phục vụ môi trường dev/local baseline
INSERT INTO billing.wallets
    (id, owner_id, owner_type, currency, cash_balance, promotional_balance, overdraft_limit, status, version)
VALUES
    ('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 'PERSONAL', 'USD', 0, 0, 0, 'ACTIVE', 1)
ON CONFLICT (owner_id, owner_type, currency) DO NOTHING;
