# Admin API Guard & Critical Actions Flow

## 1) Mục tiêu

Tài liệu này mô tả cách server guard request vào `/admin` theo 2 mức:
- Admin runtime auth guard (token fragmentation),
- Critical action guard (request signing + step-up 2FA).

---

## 2) Guard chain

## 2.1 Admin runtime routes

Mọi route admin cần runtime auth phải đi qua `AdminAPIKeyAuth`:

1. Đọc cookies:
   - `admin_api_token`,
   - `device_id`,
   - `device_secret`.
2. Verify JWT `admin_api_token` bằng secret family `admin_api_key`.
3. Check claim `device_id` trong JWT phải khớp cookie `device_id`.
4. Verify `device_secret` qua Redis runtime hash check.
5. Pass thì request mới được coi là authenticated admin runtime.

Nếu fail bất kỳ bước nào -> `401` generic.

---

## 2.2 Critical admin actions

Critical routes cần thêm 2 lớp guard:

1. `AdminCriticalActionSignatureGuard`
2. `AdminCriticalActionStepUp2FA`

Ví dụ route critical hiện hành:
- `POST /admin/auth/rotate-key`
  - `RateLimitPreAuth("iam_admin_auth_rotate_key", 5 req/min)`
  - `AdminCIDR(SECURITY_ADMIN_ALLOWED_CIDRS)`
  - `AdminAPIKeyAuth`
  - `AdminCriticalActionSignatureGuard(nonceTTL=1m, skew=2m)`
  - `AdminCriticalActionStepUp2FA`
  - `AdminAuthHandler.RotateKey`

### A) `AdminCriticalActionSignatureGuard`

Yêu cầu headers:
- `X-Admin-Signature`
- `X-Admin-Timestamp`
- `X-Admin-Nonce`

Flow:
1. Validate timestamp trong cửa sổ skew cho phép.
2. Chống replay nonce bằng Redis `SETNX` (nonce TTL).
3. Resolve device binding theo `device_id`:
   - ưu tiên RAM cache cục bộ theo `device_id -> {public_key}` TTL 5 phút,
   - cache miss/fault => fallback DB `GetAdminDeviceByID` rồi set lại cache.
4. Build canonical payload:
   - `method + path + query + body_hash + timestamp + nonce + device_id`
5. Verify chữ ký bằng `device_public_key` (ed25519).

Lưu ý: admin flow theo zero-trust, không dùng trust-device status làm gate cho critical action.

Cache policy chốt cho V1: `TTL-only by design`.
- Mỗi replica giữ RAM cache cục bộ cho critical device binding (TTL 5 phút).
- Không triển khai invalidate liên instance theo event trong V1.
- Khi cache miss/expired luôn fallback DB.

Fail ở bất kỳ bước nào -> `401` generic.
Lỗi hạ tầng Redis bắt buộc (nonce lock) -> `503` generic.

### B) `AdminCriticalActionStepUp2FA`

Middleware này trong V1 áp dụng policy **totp-only** cho critical actions.

Yêu cầu headers:
- `X-Admin-StepUp-Method`: `totp`
- `X-Admin-StepUp-Code`

Flow:
- `totp`:
  - đọc `admin_2fa_settings`, decrypt secret,
  - verify TOTP.

Ghi chú policy:
- `recovery_code` **không** dùng cho critical actions.
- `recovery_code` chỉ hợp lệ trong luồng admin login/recovery.

Fail verify -> `401` generic.
Lỗi đọc/decrypt secret -> `401` generic.

---

## 3) Data/Cache responsibility

- DB:
  - `admin_api_keys`: API key active + expiry,
  - `admin_2fa_settings`: TOTP secret ciphertext,
  - `admin_recovery_codes`: one-time consume,
- `devices`: public key binding cho verify chữ ký critical action.

- Redis:
  - runtime fragment: `device_id -> device_secret_hash` (TTL session),
  - recovery consume lock,
  - critical nonce replay lock,
  - critical device binding cache TTL 5 phút (fallback DB).

---

## 4) Trách nhiệm bảo mật

- Handler là boundary logging; service/repo không log nghiệp vụ.
- Không log plaintext secrets/tokens/codes.
- Tất cả lỗi auth trả generic để tránh lộ nội bộ.
- Critical route chỉ pass khi đủ 3 lớp:
  - runtime session valid,
  - signature valid,
  - step-up 2FA valid.
