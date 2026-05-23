-- Partial index hỗ trợ cap-per-user evict (BR-009).
-- Phục vụ ORDER BY last_seen_at DESC NULLS LAST, created_at DESC trên active devices.
CREATE INDEX IF NOT EXISTS devices_user_active_seen_idx
    ON devices (user_id, last_seen_at DESC, created_at DESC)
    WHERE status != 'revoked';
