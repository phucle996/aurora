# Admin Auth Logout Flow V1 - Specification

## 1) Purpose + Scope

### Purpose
Định nghĩa behavior source-of-truth cho logout admin flow V1 trên kênh `/admin`, gồm:
- invalidate runtime session fragments phía client,
- cleanup runtime secret theo `device_id` phía server,
- giữ nguyên security boundary hiện tại của admin runtime auth,
- đồng bộ behavior với short-lived admin session (15 phút) + refresh contract.

### In-scope
- `POST /admin/auth/logout` contract và semantics.
- Cookie clear behavior.
- Runtime secret cleanup behavior trong Redis.
- Failure semantics của logout path.

### Out-of-scope
- Revoke lifecycle mở rộng (multi-device/global revoke).
- Incident response batch revoke/freeze.
- Rotation lifecycle.

---

## 2) Terminology / Actors

### Actors
- **Admin Client**: gọi endpoint logout.
- **AdminAuthHandler**: transport boundary, gọi service, clear cookies, map HTTP.
- **AdminAPIKeyService**: cleanup runtime secret theo `device_id`.
- **AdminAPIKeyAuth middleware**: guard bắt buộc trước logout route.
- **Redis**: runtime secret store.

### Terms
- **Runtime fragments**: `admin_api_token`, `device_id`, `device_secret`.
- **Runtime secret**: hash của `device_secret` lưu theo `device_id` trong Redis.

---

## 3) API Contract

### Endpoint
- `POST /admin/auth/logout`

### Route guard
- Endpoint này MUST đi qua `AdminAPIKeyAuth` middleware.

### Request
- Không yêu cầu body.
- Yêu cầu cookies runtime hợp lệ do middleware kiểm tra:
  - `admin_api_token`
  - `device_id`
  - `device_secret`

### Success response
- `204 No Content`
- Server clear 3 cookies:
  - `admin_api_token`
  - `device_id`
  - `device_secret`
- Cookie clear semantics:
  - `Value=""`
  - `Expires=Unix(0,0)`
  - `MaxAge=-1`

### Session lifecycle alignment
- Logout MUST invalidate ngay runtime fragments dù session còn hạn hay sắp hết hạn.
- Sau logout, mọi refresh/runtime request với fragments cũ MUST bị từ chối (`401`).
- Contract này áp dụng nhất quán với runtime TTL baseline 15 phút của login/renew specs.

### Error response semantics
- `401 Unauthorized`: middleware reject (missing/invalid fragments).
- `503 Service Unavailable`: middleware không verify được dependency auth (secret provider/Redis verify path).
- `500 Internal Server Error`: handler/service logout cleanup fail.

---

## 4) Flow Behavior

### Main flow
1. Request đi qua `AdminAPIKeyAuth`.
2. Handler lấy `device_id` từ cookie.
3. Handler gọi `AdminLogout(ctx, deviceID)`.
4. Service finalize tracking xuống DB một lần (dùng runtime state + request context nếu có), sau đó xóa runtime secret theo `device_id` trong Redis.
5. Handler clear 3 cookies runtime.
6. Trả `204`.

### Error/failure branches
- Middleware reject -> request không vào handler (`401`/`503`).
- Service cleanup fail -> handler trả `500`, không coi logout thành công.

### Preconditions
- Route đã được wire middleware `AdminAPIKeyAuth`.
- Redis reachable để cleanup runtime secret (trừ trường hợp `device_id` rỗng).

### Postconditions
- Success: client fragments bị clear; runtime secret theo `device_id` bị xóa.
- Failure: không xác nhận logout hoàn tất.

### State transitions
- `Authenticated -> LogoutRequested -> LoggedOut` (success)
- `Authenticated -> LogoutRequested -> LogoutFailed` (infra/service fail)

---

## 5) Data & Boundary Rules

### Source-of-truth / stores
- Redis là runtime store cho session fragments + realtime tracking tối thiểu (`last_seen_at`).
- Logout V1 không thay đổi DB tables.

### Boundary rules
- Nếu `device_id` rỗng, service trả `nil` (no-op cleanup) và handler vẫn clear cookies.
- Cleanup Redis chỉ áp dụng theo `device_id` hiện tại của session đang logout.
- DB `last_seen_*` được ghi tại thời điểm finalize session, không ghi liên tục mỗi refresh.

---

## 6) Security Rules

- Logout endpoint MUST được bảo vệ bởi `AdminAPIKeyAuth` middleware.
- Không log plaintext token/device_secret.
- Lỗi trả generic theo `apires` mapping, không leak verify internals.
- Cookie clear phải giữ đúng cookie names/path/domain policy để xóa đúng session fragments.

---

## 7) Failure Semantics

- Middleware auth dependency fail (verify path) -> fail-closed (`503`).
- Middleware token/fragment invalid -> fail-closed (`401`).
- Service Redis delete fail -> handler trả `500`.
- Không có retry/backoff logic trong handler logout path; retry policy thuộc layer client hoặc vận hành.

---

## 8) Non-functional Baseline

- Baseline tạm cho admin auth routes:
  - `p95 latency < 800ms`
  - `error rate < 1%`
- Dependencies bắt buộc cho logout success path:
  - secret provider (qua middleware verify path),
  - Redis runtime store.

---

## 9) Acceptance Criteria

- [ ] `POST /admin/auth/logout` chỉ pass khi qua `AdminAPIKeyAuth`.
- [ ] Success trả `204` và clear đủ 3 cookies runtime.
- [ ] Success path xóa runtime secret theo `device_id` trong Redis.
- [ ] Sau logout, gọi `POST /admin/auth/refresh` với fragments cũ phải nhận `401`.
- [ ] Middleware invalid fragments trả `401` generic.
- [ ] Middleware dependency verify fail trả `503`.
- [ ] Service cleanup fail trả `500`.
- [ ] Không log plaintext token/device_secret trong logout flow.
