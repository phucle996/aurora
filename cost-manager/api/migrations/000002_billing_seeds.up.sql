-- Migration 000002: Seed dữ liệu mẫu cho 3 bảng packs, plans, pack_plans

-- 1. Seed Resource SKU Plans
INSERT INTO billing.plans (id, name, code, service_type, zone_code, monthly_price, currency, status, description) VALUES
('b0000000-0000-0000-0000-000000000001', 'Storage Standard 10GB', 'STORAGE_SKU_10GB', 'STORAGE', 'global', 50000.0000, 'VND', 'ACTIVE', 'Gói lưu trữ dung lượng 10GB'),
('b0000000-0000-0000-0000-000000000002', 'Storage High Capacity 100GB', 'STORAGE_SKU_100GB', 'STORAGE', 'global', 400000.0000, 'VND', 'ACTIVE', 'Gói lưu trữ dung lượng lớn 100GB'),
('b0000000-0000-0000-0000-000000000003', 'Compute Core 2vCPU 4GB RAM', 'VM_SKU_2C4G', 'VM', 'vn-n1', 650000.0000, 'VND', 'ACTIVE', 'Máy chủ ảo 2 Core vCPU và 4GB RAM');

-- 2. Seed Commercial Packs (Student, Free Tier, Enterprise)
INSERT INTO billing.packs (id, name, code, tier_target, monthly_price, currency, discount_rate, status, description) VALUES
('a0000000-0000-0000-0000-000000000001', 'Student Pack', 'STUDENT_PACK', 'STUDENT', 0.0000, 'VND', 100.00, 'ACTIVE', 'Gói học tập miễn phí cho sinh viên'),
('a0000000-0000-0000-0000-000000000002', 'Free Tier', 'FREE_TIER', 'FREE_TIER', 0.0000, 'VND', 100.00, 'ACTIVE', 'Gói miễn phí trải nghiệm ban đầu'),
('a0000000-0000-0000-0000-000000000003', 'Enterprise Pack', 'ENTERPRISE_PACK', 'ENTERPRISE', 2500000.0000, 'VND', 15.00, 'ACTIVE', 'Gói doanh nghiệp cao cấp kèm SLA 99.99%');

-- 3. Seed Pack_Plans (Gom nhóm Plans vào Pack)
INSERT INTO billing.pack_plans (id, pack_id, plan_id, included_quota, overage_unit_price) VALUES
('c0000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000001', 'b0000000-0000-0000-0000-000000000001', 5.0000, 1500.000000),
('c0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000002', 'b0000000-0000-0000-0000-000000000001', 10.0000, 1200.000000),
('c0000000-0000-0000-0000-000000000003', 'a0000000-0000-0000-0000-000000000003', 'b0000000-0000-0000-0000-000000000002', 1000.0000, 800.000000),
('c0000000-0000-0000-0000-000000000004', 'a0000000-0000-0000-0000-000000000003', 'b0000000-0000-0000-0000-000000000003', 720.0000, 1000.000000);
