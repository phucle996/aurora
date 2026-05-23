# Admin Auth Key Rotation V1 - Implementation Plan

## 1) Mục tiêu triển khai

Triển khai admin key rotation V1 theo spec `admin-auth-key-rotation-v1-spec.md` với hai caller: emergency HTTP và scheduler-triggered worker.
Done definition: rotate dùng chung service/repo path, lock bằng DB advisory lock, trigger flag lưu Redis, scheduler theo policy `delivery-before-commit`.
Hệ thống phải giữ semantics: lock contention no-op (không retry ngay), Telegram fail ở scheduler thì không commit rotate và giữ key cũ active.
Out-of-scope: rotate runtime fragments tự động, endpoint scheduled mới, runtime config mới cho tick/backoff/TTL.

---

## 2) Current state vs target state

**Current state**
- Chưa có rotate endpoint `/admin/auth/rotate-key` trong IAM route/handler.
- Chưa có rotation use-case trong `AdminAPIKeyService` và repo path cho rotate key.
- `AdminAPIKeyAuth` chưa set Redis trigger flag khi expired.
- Chưa có worker scheduler nội bộ cho admin key rotation.

**Target state**
- Có emergency rotate endpoint qua critical guard chain.
- Có scheduler-triggered rotation dùng Redis flag + DB advisory lock + retry policy cố định.
- Emergency và scheduler dùng chung service/repo rotate logic; chỉ khác caller.
- Scheduler flow tuân thủ `delivery-before-commit` và gửi plaintext key qua Telegram nội bộ.

---

## 3) Implementation changes (grouped by subsystem)

### Handler
**Files (SỬA)**
- `internal/iam/transport/http/handler/admin_auth_handler.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `RotateKey(c *gin.Context)` | THÊM | `AdminAuthHandler` chưa có rotate action | Bổ sung action rotate trong cùng handler contract của admin auth, gọi service rotate, trả success không chứa plaintext key, map lỗi 401/503/500 | Giữ contract admin auth tập trung trong 1 handler |
| `RotateKey(c *gin.Context)` delivery path | SỬA | Chưa chốt rõ ownership phát hành key | Chốt `AdminAPIKeyService` chịu trách nhiệm generate + phát hành plaintext key qua `infra/telegram/telegram.go`; handler chỉ gọi service và trả trạng thái generic success | Giữ đúng layering Handler -> Service và tránh lộ key qua API response |

### Service
**Files (SỬA)**
- `internal/iam/domain/service/admin_api_key_service.go`
- `internal/iam/service/admin_api_key_service.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `AdminAPIKeyService` interface | SỬA | Chỉ có `Bootstrap`, `AdminLogin`, `AdminLogout` | Bổ sung methods cho rotation: emergency caller + scheduler caller + worker tick hook | Chuẩn hóa contract service cho 2 caller |
| `RotateAdminAPIKeyEmergency(...)` | THÊM | Chưa có use-case rotate | Thêm flow emergency: guard đã pass từ middleware chain, rotate DB, gửi Telegram, invalidate cache | Có đường rotate thủ công khẩn cấp |
| `RotateAdminAPIKeyScheduled(...)` | THÊM | Chưa có scheduled use-case | Thêm flow scheduler `delivery-before-commit`: prepare key tạm -> Telegram success -> commit rotate -> invalidate cache | Tránh lockout khi delivery fail |
| `TryProcessAdminKeyRotationTrigger(...)` | THÊM | Chưa có worker orchestration | Worker đọc Redis flag, acquire DB lock, no-op nếu lock busy, retry policy fixed constants | Điều phối scheduler HA-safe |
| `loadActiveAdminAPIKey(...)` | SỬA | Có RAM cache active key cho login | Bổ sung invalidate path sau rotate success | Tránh stale active key sau rotate |

### Repo
**Files (SỬA)**
- `internal/iam/domain/repo/admin_api_key_repo.go`
- `internal/iam/repository/admin_api_key_repo.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `AcquireAdminKeyRotationLock(ctx)` | THÊM | Chưa có lock riêng cho rotate key | Thêm DB advisory lock theo scope admin key rotation | Đảm bảo 1 process rotate tại một thời điểm |
| `PrepareNextAdminAPIKeyVersion(...)` | THÊM | Chưa có prepare tạm cho scheduled | Tạo candidate/record tạm trong transaction context để phục vụ delivery-before-commit | Cho phép scheduler rollback nếu Telegram fail |
| `CommitAdminAPIKeyRotation(...)` | THÊM | Chưa có commit rotate chuyên biệt | Commit thứ tự `create new active -> revoke old -> commit` | Giữ invariant single active + audit trail |
| `RollbackPreparedAdminAPIKeyRotation(...)` | THÊM | Chưa có rollback nhánh delivery fail scheduler | Rollback phần prepare chưa commit, giữ key cũ active | Đảm bảo không tự lockout |
| `GetActiveAdminAPIKey(ctx)` | SỬA | Đọc active key phục vụ login | Dùng lại cho pre-check rotation + re-check sau lock | Hạn chế race trước rotate |

### Middleware
**Files (SỬA)**
- `internal/http/middleware/admin_api_key_auth.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `AdminAPIKeyAuth(...)` | SỬA | Expired/invalid trả unauthorized theo logic hiện tại | Khi expired: clear 3 cookies + trả 401 generic + set Redis `rotation_required` flag (`SETNX` + TTL 10m) | Sinh trigger scheduler từ runtime path |

### Cache
**Files (THÊM)**
- `internal/iam/cache/admin_key_rotation_trigger_cache.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `SetRotationRequired(ctx)` | THÊM | Chưa có abstraction flag rotate | Set Redis flag với TTL cố định 10 phút | Chuẩn hóa trigger write path |
| `HasRotationRequired(ctx)` | THÊM | Chưa có read path cho worker | Check flag cho worker tick | Giảm coupling worker với raw Redis API |
| `ClearRotationRequired(ctx)` | THÊM | Chưa có clear path sau success | Clear flag sau rotate success | Tránh rotate lặp không cần thiết |

### Route
**Files (SỬA)**
- `internal/iam/route.go`
- `internal/iam/module.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Route register `POST /admin/auth/rotate-key` | THÊM | Chưa có endpoint rotate | Wire endpoint emergency với chain: rate limit + `AdminAPIKeyAuth` + `AdminCIDR` + `AdminCriticalActionSignatureGuard` + rotate handler | Đúng security boundary cho critical rotate |
| Module wiring rotation worker | SỬA | Module chưa khởi tạo worker rotate | Thêm init dependencies flag cache + worker start/stop lifecycle | Có scheduler runtime theo spec |

### Docs
**Files (SỬA)**
- `internal/iam/docs/spec/admin-auth-key-rotation-v1-spec.md`
- `internal/iam/docs/flow/admin-api-guard-and-critical-actions-flow.md`
- `internal/iam/docs/runbook/admin-auth-redis-outage.md`
- `internal/iam/docs/runbook/admin-auth-rotation-delivery-fail.md` (THÊM)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Rotation spec sync | SỬA | Spec đã có nhưng chưa phản ánh implementation chi tiết code-level | Chốt final semantics theo code triển khai thực tế | Tránh drift spec-code |
| Runbook delivery fail | THÊM | Chưa có runbook chuyên biệt cho scheduler delivery-before-commit | Thêm hướng dẫn xử lý delivery fail, retry thủ công không rotate lại | Giảm risk vận hành |

### Tests
**Files (THÊM/SỬA)**
- `internal/iam/test/svc_test/admin_key_rotation_service_test.go` (THÊM)
- `internal/iam/test/repo_test/admin_key_rotation_repo_test.go` (THÊM)
- `internal/http/middleware/test/admin_api_key_auth_test.go` (SỬA)
- `internal/iam/test/transport_test/admin_auth_handler_test.go` (SỬA)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Service rotation tests | THÊM | Chưa có test emergency/scheduler rotate | Verify delivery-before-commit, lock busy no-op, retry lỗi thật | Khóa business semantics quan trọng |
| Repo lock/transaction tests | THÊM | Chưa có test advisory lock + commit order | Verify atomic order, rollback prepare path | Khóa invariant DB rotate |
| Middleware expired-trigger tests | SỬA | Chưa verify set rotation flag khi expired | Thêm assert clear cookies + set trigger + generic 401 | Đúng trigger policy |
| Handler rotate tests | SỬA | Chưa có test rotate trong bộ test handler admin auth | Bổ sung case rotate thành công/lỗi và assert response không chứa plaintext | Đúng HTTP contract và giữ test cùng cụm admin auth |

---

## 4) Contract changes

- New endpoint: `POST /admin/auth/rotate-key` (emergency/manual).
- Critical guard contract: MUST dùng `AdminCIDR` + `AdminCriticalActionSignatureGuard`; KHÔNG dùng step-up 2FA code cho flow rotate này.
- Response contract rotate: success không chứa plaintext key; key mới gửi Telegram nội bộ.
- Middleware contract change: expired admin runtime request ngoài 401 + clear cookie còn set Redis rotation flag.
- Service/repo interface bổ sung methods rotation và lock/prepare/commit/rollback.
- Error mapping:
  - `401`: guard fail,
  - `503`: dependency verify unavailable,
  - `500`: rotate/delivery internal failure.

---

## 5) Test plan + acceptance

### Required tests
- Happy path:
  - Emergency rotate success qua full guard chain, trả success không leak plaintext.
  - Scheduler rotate success theo `delivery-before-commit`, clear trigger sau commit.
- Error path:
  - Lock contention scheduler => no-op, không retry ngay.
  - Telegram fail ở scheduler => rollback prepare, giữ key cũ active.
  - DB rotate fail => retry ở tick sau với backoff constants.
  - Middleware dependency fail giữ semantics 503 hiện hành.
- Edge path:
  - Nhiều replica cùng thấy flag: chỉ 1 instance rotate (advisory lock).
  - Trigger flag mất/hết TTL: request expired kế tiếp set lại flag.
  - Cache stale sau rotate: active key cache invalidate đúng.

### Acceptance checklist
- [ ] Có endpoint `POST /admin/auth/rotate-key` với critical guard chain đầy đủ.
- [ ] Emergency + scheduler dùng chung service/repo rotate logic.
- [ ] Redis chỉ dùng trigger flag; DB advisory lock là lock chính.
- [ ] Lock busy là no-op và không retry ngay.
- [ ] Scheduler áp dụng delivery-before-commit (Telegram fail không commit rotate).
- [ ] Commit order đúng: create new active -> revoke old -> commit.
- [ ] Success response không chứa plaintext key.
- [ ] Metrics phát ra đủ: lock contention / rotate fail / delivery fail / success.

---

## 6) Rollout & operations

- Enable path:
  - Deploy code với route emergency rotate + worker scheduler enabled mặc định.
  - Worker constants hard-coded theo spec: flag TTL 10m, tick 30s, backoff 5s->15s->30s.
- Disable/fallback:
  - Khi cần tạm dừng scheduler: stop worker ở lifecycle module (không ảnh hưởng emergency endpoint).
  - Redis outage: middleware vẫn fail-closed theo policy auth; rotation trigger giảm hiệu lực cho scheduled path.
- Monitoring signals:
  - `iam_admin_key_rotation_lock_contention_total`
  - `iam_admin_key_rotation_rotate_fail_total`
  - `iam_admin_key_rotation_delivery_fail_total`
  - `iam_admin_key_rotation_success_total`
- Operational runbook:
  - Bắt buộc có runbook `delivery fail before commit` để operator xử lý retry thủ công an toàn.

---

## 7) Risk & mitigation

- Risk: Lock contention cao khi nhiều replica cùng trigger.
  - Mitigation: lock contention no-op + jitter tick worker + không retry ngay.

- Risk: Telegram channel không ổn định làm rotate scheduler chậm.
  - Mitigation: delivery-before-commit + retry backoff + alert delivery fail metric + runbook operator.

- Risk: Stale cache active key gây verify sai sau rotate.
  - Mitigation: invalidate cache ngay sau rotate success + TTL fallback.

- Risk: Scope drift giữa emergency và scheduler implementation.
  - Mitigation: dùng chung service/repo methods, test assert shared logic.

- Risk: Mismatch spec-plan-code khi đổi semantics vận hành.
  - Mitigation: coi spec rotation là source-of-truth, plan update cùng PR với code.
