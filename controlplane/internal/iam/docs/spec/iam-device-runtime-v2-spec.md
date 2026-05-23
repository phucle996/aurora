# IAM Device Runtime v2 Spec

Owner: iam team
Approver: system-architecture
Effective date: 2026-05-19
Next review: 2026-08-19
Version: v2 (replaces v1 device tracking presence model)

## Goals
- Định nghĩa Source-of-Truth (SoT) presence và device binding cho luồng user (mirror admin flow).
- Loại bỏ việc suy presence từ refresh_token lifecycle.
- Đảm bảo độ an toàn khi CCU cao: rotate-per-jti, không lộ DB id, atomic update.

## Identifiers
- `tracking_id`: stable cho 1 phiên runtime dài hạn của 1 thiết bị; KHÔNG đổi theo token.
- `device_id` (fragment): random opaque, ROTATE mỗi access `jti`.
- `device_secret`: random opaque, ROTATE mỗi access `jti`. Server chỉ lưu hash.
- `jti`: rotate mỗi access token issue/refresh.
- `tracked_device_ref`: `iam.devices.id` (DB), server-only. KHÔNG expose ra client/Redis key.

## Source of Truth
- Identity registry: `iam.devices` (DB).
- Presence + token-binding: Redis runtime hash, key:
  - `iam:user:device:runtime:<tracking_id>`
- Refresh token store: `iam.refresh_tokens` (DB) gắn `device_id = tracked_device_ref`.

## Redis Hash Contract
Key: `iam:user:device:runtime:<tracking_id>`
Fields:
- `tracking_id` (stable)
- `device_id` (current fragment)
- `device_secret_hash`
- `current_jti`
- `tracked_device_ref` (server-only)
- `user_id`
- `last_seen_at`, `last_seen_ip`, `last_seen_user_agent`
- `status`: `online` | `pending` | `revoked`
- `version` (monotonic int, dùng cho compare-and-set)
- `last_seen_dirty` (dùng để flush DB lazily)

TTL:
- 90s–180s (heartbeat-driven). Refresh tại login/refresh/heartbeat.

## Cookies
- `device_id`: opaque fragment, HttpOnly=false, Secure, SameSite=Lax.
- `device_secret`: HttpOnly=true, Secure, SameSite=Lax.
- `access_token`: HttpOnly=true.
- `refresh_token`: HttpOnly=true.

## JWT Claims
- `sub` = user_id
- `jti`
- `tracking_id`
- KHÔNG nhúng DB id hay device_secret.
- (Tuỳ chọn) `device_id` claim chỉ làm reference runtime; verify SoT vẫn từ Redis.

## Rotation Flow (per access jti)
1. Client gọi login/refresh.
2. Server giữ nguyên `tracking_id`, sinh `jti`, fragment `device_id`, `device_secret`.
3. Server compute `device_secret_hash`.
4. Atomic update Redis (Lua/MULTI):
   - assert old `current_jti` (nếu có) == expected.
   - update fields: `device_id`, `device_secret_hash`, `current_jti`, `last_seen_at`, `version+=1`.
   - reset TTL.
5. Set cookies mới (`device_id`, `device_secret`).
6. Issue access JWT với `tracking_id`, `jti` mới.
7. Grace window: chấp nhận old `jti` trong vài giây (configurable, default 10s) cho in-flight requests.

## Verify Path (middleware)
- Đọc cookie `device_id`, `device_secret`.
- Đọc claims: `tracking_id`, `jti`.
- Lookup Redis runtime by `tracking_id`.
- Pass khi:
  - `device_id` cookie == runtime.device_id.
  - hash(`device_secret`) == runtime.device_secret_hash.
  - claims.jti == runtime.current_jti (hoặc trong grace window).
  - status != `revoked`.
- Fail: 401, DEL key, clear cookies (`device_id`, `device_secret`).

## Heartbeat
- Endpoint `POST /api/v1/me/devices/heartbeat` (P1, optional batch 3).
- Touch TTL + cập nhật `last_seen_*`.
- Idempotent, không tốn DB write.

## Logout / Revoke
- Logout phiên: DEL `iam:user:device:runtime:<tracking_id>` + revoke refresh token tracked_device_ref.
- Revoke 1 device khác: tìm các tracking_id map về tracked_device_ref → DEL.
- Logout-others / Logout-all: lọc theo user_id + DEL theo danh sách runtime.
- Audit: ghi `actor`, `action`, `target_device_ref`, `tracking_id`, `reason`.

## Concurrency / CCU
- Atomic: dùng Lua script cho rotate, compare-and-set theo `version`.
- Cache-first lookup khi verify, giảm hit DB.
- Rate-limit: per user, per ip, per tracking_id.
- Jitter refresh window cho expiry.
- Audit async qua redis stream.

## Failure Modes
- Redis down: degrade về deny-by-default; KHÔNG fallback về suy state từ refresh token.
- DB down: login từ chối; refresh đang valid runtime vẫn pass nếu Redis còn.
- Stale state: TTL ngắn đảm bảo tự dọn sau 180s offline.

## Migration plan (v1 → v2)
1. Triển khai cache + middleware mới (chưa enforce).
2. Login/refresh ghi đồng thời v1 lifecycle + v2 runtime.
3. Đổi presence read sang v2.
4. Bật enforcement middleware verify cookie+jti+secret.
5. Rút phụ thuộc presence khỏi refresh_token, cleanup v1.

## API Impact
- `GET /api/v1/me/devices`: list từ DB + merge online từ Redis (theo tracked_device_ref).
- `POST /api/v1/me/devices/:device_id/revoke`: revoke theo `tracked_device_ref`, DEL runtime.
- `POST /api/v1/me/devices/logout-others|logout-all`: chuẩn hóa cùng cơ chế.

## Testing
- Unit: cache module (rotate atomic, ttl, verify match/mismatch).
- Integration: login → refresh → presence online → logout → presence offline.
- Race: concurrent refresh vs revoke; rotate vs verify in-flight grace.
- k6: login burst, refresh storm, redis degradation, fanout revoke.
