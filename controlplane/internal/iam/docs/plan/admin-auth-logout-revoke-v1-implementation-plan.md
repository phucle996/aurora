# Admin Auth Logout/Revoke Flow V1 - Implementation Plan

## 1) Mục tiêu triển khai

Triển khai logout admin flow V1 theo spec `admin-auth-logout-revoke-flow-temp-spec.md` với mục tiêu:
- bảo vệ endpoint logout bằng runtime auth middleware,
- cleanup runtime secret theo `device_id` ở server,
- clear đầy đủ session fragments ở client cookie,
- đồng bộ semantics với session short-lived 15 phút + refresh contract.

Done definition:
- `POST /admin/auth/logout` trả đúng semantic `204` khi success,
- runtime secret cleanup + cookie clear đúng policy,
- error mapping rõ cho các nhánh middleware/service.

Out-of-scope:
- global revoke/multi-device revoke,
- incident response batch revoke,
- rotation lifecycle.

Cross-link plans:
- Login plan: `controlplane/internal/iam/docs/plan/admin-auth-login-v1-implementation-plan.md`
- Renew plan: `controlplane/internal/iam/docs/plan/admin-auth-renew-v1-implementation-plan.md`

---

## 2) Current state vs target state

**Current state**
- Logout handler/service/route đã tồn tại và chạy được.
- Chưa có plan chuẩn hóa theo `plan-docs` cho logout/revoke scope.

**Target state**
- Có implementation plan decision-complete cho logout/revoke V1.
- Scope logout/revoke tách riêng khỏi login plan.
- Test/acceptance rõ ràng để merge và vận hành.

---

## 3) Implementation changes (grouped by subsystem)

### Handler
**Files (SỬA)**
- `internal/iam/transport/http/handler/admin_auth_handler.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `Logout(c *gin.Context)` | SỬA | Logout xử lý cơ bản, chưa chuẩn hóa đầy đủ decision notes trong plan | Chốt rõ: đọc `device_id` cookie, gọi `AdminLogout`, clear 3 cookies, trả `204`, map fail `500` | Logout behavior rõ ràng và nhất quán với spec |

### Service
**Files (SỬA)**
- `internal/iam/service/admin_api_key_service.go`
- `internal/iam/domain/service/admin_api_key_service.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `AdminLogout(ctx, deviceID)` | SỬA | Cleanup semantics hiện hữu nhưng chưa được chốt đầy đủ trong plan | Chốt policy: `device_id` rỗng -> no-op; có `device_id` -> `DeleteDeviceSecret` | Tránh false-fail khi thiếu cookie; cleanup rõ side effect |
| `AdminLogout(ctx, deviceID)` | SỬA | Logout chỉ cleanup runtime record | Finalize tracking DB một lần trước khi xóa runtime Redis record | Giảm write DB liên tục ở refresh, vẫn giữ lịch sử cuối phiên |

### Repo
**Files (KHÔNG ĐỔI)**
- Không có thay đổi repo trong scope logout V1.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | XÓA | N/A | N/A | Logout V1 không cần DB mutation/read |

### Middleware
**Files (SỬA)**
- `internal/http/middleware/admin_api_key_auth.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `AdminAPIKeyAuth(...)` (áp dụng cho route logout) | SỬA | Guard logic đã có, chưa được ràng buộc logout plan rõ | Chốt logout MUST đi qua middleware verify đủ 3 fragments | Logout không thể gọi khi session runtime không hợp lệ |

### Cache
**Files (SỬA)**
- `internal/iam/cache/admin_device_runtime_cache.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `DeleteDeviceSecret(ctx, deviceID)` | SỬA | Delete semantics đã có, chưa được chốt thành contract logout trong plan | Chốt runtime cleanup duy nhất của logout V1: xóa secret theo `device_id` | Thu hồi mảnh thứ 3 server-side cho session hiện tại |

### Route
**Files (SỬA)**
- `internal/iam/route.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Route register `/admin/auth/logout` | SỬA | Route đã tồn tại | Chốt chain: rate limit + `AdminAPIKeyAuth` + `AdminAuthHandler.Logout` | Enforce auth boundary trước khi logout |

### Docs
**Files (SỬA + THÊM)**
- `internal/iam/docs/spec/admin-auth-logout-revoke-flow-temp-spec.md` (**SỬA**)
- `internal/iam/docs/plan/admin-auth-logout-revoke-v1-implementation-plan.md` (**THÊM**)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A (doc sync - spec) | SỬA | Spec logout/revoke mới tạo nhưng chưa khóa bằng execution blueprint theo plan-docs | Spec + plan đồng bộ cùng scope logout/revoke V1, tách khỏi login flow | Tránh scope drift, review và quyết định code dễ hơn |

### Tests
**Files (SỬA)**
- `internal/iam/test/transport_test/admin_auth_handler_test.go`
- `internal/http/middleware/test/admin_api_key_auth_test.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Transport tests logout | SỬA | Có test cơ bản | Chốt case: success 204 + clear 3 cookie + service error -> 500 | Khóa HTTP contract logout |
| Middleware auth tests | SỬA | Có test runtime auth | Chốt nhánh 401/503 liên quan logout guard dependency | Khóa failure semantics của guard |

### Deletions in this scope
- Không có file/function **XÓA** thực tế trong code scope logout V1.

---

## 4) Contract changes

- Endpoint:
  - `POST /admin/auth/logout`
- Request:
  - Không yêu cầu body; dựa trên cookie fragments đã được middleware verify.
- Response:
  - success: `204 No Content`
  - failure: `401`/`503` (middleware), `500` (handler/service cleanup fail)
- Service contract:
  - `AdminLogout(ctx, deviceID string) error`

No public contract change ngoài phạm vi logout endpoint nêu trên.

---

## 5) Test plan + acceptance

### Required tests
- Happy path:
  - logout success trả `204` và clear đủ 3 cookies.
- Error path:
  - middleware reject khi thiếu/invalid fragments (`401`),
  - middleware dependency failure (`503`),
  - service cleanup failure (`500`).
- Edge path:
  - `device_id` rỗng -> no-op cleanup nhưng vẫn clear cookie.

### Acceptance checklist
- [ ] Logout route bắt buộc qua `AdminAPIKeyAuth`.
- [ ] Success path trả `204` và clear đủ `admin_api_token/device_id/device_secret`.
- [ ] Success path xóa runtime secret theo `device_id`.
- [ ] Success path finalize tracking DB một lần trước cleanup runtime.
- [ ] Sau logout, gọi `POST /admin/auth/refresh` với fragments cũ phải trả `401`.
- [ ] Middleware invalid session trả `401` generic.
- [ ] Middleware dependency failure trả `503`.
- [ ] Service cleanup fail trả `500`.
- [ ] Không log plaintext token/device_secret.

---

## 6) Rollout & operations

- Enable path: deploy IAM module với route `/admin/auth/logout` và middleware chain đã wire.
- Required dependencies:
  - secret provider + Redis cho middleware verify,
  - Redis cho cleanup runtime secret.
- Fallback behavior:
  - dependency security-critical fail -> deny request (`401/503` theo path).
- Monitoring/log signals:
  - logout success/fail rate,
  - `401/503/500` distribution trên `/admin/auth/logout`,
  - Redis error rate path verify + delete.

---

## 7) Risk & mitigation

- Risk: Redis outage làm logout bị deny/fail.
  - Mitigation: fail-closed policy rõ + runbook Redis outage + alert Redis health.

- Risk: cookie clear không đồng bộ policy domain/path khiến session chưa sạch phía client.
  - Mitigation: chốt cookie clear attributes đồng nhất với cookie set policy.

- Risk: scope drift lẫn revoke/global revoke vào logout V1.
  - Mitigation: giữ out-of-scope rõ, tách plan/spec revoke riêng.

- Risk: mismatch giữa middleware failure semantics và docs.
  - Mitigation: test 401/503 path bắt buộc + sync spec/runbook.
