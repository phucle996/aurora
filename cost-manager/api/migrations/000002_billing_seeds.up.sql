-- Migration 000002: Seed dữ liệu mẫu cho billing schema
-- Thực hiện seed user accountant và các biểu giá cơ sở Tiers/Tier Ranges cho Storage, Inbound và Outbound network sử dụng đơn vị MB và UUID thực tế

-- 1. Seed Accountant User (Mã nhân viên: accountant, Khóa công khai Ed25519)
-- [DEV LOGIN CREDENTIALS]
-- Employee Code : accountant
-- Secret Key    : EAmpu8dW0c1PqwshwPnxqaLI+7oeB81NQFvL0C1ThSY=  ← Ed25519 Private Key (B64)
-- Public Key    : 9RLIsCm/rBcPFVCCCLP1OwrxjJTFV+3I0mmorUbrmxk=  ← Stored in DB
-- CẢNH BÁO: Private key này CHỈ dùng cho môi trường DEV/local.
INSERT INTO billing.users (id, employee_code, public_key, fullname, email, role_id, level, status) VALUES
('019f3d3e-9999-7894-9236-c5122634cb4f', 'accountant', '9RLIsCm/rBcPFVCCCLP1OwrxjJTFV+3I0mmorUbrmxk=', 'Kế toán trưởng', 'finance@aurora.cloud', 'billing_admin', 2, 'ACTIVE')
ON CONFLICT (id) DO NOTHING;

-- 2. Seed biểu giá cơ sở Tiers cho dịch vụ Object Storage
-- Standard Storage Base Tier đóng vai trò là mốc tính giá sàn chung cho dịch vụ lưu trữ
INSERT INTO billing.tiers (id, name, code, service_type) VALUES
('019f3d3e-998a-7894-9236-c5122634cb5a', 'Standard Storage Base Tier', 'STORAGE_STD_BASE', 'STORAGE')
ON CONFLICT (id) DO NOTHING;

-- 3. Seed các nấc giá lũy tiến (Tier Ranges) tương ứng cho Storage (Đơn vị Megabytes - MB)
-- Nấc 1: Từ 0 - 50 GB (51200 MB). Đơn giá cơ sở: 15000 Micro-units USD/MB/Hour
INSERT INTO billing.tier_ranges (id, tier_id, range_start, range_end, base_unit_price) VALUES
('019f3d3e-998b-7894-9236-c5122634cb5b', '019f3d3e-998a-7894-9236-c5122634cb5a', 0, 51200, 15000)
ON CONFLICT (id) DO NOTHING;

-- Nấc 2: Trên 50 GB (>51200 MB). Đơn giá cơ sở ưu đãi hơn: 12000 Micro-units USD/MB/Hour (Quy ước: 0 là không giới hạn)
INSERT INTO billing.tier_ranges (id, tier_id, range_start, range_end, base_unit_price) VALUES
('019f3d3e-998c-7894-9236-c5122634cb5c', '019f3d3e-998a-7894-9236-c5122634cb5a', 51200, 0, 12000)
ON CONFLICT (id) DO NOTHING;

-- 4. Seed biểu giá cơ sở Tiers cho Inbound Traffic (Băng thông đi vào hệ thống)
-- Inbound Network Base Tier dùng để tính toán chi phí nhận dữ liệu
INSERT INTO billing.tiers (id, name, code, service_type) VALUES
('019f3d3e-998d-7894-9236-c5122634cb5d', 'Inbound Network Base Tier', 'NETWORK_IN_BASE', 'NETWORK_IN')
ON CONFLICT (id) DO NOTHING;

-- 5. Seed các nấc giá (Tier Ranges) tương ứng cho Inbound Traffic (Đơn vị Megabytes - MB)
-- Nấc 1: Từ 0 - 100 GB (102400 MB). Đơn giá cơ sở: 0 Micro-units (Miễn phí hoàn toàn 100GB dữ liệu đi vào đầu tiên)
INSERT INTO billing.tier_ranges (id, tier_id, range_start, range_end, base_unit_price) VALUES
('019f3d3e-998e-7894-9236-c5122634cb5e', '019f3d3e-998d-7894-9236-c5122634cb5d', 0, 102400, 0)
ON CONFLICT (id) DO NOTHING;

-- Nấc 2: Trên 100 GB (>102400 MB). Đơn giá cơ sở: 5000 Micro-units USD/MB (~ $0.005/MB vượt định mức) (Quy ước: 0 là không giới hạn)
INSERT INTO billing.tier_ranges (id, tier_id, range_start, range_end, base_unit_price) VALUES
('019f3d3e-998f-7894-9236-c5122634cb5f', '019f3d3e-998d-7894-9236-c5122634cb5d', 102400, 0, 5000)
ON CONFLICT (id) DO NOTHING;

-- 6. Seed biểu giá cơ sở Tiers cho Outbound Traffic (Băng thông đi ra ngoài Internet)
-- Outbound Network Base Tier dùng để tính toán chi phí xuất truyền dữ liệu ra Internet
INSERT INTO billing.tiers (id, name, code, service_type) VALUES
('019f3d3e-9990-7894-9236-c5122634cb60', 'Outbound Network Base Tier', 'NETWORK_OUT_BASE', 'NETWORK_OUT')
ON CONFLICT (id) DO NOTHING;

-- 7. Seed các nấc giá (Tier Ranges) tương ứng cho Outbound Traffic (Đơn vị Megabytes - MB)
-- Nấc 1: Từ 0 - 10 GB (10240 MB). Đơn giá cơ sở: 0 Micro-units (Miễn phí 10GB truyền tải đầu tiên hàng tháng)
INSERT INTO billing.tier_ranges (id, tier_id, range_start, range_end, base_unit_price) VALUES
('019f3d3e-9991-7894-9236-c5122634cb61', '019f3d3e-9990-7894-9236-c5122634cb60', 0, 10240, 0)
ON CONFLICT (id) DO NOTHING;

-- Nấc 2: Trên 10 GB (>10240 MB). Đơn giá cơ sở: 90000 Micro-units USD/MB (~ $0.09/MB vượt định mức) (Quy ước: 0 là không giới hạn)
INSERT INTO billing.tier_ranges (id, tier_id, range_start, range_end, base_unit_price) VALUES
('019f3d3e-9992-7894-9236-c5122634cb62', '019f3d3e-9990-7894-9236-c5122634cb60', 10240, 0, 90000)
ON CONFLICT (id) DO NOTHING;
