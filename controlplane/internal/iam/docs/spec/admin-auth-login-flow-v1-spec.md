# Admin Auth Login Flow V1 - Specification

## 1) Purpose + Scope

### Purpose
Định nghĩa behavior source-of-truth cho login admin flow V1 trên kênh `/admin`, bao gồm:
- xác thực `admin_api_key + MFA (totp|recovery_code)`,
- cấp runtime fragments (`admin_api_token`, `device_id`, `device_secret`),
- ràng buộc session admin theo mô hình fragment + middleware chain.

### In-scope
- `POST /admin/auth/login` contract và semantics.
- Runtime fragment issuance và persistence rules.
- Login-related dependency behaviors (DB/Redis/cache).
- Login-related security/failure semantics.

### Out-of-scope
- Logout/revoke lifecycle chi tiết (spec riêng).
- Incident-response/rotation lifecycle (spec riêng).
- UI/UX vận hành.

---

## 2) Terminology / Actors

### Actors
- **Admin Client**: gọi endpoint login admin.
- **Auth Handler**: transport boundary, bind/map lỗi/set cookie.
- **Auth Service**: business verification + token/fragment issuance.
- **Repository/DB**: source-of-truth dữ liệu admin auth.
- **Redis**: runtime store và lock cho security-critical operations.

### Terms
- **Admin API Key**: credential admin dạng plaintext input, verify bằng hash trong DB.
- **Runtime fragments**: `admin_api_token`, `device_id`, `device_secret`.
- **Step-up 2FA**: xác thực bổ sung cho critical action routes (thuộc flow khác, nhưng phụ thuộc kết quả login).

Phân tách bắt buộc:
- `admin_api_key` là login credential (bootstrap/rotation cấp, thường qua Telegram nội bộ), không phải runtime session token.
- `admin_api_token` là JWT runtime session, chỉ có hiệu lực theo phiên và có thể thay đổi ở mỗi lần login mới.

---

## 3) API Contract

### Endpoint
- `POST /admin/auth/login`

### Request
```json
{
  "admin_api_key": "<plaintext-admin-api-key>",
  "mfa_method": "totp|recovery_code",
  "mfa_code": "<totp-or-recovery-code>",
  "device_public_key": "<base64-ed25519-public-key>"
}
```

### Request semantics
- `admin_api_key` bắt buộc, verify bằng hash + active key policy.
- `mfa_method` bắt buộc: `totp` hoặc `recovery_code`.
- `mfa_code` bắt buộc theo method tương ứng.
- `device_public_key` bắt buộc; phải hợp lệ theo policy key format của hệ thống.

### Success response
- `200 OK`
- Body:
```json
{ "ok": true }
```
- Body contract MUST giữ tối giản: chỉ trả `{ "ok": true }`, không trả token/session data trong body.
- Response header:
  - `X-Session-Expires-In: <seconds>`
  - Giá trị là số giây còn lại của admin runtime session vừa được issue.
- Set cookies runtime:
  - `admin_api_token`
  - `device_id`
  - `device_secret`

### Error response semantics
- `400`: invalid request shape / invalid argument.
- `401`: invalid credential hoặc MFA invalid.
- `429`: rate limit.
- `5xx`: infrastructure/runtime failure.

---

## 4) Flow Behavior

### Main flow
1. Handler nhận request, validate/normalize input.
2. Service verify active admin key + hash match.
3. Service verify MFA theo method:
   - `totp`: verify code với secret active,
   - `recovery_code`: lock + consume one-time.
4. Service generate runtime fragments + admin JWT.
5. Service persist runtime secret hash theo `device_id` (TTL runtime).
6. Service upsert device public key binding.
7. Handler set 3 cookies runtime và trả `200`.

### Error/failure branches
- Invalid input -> `400`, không tạo session.
- Invalid credential/MFA -> `401`, không tạo session.
- Redis/DB/security dependency fail ở bước critical -> deny theo failure semantics.
- Nếu fail sau khi đã persist một phần runtime state -> phải cleanup theo policy compensation.

### Preconditions
- DB và Redis khả dụng theo mức tối thiểu cho login flow.
- Secret provider khả dụng để sign token.

### Postconditions
- Login success: session fragments được cấp đầy đủ.
- Login fail: không để lại session ở trạng thái nửa chừng.
- Sau logout/login lại, runtime fragments mới (`admin_api_token`, `device_id`, `device_secret`) được cấp lại; bộ fragments cũ không còn hợp lệ cho runtime access.
- Việc runtime fragments đổi theo phiên không tự động làm invalid `admin_api_key`; `admin_api_key` chỉ fail khi expired/revoked/rotated hoặc nhập sai.

### State transitions
- `Unauthenticated -> LoginRequested -> Verified -> SessionIssued -> Authenticated`
- `LoginRequested -> Rejected` cho mọi nhánh fail.

---

## 5) Data & Boundary Rules

### Source-of-truth
- DB:
  - `admin_api_keys`: active key + expiry.
  - `admin_2fa_settings`: TOTP secret ciphertext.
  - `admin_recovery_codes`: one-time consume semantics.
  - `devices`: public key binding metadata.

### Runtime store
- Redis:
  - `device_id -> runtime record` (TTL runtime) gồm tối thiểu: `device_secret_hash`, `tracked_device_id`, `token_jti`, `version`, `last_seen_at`.
  - recovery consume lock keyspace.

Runtime TTL baseline:
- TTL runtime token/cookie mục tiêu mặc định: **15 phút**.
- TTL runtime store của `device_secret` phải đồng bộ với cửa sổ session runtime.

Tracking policy:
- Redis là nguồn realtime theo session.
- DB `admin_devices` dùng cho lịch sử bền vững và chỉ finalize khi session kết thúc.

### Boundary rules
- Redis không thay thế DB cho verify admin key.
- Recovery code phải one-time, consume thành công mới pass MFA branch.
- TTL/expiry phải đồng bộ giữa token và runtime secret policy.

---

## 6) Security Rules

- Admin auth tách biệt user auth flow.
- Không log plaintext `admin_api_key`, `mfa_code`, token, `device_secret`.
- Response lỗi auth phải generic, không leak verify details.
- `device_public_key` bắt buộc và phải hợp lệ theo format policy.
- Recovery code branch phải có race-control lock.

---

## 7) Failure Semantics

- Login dependency security-critical fail => deny login (fail-closed).
- Redis runtime secret write fail => login fail.
- Recovery lock/consume path fail => deny theo error mapping hiện hành.
- DB/source-of-truth read fail ở bước verify => deny theo error mapping hiện hành.
- Retry/backoff policy thực thi ở layer hạ tầng phù hợp; không nới lỏng auth decision semantics.

---

## 8) Non-functional Baseline

- Baseline cho admin login flow:
  - `p95 latency < 800ms`
  - `error rate < 1%`
- Dependencies bắt buộc cho login path:
  - PostgreSQL
  - Redis
  - runtime secret provider
- Cache policy:
  - TTL-only by design,
  - replica-local cache có fallback DB,
  - không event-bus invalidate liên instance trong V1.

---

## 9) Acceptance Criteria

- [ ] Login chỉ thành công khi `admin_api_key + MFA + device_public_key` hợp lệ.
- [ ] Login success trả `200` và set đủ 3 cookie runtime.
- [ ] Login success trả header `X-Session-Expires-In` hợp lệ (đơn vị giây).
- [ ] Invalid input trả `400`.
- [ ] Invalid credential/MFA trả `401` generic.
- [ ] Dependency fail ở bước security-critical không được fail-open.
- [ ] Không log plaintext secret/token/code.
- [ ] Runtime fragment persistence và cleanup behavior đúng policy.
