-- Migration 000002: Seed dữ liệu mẫu cho billing schema
-- 1. Seed Accountant User (Mã nhân viên: accountant, Khóa công khai Ed25519)
-- [DEV LOGIN CREDENTIALS]
-- Employee Code : accountant
-- Secret Key    : EAmpu8dW0c1PqwshwPnxqaLI+7oeB81NQFvL0C1ThSY=  ← Ed25519 Private Key (B64)
-- Public Key    : 9RLIsCm/rBcPFVCCCLP1OwrxjJTFV+3I0mmorUbrmxk=  ← Stored in DB
-- CẢNH BÁO: Private key này CHỈ dùng cho môi trường DEV/local.
INSERT INTO billing.users (id, employee_code, public_key, fullname, email, role_id, level, status) VALUES
('019f3d3e-9999-7894-9236-c5122634cb4f', 'accountant', '9RLIsCm/rBcPFVCCCLP1OwrxjJTFV+3I0mmorUbrmxk=', 'Kế toán trưởng', 'finance@aurora.cloud', 'billing_admin', 2, 'ACTIVE')
ON CONFLICT (id) DO NOTHING;
