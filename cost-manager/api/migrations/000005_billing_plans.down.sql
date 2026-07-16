-- Rollback: xóa theo thứ tự ngược (FK phụ thuộc trước)
DROP TABLE IF EXISTS billing.subscriptions;
DROP TABLE IF EXISTS billing.plan_metrics;
DROP TABLE IF EXISTS billing.plans;
