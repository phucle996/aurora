-- Rollback Migration 000001: Xóa 3 bảng cốt lõi

DROP TABLE IF EXISTS billing.subscriptions;
DROP TABLE IF EXISTS billing.pack_plans;
DROP TABLE IF EXISTS billing.plans;
DROP TABLE IF EXISTS billing.packs;
