-- [COMMENT]: Index cho các giao dịch theo ví tiền phục vụ tối ưu hóa truy vấn lịch sử giao dịch
CREATE INDEX IF NOT EXISTS idx_transactions_wallet_id ON billing.transactions(wallet_id);

-- [COMMENT]: Index cho phép truy vấn nhanh giao dịch theo loại dịch vụ
CREATE INDEX IF NOT EXISTS idx_transactions_service_type ON billing.transactions(service_type);

-- [COMMENT]: Index và Unique Constraint cho cấu hình đơn giá theo vùng, loại dịch vụ và phân hạng
CREATE UNIQUE INDEX IF NOT EXISTS uidx_prices_service_zone_tier ON billing.prices(service_type, zone_code, tier);

-- [COMMENT]: Index hỗ trợ truy vấn đơn giá có hiệu lực
CREATE INDEX IF NOT EXISTS idx_prices_effective_period ON billing.prices(effective_from, effective_to);
