DROP INDEX IF EXISTS billing.idx_referral_campaign_catalog;
DROP INDEX IF EXISTS billing.idx_payment_intents_pending_expiry;
DROP INDEX IF EXISTS billing.idx_payment_intents_actor_created;
DROP INDEX IF EXISTS billing.idx_payment_intents_owner_created;
DROP INDEX IF EXISTS billing.uq_referral_reservation_live_owner;
DROP INDEX IF EXISTS billing.idx_referral_reservations_campaign_capacity;

DROP TABLE IF EXISTS billing.referral_redemptions;
DROP TABLE IF EXISTS billing.payment_webhook_inbox;
DROP TABLE IF EXISTS billing.payment_intents;
DROP TABLE IF EXISTS billing.referral_reservations;

ALTER TABLE billing.wallet_provision_inbox
    DROP COLUMN IF EXISTS actor_user_id,
    DROP COLUMN IF EXISTS owner_type;

ALTER TABLE billing.wallet_ledger_entries
    DROP COLUMN IF EXISTS actor_user_id;

UPDATE billing.packs
SET status='ACTIVE', updated_at=NOW()
WHERE code='FREE_TIER' AND status='DEPRECATED';

UPDATE billing.promotion_campaigns
SET status='ACTIVE', updated_at=NOW()
WHERE code='FREE_TIER_100_USD' AND status='ENDED';

ALTER TABLE billing.promotion_campaigns
    DROP CONSTRAINT IF EXISTS ck_promotion_version_positive,
    DROP CONSTRAINT IF EXISTS ck_promotion_max_redemptions,
    DROP CONSTRAINT IF EXISTS ck_promotion_minimum_top_up,
    DROP CONSTRAINT IF EXISTS ck_promotion_campaign_type,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS max_redemptions,
    DROP COLUMN IF EXISTS minimum_top_up_micro_units,
    DROP COLUMN IF EXISTS campaign_type;

UPDATE billing.wallets
SET status = 'SUSPENDED'
WHERE status = 'PENDING_ACTIVATION';

ALTER TABLE billing.wallets
    ALTER COLUMN status DROP DEFAULT,
    ALTER COLUMN status TYPE billing.wallet_status USING status::text::billing.wallet_status,
    ALTER COLUMN status SET DEFAULT 'ACTIVE'::billing.wallet_status;

DROP TYPE IF EXISTS billing.wallet_lifecycle_status;
