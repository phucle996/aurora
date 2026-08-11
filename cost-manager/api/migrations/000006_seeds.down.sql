-- Migration 000006 Down: Xóa dữ liệu hạt giống
DELETE FROM billing.promotion_campaigns;
DELETE FROM billing.pack_plans;
DELETE FROM billing.packs;
DELETE FROM billing.plans;
DELETE FROM billing.tier_version_ranges;
DELETE FROM billing.tier_versions;
DELETE FROM billing.tiers;
DELETE FROM billing.wallets;
