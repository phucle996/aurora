-- Revert columns to VARCHAR/TEXT
ALTER TABLE billing.wallets ALTER COLUMN owner_type TYPE VARCHAR(32) USING owner_type::text;
ALTER TABLE billing.wallets ALTER COLUMN status DROP DEFAULT;
ALTER TABLE billing.wallets ALTER COLUMN status TYPE VARCHAR(32) USING status::text;
ALTER TABLE billing.wallets ALTER COLUMN status SET DEFAULT 'ACTIVE';

ALTER TABLE billing.transactions ALTER COLUMN tx_type TYPE VARCHAR(32) USING tx_type::text;
ALTER TABLE billing.transactions ALTER COLUMN service_type TYPE VARCHAR(32) USING service_type::text;

ALTER TABLE billing.prices ALTER COLUMN service_type TYPE VARCHAR(32) USING service_type::text;
ALTER TABLE billing.prices ALTER COLUMN metric_type TYPE VARCHAR(32) USING metric_type::text;
ALTER TABLE billing.prices ALTER COLUMN unit DROP DEFAULT;
ALTER TABLE billing.prices ALTER COLUMN unit TYPE VARCHAR(32) USING unit::text;
ALTER TABLE billing.prices ALTER COLUMN unit SET DEFAULT 'GB_HOUR';
ALTER TABLE billing.prices ALTER COLUMN tier DROP DEFAULT;
ALTER TABLE billing.prices ALTER COLUMN tier TYPE VARCHAR(32) USING tier::text;
ALTER TABLE billing.prices ALTER COLUMN tier SET DEFAULT 'STANDARD';

ALTER TABLE billing.plans ALTER COLUMN service_type TYPE VARCHAR(32) USING service_type::text;
ALTER TABLE billing.plans ALTER COLUMN status DROP DEFAULT;
ALTER TABLE billing.plans ALTER COLUMN status TYPE VARCHAR(16) USING status::text;
ALTER TABLE billing.plans ALTER COLUMN status SET DEFAULT 'ACTIVE';

ALTER TABLE billing.plan_metrics ALTER COLUMN metric_type TYPE VARCHAR(32) USING metric_type::text;
ALTER TABLE billing.plan_metrics ALTER COLUMN unit TYPE VARCHAR(32) USING unit::text;

ALTER TABLE billing.subscriptions ALTER COLUMN owner_type TYPE VARCHAR(32) USING owner_type::text;
ALTER TABLE billing.subscriptions ALTER COLUMN status DROP DEFAULT;
ALTER TABLE billing.subscriptions ALTER COLUMN status TYPE VARCHAR(16) USING status::text;
ALTER TABLE billing.subscriptions ALTER COLUMN status SET DEFAULT 'ACTIVE';

-- Drop ENUM types
DROP TYPE IF EXISTS billing.sub_status;
DROP TYPE IF EXISTS billing.plan_status;
DROP TYPE IF EXISTS billing.tier_type;
DROP TYPE IF EXISTS billing.unit_type;
DROP TYPE IF EXISTS billing.metric_type;
DROP TYPE IF EXISTS billing.service_type;
DROP TYPE IF EXISTS billing.tx_type;
DROP TYPE IF EXISTS billing.wallet_status;
DROP TYPE IF EXISTS billing.owner_type;
