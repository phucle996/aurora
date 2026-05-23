# Admin Auth Session Bootstrap V1 - Implementation Plan

## 1) Mục tiêu triển khai

Triển khai endpoint session bootstrap ở IAM theo hướng production-safe: backend xác nhận session cho admin channel.
Done definition: có `GET /admin/auth/session` với semantics rõ ràng để UI tiêu thụ.
Không nằm trong scope: thay đổi rotate-key/signature policy, redesign toàn bộ login payload, hoặc thay đổi TTL policy hiện tại.

---

## 2) Current state vs target state

### Current state
- Chưa có endpoint session bootstrap read-only dành riêng cho admin auth probe.
- Các endpoint admin auth hiện hữu tập trung vào login/refresh/logout và critical action flow.

### Target state
- Có endpoint read-only `GET /admin/auth/session` dành riêng cho bootstrap auth state.
- Endpoint trả payload tối giản để client xác định trạng thái auth hợp lệ.
- Flow UI/UX phía frontend được quản lý ở docs UI riêng.

---

## 3) Implementation changes (grouped by subsystem)

### A) Handler

**Files**
- `SỬA` `controlplane/internal/iam/transport/http/handler/admin_auth_handler.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `(*AdminAuthHandler).Session` | THÊM | Chưa có endpoint session probe | Thêm handler `GET /admin/auth/session`, trả payload tối giản `{ "authenticated": true }` khi middleware auth pass | FE có tín hiệu auth rõ ràng để render đúng page |

### B) Service

**Files**
- Không đổi

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | N/A | Endpoint chỉ làm auth probe | Không thêm service logic cho v1 | Giữ scope nhỏ, giảm risk regression |

### C) Repo

**Files**
- Không đổi

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | N/A | Repo hiện tại đã đủ cho auth runtime verify qua middleware/cache | Không thêm DB query cho session probe v1 | Giữ hot-path nhẹ, tránh phát sinh IO không cần thiết |

### D) Middleware

**Files**
- Không đổi

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `AdminAPIKeyAuth` chain cho session route | SỬA (wiring-only) | Chưa dùng cho `/admin/auth/session` | Reuse middleware auth runtime ở route mới, không thêm signature guard | Đảm bảo security parity nhưng không ép FE gửi critical headers |

### E) Cache

**Files**
- Không đổi

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | N/A | Runtime cache đã là source verify cho middleware | Không đổi contract cache trong v1 | Giảm risk regression ở auth runtime layer |

### F) Route

**Files**
- `SỬA` `controlplane/internal/iam/route.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `RegisterRoutes` (`/admin/auth/session`) | THÊM | Không có endpoint session bootstrap | Thêm `GET /admin/auth/session` với chain rate limit + admin auth runtime (không signature guard) | FE có endpoint chuẩn để hydrate auth state |

### G) Docs

**Files**
- `THÊM` `controlplane/internal/iam/docs/spec/admin-auth-session-v1-spec.md`
- `THÊM` `controlplane/internal/iam/docs/plan/admin-auth-session-v1-implementation-plan.md`
- `SỬA` `controlplane/internal/iam/docs/spec/admin-auth-renew-flow-temp-spec.md`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Renew spec session-check notes | SỬA | Renew spec có thể bị hiểu là dùng refresh để bootstrap FE | Ghi rõ refresh là renew endpoint, session bootstrap dùng `/admin/auth/session` | Tránh lệch contract giữa FE và BE |

### H) Tests

**Files**
- `SỬA` `controlplane/internal/http/middleware/test/admin_api_key_auth_test.go`
- `SỬA` `controlplane/internal/iam/test/transport_test/admin_auth_handler_test.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Session bootstrap transport tests | THÊM | Chưa có test cho `/admin/auth/session` | Test `200`, `401`, dependency failure mapping | Khóa contract behavior ngay ở transport boundary |


---

## 4) Contract changes

### Public/API contract changes
- **THÊM endpoint**: `GET /admin/auth/session`.
- **Không đổi** contract public của `POST /admin/auth/login`, `POST /admin/auth/refresh`, `POST /admin/auth/logout`.
- UI integration contract được quản lý ở docs UI riêng (ngoài phạm vi plan IAM này).

### DTO / entity contract
- Không thêm DTO/entity mới; response payload tối giản `{ "authenticated": true }`.

### Error mapping
- Session endpoint map:
  - `401` khi auth runtime invalid,
  - `503/5xx` khi dependency unavailable.
- Response message giữ generic theo `app-error-envelope-canonical-contract.md`.

---

## 5) Test plan + acceptance

### Test plan
- **Happy path**
  - `GET /admin/auth/session` với cookies hợp lệ -> `200` + `authenticated=true`.
- **Error path**
  - Missing/invalid cookie fragments -> `401`.
  - Redis/auth dependency fail -> `503/5xx` generic.
- **Edge path**
  - Request đồng thời trên session probe không làm rò rỉ state hoặc bypass auth.

### Acceptance checklist
- [ ] Endpoint `/admin/auth/session` hoạt động đúng semantics `200/401/5xx`.
- [ ] Không thêm service/repo logic ngoài scope auth probe endpoint.
- [ ] Test IAM transport/service liên quan pass.

---

## 6) Rollout & operations

- **Enable path**: deploy backend endpoint; FE adoption theo release plan của UI team.
- **Disable/fallback path**: nếu rollback FE, endpoint mới có thể giữ lại vì backward-compatible.
- **Config required**: không thêm biến config mới cho v1.
- **Monitoring/log signals**:
  - Tỷ lệ `401` tại `/admin/auth/session`.
  - Tỷ lệ `5xx/503` của session endpoint để phát hiện outage Redis/auth dependency.

---

## 7) Risk & mitigation

1. **Risk**: FE deploy trước BE -> `404` endpoint session.
   - **Mitigation**: deploy BE trước; FE fallback tạm sang unauthenticated + toast generic.
2. **Risk**: Gắn nhầm signature guard vào session endpoint làm FE luôn `401`.
   - **Mitigation**: explicit route review checklist: session route không được thêm `AdminCriticalActionSignatureGuard`.
3. **Risk**: Retry logic FE quá aggressive gây burst lúc backend lỗi.
   - **Mitigation**: giới hạn retry count + backoff ngắn; không retry cho `401`.
4. **Risk**: Lộ thông tin chi tiết lỗi auth trong response/log.
   - **Mitigation**: giữ message generic theo canonical error envelope; review log fields trước merge.
5. **Risk**: Regression auth state race giữa tab/browser.
   - **Mitigation**: giữ unauthorized event bus + bổ sung test concurrent state transitions.
