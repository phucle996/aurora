-- PAYG cutover is intentionally forward-only after financial writes.
-- This down file is a guardrail for migration tooling, not a production
-- rollback: restoring the legacy catalog would make settled lineage unsafe.
DO $$
BEGIN
    RAISE EXCEPTION 'PAYG pricing schedule cutover cannot be rolled back; use a forward migration';
END $$;
