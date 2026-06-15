-- IAM migration layer 000008 down migration

DROP INDEX IF EXISTS idx_iam_outbox_pending;
DROP TABLE IF EXISTS iam_outbox_records;
