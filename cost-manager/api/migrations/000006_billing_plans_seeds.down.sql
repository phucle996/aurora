-- Rollback seeds
DELETE FROM billing.plan_metrics WHERE id LIKE '019f3d4b-%';
DELETE FROM billing.plans           WHERE id LIKE '019f3d4a-%';
DELETE FROM billing.prices          WHERE id LIKE '019f3d3e-1%';
