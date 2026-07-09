ALTER TABLE iam.devices ADD COLUMN IF NOT EXISTS trusted_at timestamptz NULL;
ALTER TABLE iam.devices ADD COLUMN IF NOT EXISTS quarantined_at timestamptz NULL;
ALTER TABLE iam.devices ADD COLUMN IF NOT EXISTS status varchar(64) NOT NULL DEFAULT 'new';
