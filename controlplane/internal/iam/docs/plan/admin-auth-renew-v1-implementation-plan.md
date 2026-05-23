# Admin Auth Renew Flow V1 - Implementation Plan

## 1) Mục tiêu triển khai

Triển khai renew admin flow V1 theo spec `admin-auth-renew-flow-temp-spec.md` với mục tiêu:
- runtime session short-lived mặc định 15 phút,
- hỗ trợ `POST /admin/auth/refresh` để rolling refresh khi session còn hợp lệ,
- enforce idle-timeout: quá 15 phút không hoạt động thì buộc đăng nhập lại.

Done definition:
- refresh route hoạt động đúng contract,
- response có `X-Session-Expires-In` hợp lệ,
- nhánh idle-timeout/expired-token trả `401` fail-closed.

Out-of-scope:
- thay đổi bootstrap/login credential flow,
- key rotation lifecycle,
- UI scheduling strategy chi tiết.

Cross-link plans:
- Login plan: `controlplane/internal/iam/docs/plan/admin-auth-login-v1-implementation-plan.md`
- Logout/Revoke plan: `controlplane/internal/iam/docs/plan/admin-auth-logout-revoke-v1-implementation-plan.md`

---

## 2) Current state vs target state

**Current state**
- Có login/logout flow và middleware runtime verify.
- Chưa có plan execution chi tiết riêng cho renew 15 phút + header contract.

**Target state**
- Có implementation blueprint decision-complete cho renew flow.
- Login/renew/logout đồng bộ cùng session policy.

---

## 3) Implementation changes (grouped by subsystem)

### Handler
**Files (SỬA)**
- `internal/iam/transport/http/handler/admin_auth_handler.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `Refresh(c *gin.Context)` | THÊM | Chưa có refresh handler | Gọi service refresh, set lại 3 cookies runtime, trả `200` + header `X-Session-Expires-In` | Chuẩn hóa transport contract renew |

Client refresh trigger (implementation note):
- Client đọc `X-Session-Expires-In` (integer, đơn vị giây) từ response.
- Nếu `X-Session-Expires-In <= 300` thì gọi `POST /admin/auth/refresh` để gia hạn session.
- Nếu `X-Session-Expires-In > 300` thì chưa refresh.
- Nếu thiếu header hoặc parse lỗi: fallback timer bảo thủ và check lại.

### Service
**Files (SỬA)**
- `internal/iam/domain/service/admin_api_key_service.go`
- `internal/iam/service/admin_api_key_service.go`
- `internal/iam/domain/entity/admin.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `RefreshAdminSession(ctx, deviceID)` | THÊM | Chưa có API service cho renew | Re-issue `admin_api_token` 15 phút, giữ `device_id/device_secret`, trả expiry còn lại | Gom business renew vào service boundary |
| `AdminLogin(ctx, req)` | SỬA | TTL runtime hiện trạng dài hơn policy mới | Cấp runtime theo baseline 15 phút | Đồng bộ login với renew policy |
| `RefreshAdminSession(ctx, ...)` | SỬA | Refresh path ghi tracking xuống DB theo từng request | Refresh chỉ update runtime tracking trên Redis (`last_seen_at`) + CAS touch TTL/version | Giảm write DB liên tục, vẫn giữ realtime session truth |

### Cache
**Files (SỬA)**
- `internal/iam/cache/admin_device_runtime_cache.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `TouchDeviceSecret(ctx, deviceID, ttl)` | THÊM | Chưa có touch TTL | Gia hạn TTL runtime secret khi refresh hợp lệ | Enforce rolling idle window 15 phút |

### Middleware
**Files (SỬA)**
- `internal/http/middleware/admin_api_key_auth.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `AdminAPIKeyAuth(...)` | SỬA | Verify runtime fragments cho admin routes | Bảo đảm refresh route đi qua cùng guard; fail-closed 401/503 theo contract | Tránh bypass refresh khi session không hợp lệ |

### Route
**Files (SỬA)**
- `internal/iam/route.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Route register `/admin/auth/refresh` | THÊM | Chưa có route renew | Chain: rate limit + `AdminAPIKeyAuth` + `AdminAuthHandler.Refresh` | Khóa boundary runtime auth cho renew |
| Route register `/admin/auth/refresh` | SỬA | Guard chain chưa có device signing | Chain: rate limit + `AdminCIDR` + `AdminAPIKeyAuth` + `AdminCriticalActionSignatureGuard` + `AdminAuthHandler.Refresh` | Enforce critical-action signing cho refresh |

### Config
**Files (SỬA)**
- `internal/config/config.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `Security.AdminSessionTTL` | SỬA | TTL chưa chốt theo spec mới | Baseline mặc định 15 phút | Đồng bộ runtime policy toàn flow |

### Docs
**Files (SỬA + THÊM)**
- `internal/iam/docs/spec/admin-auth-renew-flow-temp-spec.md` (**SỬA**)
- `internal/iam/docs/plan/admin-auth-renew-v1-implementation-plan.md` (**THÊM**)

---

## 4) Contract changes

- Endpoint mới:
  - `POST /admin/auth/refresh`
- Request:
  - không body, dựa trên runtime cookies + middleware verify.
- Response:
  - success: `200 {"ok": true}`
  - header: `X-Session-Expires-In: <seconds>`
  - failure: `401` / `429` / `5xx`

---

## 5) Test plan + acceptance

### Required tests
- Happy path:
  - refresh success trả `200`, set runtime cookies mới, trả header `X-Session-Expires-In`.
- Error path:
  - thiếu/invalid fragments -> `401`,
  - token expired -> `401`,
  - dependency fail (secret provider/Redis) -> `5xx` fail-closed.
- Idle path:
  - runtime secret TTL hết hạn -> `401` và buộc login lại.

Tracking path:
- refresh không gọi DB touch `last_seen_*` theo từng request.
- realtime `last_seen_at` nằm trong Redis runtime record và được finalize về DB khi session kết thúc.

### Acceptance checklist
- [ ] Runtime TTL baseline 15 phút.
- [ ] `/admin/auth/refresh` chỉ pass khi đủ 3 fragments hợp lệ.
- [ ] Refresh success có `X-Session-Expires-In` hợp lệ.
- [ ] Idle timeout >15 phút trả `401`.
- [ ] Không có nhánh fail-open.

---

## 6) Rollout & operations

- Enable path: deploy route refresh + service + middleware wiring.
- Monitoring signals:
  - refresh success/fail rate,
  - phân phối mã `401/429/5xx` trên `/admin/auth/refresh`,
  - Redis errors path verify/touch.

---

## 7) Risk & mitigation

- Risk: đồng bộ TTL giữa token và runtime secret lệch nhau.
  - Mitigation: dùng chung `AdminSessionTTL` cho sign token + cache TTL.

- Risk: client không đọc được cookie HttpOnly để schedule refresh.
  - Mitigation: bắt buộc trả `X-Session-Expires-In`; fallback timer bảo thủ khi thiếu header.

- Risk: refresh bị abuse burst.
  - Mitigation: rate limit route + quan sát metrics spike.
