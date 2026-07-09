ALTER TABLE iam.devices DROP COLUMN IF EXISTS trusted_at;
ALTER TABLE iam.devices DROP COLUMN IF EXISTS quarantined_at;
ALTER TABLE iam.devices DROP COLUMN IF EXISTS status;
