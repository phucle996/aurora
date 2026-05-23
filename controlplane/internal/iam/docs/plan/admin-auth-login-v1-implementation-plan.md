# Admin Auth Login Flow V1 - Implementation Plan

## 1) Mục tiêu triển khai

Triển khai login admin flow V1 theo spec `admin-auth-login-flow-v1-spec.md` với mục tiêu:
- xác thực `admin_api_key + MFA (totp|recovery_code) + device_public_key`,
- cấp runtime fragments (`admin_api_token`, `device_id`, `device_secret`) qua cookie,
- bảo đảm login path fail-safe với cleanup phù hợp khi lỗi giữa chừng.
- đồng bộ runtime TTL baseline 15 phút + response header `X-Session-Expires-In`.

Done definition:
- login route hoạt động đúng contract,
- service/repo/cache xử lý đầy đủ nhánh chính và nhánh lỗi,
- test path bắt buộc pass.

Out-of-scope:
- logout/revoke lifecycle,
- runtime `/admin` middleware auth chain,
- critical signature/step-up guard,
- incident-response/rotation lifecycle.

Cross-link plans:
- Renew plan: `controlplane/internal/iam/docs/plan/admin-auth-renew-v1-implementation-plan.md`
- Logout/Revoke plan: `controlplane/internal/iam/docs/plan/admin-auth-logout-revoke-v1-implementation-plan.md`

---

## 2) Current state vs target state

**Current state**
- Login admin flow đã có phần lớn implementation, nhưng plan cũ trộn scope login với logout/middleware/critical guard.
- Một số section chưa đồng bộ naming/contract mới sau khi tách spec/flow.

**Target state**
- Plan chỉ còn scope login flow và là nguồn triển khai decision-complete.
- Contract và implementation boundaries khớp với codebase hiện tại.
- Acceptance và rollout notes phản ánh đúng phạm vi login-only.

---

## 3) Implementation changes (grouped by subsystem)

> Template thống nhất cho từng function: `Function | Change | Before | After | Impact`.

### Handler
**Files (SỬA)**
- `internal/iam/transport/http/handler/admin_auth_handler.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `Login(c *gin.Context)` | SỬA | Bind/map login chưa chuẩn hóa theo login-only scope | Bind DTO, normalize input, gọi `AdminLogin`, set 3 cookies runtime, map lỗi generic | Boundary transport rõ, không leak lỗi nội bộ/secret |
| `Login(c *gin.Context)` | SỬA | Chưa có contract metadata session-expiry cho FE | Trả header `X-Session-Expires-In` theo TTL runtime còn lại | FE schedule refresh mà không cần đọc cookie HttpOnly |

### Service
**Files (SỬA)**
- `internal/iam/service/admin_api_key_service.go`
- `internal/iam/domain/service/admin_api_key_service.go`
- `internal/iam/domain/entity/admin.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `AdminLogin(ctx, req)` | SỬA | Login steps chưa chốt đầy đủ theo contract hiện tại | Validate input, verify key+MFA, issue fragments/JWT, persist runtime secret, upsert device binding | Login flow nhất quán theo spec, handler chỉ còn mapping |
| `AdminLogin(ctx, req)` | SỬA | TTL runtime policy chưa chốt rõ theo bản spec mới | Issue runtime token/secret theo baseline 15 phút | Đồng bộ contract login/renew/logout |
| `normalizeDevicePublicKey(raw)` | THÊM | Chưa có chuẩn hóa key tập trung | Validate ed25519 public key + canonicalize base64 | Loại key rác sớm, fingerprint nhất quán |
| `loadActiveAdminAPIKey(ctx)` | SỬA | Read active key không tối ưu cache policy | Cache RAM TTL 5m + fallback DB, TTL không vượt `expires_at` | Giảm DB call burst login, vẫn giữ correctness expiry |
| `loadAdminTOTPSecret(ctx)` | SỬA | Decrypt/read lặp lại nhiều lần | Cache RAM TTL 5m keyed theo `updated_at` + fallback DB | Giảm decrypt/DB overhead ở nhánh TOTP |

### Repo
**Files (SỬA)**
- `internal/iam/domain/repo/admin_api_key_repo.go`
- `internal/iam/repository/admin_api_key_repo.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `GetActiveAdminAPIKey(ctx)` | SỬA | Truyền `now` từ service | DB tự check `expires_at > CURRENT_TIMESTAMP` | Đơn giản contract, giảm lệch app clock |
| `GetAdmin2FASettings(ctx)` | SỬA | Chưa chốt rõ query source cho TOTP | Query secret ciphertext active từ DB | Service decrypt/cache theo source-of-truth |
| `ConsumeRecoveryCode(ctx, codeHash, now)` | SỬA | One-time semantics chưa mô tả đầy đủ trong plan | Update `used_at` atomically khi chưa used | Đảm bảo one-time consume, kết hợp lock Redis |
| `UpsertAdminDeviceBinding(ctx, input)` | SỬA | Device binding semantics mô tả chưa rõ | Upsert binding với public key canonical + fingerprint | Persist đủ dữ liệu cho runtime/critical verify về sau |

### Cache
**Files (SỬA)**
- `internal/iam/cache/admin_device_runtime_cache.go`
- `internal/iam/cache/admin_login_lock_cache.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `SetDeviceSecret(...)` | SỬA | Runtime fragment write semantics chưa chốt rõ | Ghi `device_id -> device_secret_hash` với TTL token | Login chỉ pass khi runtime fragment persisted |
| `VerifyDeviceSecret(...)` | SỬA | Verify behavior chưa mô tả rõ trong plan login | Verify hash runtime fragment theo `device_id` | Đồng bộ semantics với auth middleware |
| `DeleteDeviceSecret(...)` | SỬA | Cleanup semantics mô tả mơ hồ | Dùng cho compensation khi login fail giữa chừng | Tránh để lại runtime secret rác |
| `AcquireRecoveryConsumeLock(...)` | SỬA | Race control chưa chuẩn hóa ownership | `SET NX` + owner-token unlock | Ngăn concurrent consume cùng recovery code |

### Route
**Files (SỬA)**
- `internal/iam/route.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Route register `/admin/auth/login` | SỬA | Scope route trong plan còn lẫn flow khác | Chỉ giữ login route + bucket rate limit login | Tránh scope drift sang logout/critical |

### Docs
**Files (SỬA)**
- `docs/spec/admin-auth-login-flow-v1-spec.md`
- `docs/flow/admin-login-flow.md`
- `docs/plan/admin-auth-login-v1-implementation-plan.md`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A (doc sync) | SỬA | Plan/spec/flow có đoạn lệch scope login-only | Đồng bộ login-only + cross-link rõ | Reviewer có 1 source-of-truth trước code |

### Tests
**Files (SỬA)**
- `internal/iam/test/svc_test/*`
- `internal/iam/test/repo_test/*`
- `internal/iam/test/transport_test/*`
- `internal/iam/test/integration_test/*`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Service tests | SỬA | Chưa cover đủ login main/error/edge | Bổ sung verify credential/MFA/cache/cleanup | Khóa business behavior trước merge |
| Repo tests | SỬA | Chưa chốt đầy đủ semantics active/recovery/binding | Bổ sung cases one-time consume + upsert binding | Khóa contract persistence |
| Transport tests | SỬA | Mapping/login cookie contract chưa đủ rõ | Bổ sung bind error + unauthorized + cookie set cases | Khóa HTTP contract |
| Integration tests | SỬA | E2E login cases chưa tách rõ login scope | Bổ sung login path integration module-level | Xác nhận wiring liên tầng đúng |

### Deletions in this scope
- Không có file/function **XÓA** trong scope login V1 hiện tại.

---
## 4) Contract changes

- Endpoint:
  - `POST /admin/auth/login`
- Request DTO:
  - `admin_api_key`, `mfa_method`, `mfa_code`, `device_public_key`
- Service contract:
  - `AdminLogin(ctx, req AdminLoginRequest) (AdminLoginResult, error)`
- Entity contract:
  - `AdminLoginRequest`
  - `AdminLoginResult`
- Repo contract:
  - `GetActiveAdminAPIKey(ctx)` (không truyền `now` từ service)
  - `GetAdmin2FASettings`
  - `ConsumeRecoveryCode`
  - `UpsertAdminDeviceBinding`

No public contract change ngoài phạm vi login endpoint nêu trên.

---

## 5) Test plan + acceptance

### Required tests
- Happy path:
  - login success với `totp` hợp lệ,
  - login success với `recovery_code` hợp lệ.
- Error path:
  - API key invalid/expired,
  - TOTP invalid,
  - recovery code invalid/used,
  - Redis runtime set fail,
  - DB device binding fail sau runtime set (phải cleanup secret).
- Edge path:
  - concurrent recovery consume (lock semantics),
  - canonicalized public key input variants (std/raw base64).

### Acceptance checklist
- [ ] Login yêu cầu đủ `admin_api_key + mfa + device_public_key`.
- [ ] Login success set đủ 3 cookie runtime (`admin_api_token`, `device_id`, `device_secret`).
- [ ] Login success trả header `X-Session-Expires-In` hợp lệ (đơn vị giây).
- [ ] Không log plaintext secret/token/code.
- [ ] Recovery code one-time semantics pass dưới concurrent requests.
- [ ] Active key/TOTP cache fallback DB hoạt động đúng.
- [ ] Test suite `internal/iam` và transport liên quan pass.

---

## 6) Rollout & operations

- Enable path: deploy module IAM với route `/admin/auth/login` đã wire đầy đủ deps.
- Required dependencies:
  - PostgreSQL (admin key/2FA/recovery/devices),
  - Redis (runtime fragment + recovery lock),
  - secret provider để sign JWT.
- Fallback behavior:
  - cache miss -> DB fallback,
  - dependency security-critical fail -> deny login theo error mapping hiện hành.
- Monitoring signals sau deploy:
  - login success/fail rate,
  - latency p95 login route,
  - Redis error rate,
  - DB query error rate cho `admin_api_keys`, `admin_2fa_settings`, `admin_recovery_codes`, `devices`.

---

## 7) Risk & mitigation

- Risk: Redis unavailable làm login fail.
  - Mitigation: fail-closed policy rõ ràng + runbook outage + alert Redis health.

- Risk: race consume recovery code khi burst login.
  - Mitigation: Redis lock owner-token + DB one-time consume assertion.

- Risk: key format không chuẩn gây lỗi verify critical về sau.
  - Mitigation: canonicalize + validate `device_public_key` ngay tại login service.

- Risk: stale local cache trên multi-replica.
  - Mitigation: TTL ngắn 5m + DB fallback + chốt TTL-only by design trong docs.

- Risk: scope drift (trộn login với logout/critical plans).
  - Mitigation: giữ plan này login-only, cross-link sang plan/spec khác cho logout/critical.
