-- A resource/source version identifies exactly one immutable ownership event.
-- Existing conflicting rows are an integrity incident and intentionally block
-- migration instead of being deleted or silently selected.
CREATE UNIQUE INDEX IF NOT EXISTS uq_ownership_inbox_resource_version
    ON billing.ownership_event_inbox (resource_id, source_version);
