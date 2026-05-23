# Admin Auth Key Rotation V1 - Specification

## 1) Purpose + Scope

### Purpose
Định nghĩa behavior source-of-truth cho rotation `admin_api_key` của kênh `/admin` với 2 cơ chế:
- **Emergency rotation** qua API thủ công cho tình huống khẩn cấp.
- **Scheduler-triggered rotation** qua trigger nội bộ khi runtime auth phát hiện expired.

Mục tiêu là giảm window rủi ro, giữ zero-trust, và tránh long-lived admin access bằng key cũ.

### In-scope
- `POST /admin/auth/rotate-key` cho emergency rotation.
- Trigger flag từ `AdminAPIKeyAuth` khi gặp expired.
- Worker nội bộ (goroutine) xử lý scheduler-triggered rotation theo flag.
- Semantics clear cookie + generic error cho request expired.
- Atomic transaction rotate key (create new -> revoke old -> commit).

### Out-of-scope
- Rotation tự động cho runtime token/session fragments.
- Thêm endpoint mới cho scheduled rotation.
- Thêm runtime config mới cho worker tick/flag/backoff trong V1.

---

## 2) Terminology / Actors

### Actors
- **Admin Client**: client gọi route `/admin` và route emergency rotate.
- **AdminAPIKeyAuth middleware**: verify runtime fragments; phát hiện expired; clear cookie; set trigger flag.
- **Rotation Worker**: goroutine nội bộ nhận trigger và chạy rotate scheduler.
- **AdminAPIKeyService**: orchestration rotation logic cho cả emergency và scheduler.
- **AdminAPIKeyRepository**: transaction DB cho rotate key.
- **Secret Delivery Channel**: kênh an toàn phát plaintext key mới (Telegram theo hệ thống hiện hành).

### Terms
- **Emergency rotation**: rotate thủ công qua `POST /admin/auth/rotate-key`.
- **Scheduler-triggered rotation**: rotate tự động do worker xử lý từ trigger nội bộ.
- **Rotation trigger flag**: cờ nội bộ đánh dấu cần rotate key.
- **Runtime fragments**: `admin_api_token`, `device_id`, `device_secret`.

---

## 3) API Contract

### 3.1 Emergency rotation endpoint
- `POST /admin/auth/rotate-key`
- Mục đích: rotation khẩn cấp/thủ công.

#### Success
- `200 OK` (hoặc contract success hiện hành của handler rotate) **không** chứa plaintext key.
- Plaintext key mới MUST được phát qua Telegram channel nội bộ (cùng cơ chế vận hành như bootstrap), không trả về client.

#### Error semantics
- `401`: auth/signature/CIDR guard không đạt.
- `503`: dependency verify path không khả dụng.
- `500`: rotate transaction hoặc delivery fail theo mapping handler.

### 3.2 Scheduler-triggered path (non-HTTP)
- Không có endpoint mới.
- Trigger đến từ middleware khi phát hiện expired.

### 3.3 Expired request semantics trên admin routes
Khi middleware phát hiện expired token/key:
- MUST clear 3 cookies runtime (`admin_api_token`, `device_id`, `device_secret`).
- MUST trả `401 Unauthorized` generic.
- MUST set rotation trigger flag cho worker.

### 3.4 Response policy
- Client-facing errors MUST generic, không leak nguyên nhân nội bộ.

---

## 4) Flow Behavior

### 4.0 Trigger conditions

#### Emergency trigger condition
- Emergency rotation MUST ONLY run khi admin chủ động gọi `POST /admin/auth/rotate-key`.
- Request MUST pass full critical guard chain (auth + `AdminCIDR` + `AdminCriticalActionSignatureGuard`).
- Emergency rotate flow này KHÔNG dùng step-up 2FA code.

#### Scheduler-triggered condition
- Scheduler-triggered rotation MUST ONLY run khi `AdminAPIKeyAuth` phát hiện expired trên admin runtime request.
- Middleware MUST set `rotation_required` flag sau khi clear cookies và trả `401` generic.
- Worker MUST ONLY execute rotate khi thấy trigger flag trong Redis và acquire DB advisory lock thành công.

### 4.1 Emergency rotation flow
1. Admin gọi `POST /admin/auth/rotate-key`.
2. Request MUST pass full critical guard chain (auth + `AdminCIDR` + `AdminCriticalActionSignatureGuard`).
3. Service chạy rotate transaction.
4. Repo thực thi atomic order: create new key success -> revoke old key -> commit.
5. Service invalidate active-key RAM cache.
6. Handler trả success payload không chứa plaintext key và ghi audit success.
7. Plaintext key mới MUST được gửi qua Telegram channel nội bộ (như bootstrap), không trả về client.

Emergency rotation MUST dùng chung rotation use-case trong `AdminAPIKeyService` và cùng DB transaction path trong `AdminAPIKeyRepository` với scheduler-triggered rotation; chỉ khác entrypoint caller.

### 4.2 Scheduler-triggered rotation flow
1. Request `/admin` vào `AdminAPIKeyAuth`.
2. Middleware phát hiện expired token/key.
3. Middleware clear cookies + trả `401` + set trigger flag.
4. Worker quan sát trigger flag với ticker cố định (internal constant, ví dụ mỗi 15–30s).
5. Worker acquire DB advisory lock đảm bảo single-rotation.
6. Worker gọi service chuẩn bị key material mới ở trạng thái tạm (chưa revoke key cũ, chưa activate key mới).
7. Worker phát key mới qua Telegram channel nội bộ.
8. Chỉ khi delivery thành công, service/repo mới commit transaction rotate theo thứ tự: create new active -> revoke old -> commit.
9. Worker ghi audit success, clear trigger flag, invalidate active-key cache.

Scheduler-triggered rotation MUST dùng cùng service/repository rotation logic như emergency rotation; chỉ caller là worker nội bộ thay vì HTTP handler.

### 4.3 Emergency rotation failure branches
- Guard fail (auth/signature/CIDR): request bị từ chối (`401`), không vào rotate transaction.
- Rotate DB fail: handler trả lỗi theo mapping hiện hành (`500`).
- Telegram delivery fail: handler trả lỗi theo mapping hiện hành (`500`), không coi rotation hoàn tất vận hành.

### 4.4 Scheduler-triggered rotation failure branches
- Set trigger flag fail: request vẫn `401`; trigger sẽ được tạo lại ở request expired tiếp theo.
- Lock contention: no-op (instance khác đang xử lý) và MUST NOT retry ngay trong cùng vòng xử lý.
- Rotate DB fail: worker giữ/đặt lại flag để retry tick sau.
- Delivery fail: MUST rollback phần chuẩn bị rotate chưa commit, giữ key cũ active, và retry ở tick sau theo backoff.

### 4.5 Preconditions
- Có active admin key trong DB.
- Worker chạy cùng process server.
- Delivery channel khả dụng cho phát key mới.

### 4.6 Postconditions
- Success: key mới active, key cũ revoked, cache invalidated, trigger flag cleared, audit success recorded.
- Expired request luôn bị từ chối và cookie fragments bị clear.

### 4.7 State transitions
- `active -> rotation_required -> rotating -> rotated`
- `rotating -> rotation_required` khi fail để retry.

---

## 5) Data & Boundary Rules

### Source-of-truth
- DB là source-of-truth cho `admin_api_key` lifecycle.
- Redis/cache dùng cho runtime support (session verify, trigger coordination, cache tạm).
- Rotation trigger flag MUST lưu ở Redis; DB không dùng để lưu trigger flag trong V1.

### Consistency rules
- Rotation MUST atomic trong DB transaction.
- Thứ tự bắt buộc: create new -> revoke old -> commit.
- Key cũ trong V1 MUST revoke mềm (không hard-delete) để giữ forensic/audit trail.
- Scheduler-triggered rotation MUST dùng `delivery-before-commit`: delivery Telegram thành công là precondition trước khi commit rotate.

### TTL/expiry rules
- `admin_api_token` TTL tiếp tục theo config hiện có (`AdminAPITokenTTL`).
- Trigger flag TTL, worker tick và retry backoff dùng fixed internal constants trong code V1:
  - `rotation flag TTL`: 10 phút.
  - `worker tick`: 30 giây.
  - `retry backoff`: 5 giây -> 15 giây -> 30 giây (cap 30 giây).

---

## 6) Security Rules

- Emergency endpoint MUST giữ full critical guard chain hiện có.
- Middleware MUST fail-closed cho expired/invalid admin runtime.
- Rotation MUST không chạy trực tiếp trong middleware request path.
- Không log plaintext `admin_api_key`, token, `device_secret`, recovery code.
- Plaintext key mới chỉ được phát qua secret delivery channel an toàn.

---

## 7) Failure Semantics

- Expired/invalid runtime auth: `401` generic + clear cookies.
- Verify dependency unavailable ở middleware: `503` theo semantics auth hiện hành.
- Emergency rotate guard fail: `401` generic.
- Worker lock contention: no-op, không ảnh hưởng client response và không retry ngay.
- DB rotate fail: fail-closed ở worker, retry tick sau.
- Delivery fail: chưa coi là operationally complete, phải retry delivery.
- Scheduler delivery fail: MUST NOT commit rotate; key cũ giữ nguyên active, retry tick sau.

Retry/backoff worker là fixed internal constants trong code V1 (5s -> 15s -> 30s, cap 30s).

---

## 8) Non-functional Baseline

- Baseline admin route vẫn theo ngưỡng IAM hiện hành (latency/error-rate).
- Worker MUST không block HTTP request path.
- Scheduler-triggered rotation MUST idempotent dưới lock đa instance.
- Dependencies bắt buộc:
  - DB transaction path,
  - DB advisory lock mechanism,
  - Redis trigger flag store,
  - secret delivery channel.
- Metrics bắt buộc (MUST emit):
  - `iam_admin_key_rotation_lock_contention_total`
  - `iam_admin_key_rotation_rotate_fail_total`
  - `iam_admin_key_rotation_delivery_fail_total`
  - `iam_admin_key_rotation_success_total`

### Operator runbook requirement
- Hệ thống MUST có runbook vận hành riêng cho tình huống `delivery fail before rotate commit`.
- Runbook tối thiểu phải mô tả:
  - cách xác định instance/job đang giữ trạng thái `delivery_pending`,
  - cách retry delivery thủ công an toàn (không rotate lại),
  - điều kiện escalate incident khi vượt ngưỡng retry/backoff.

---

## 9) Acceptance Criteria

- [ ] Spec mô tả đủ cả emergency và scheduler-triggered rotation.
- [ ] Chỉ có 1 endpoint rotate hiện hữu (`POST /admin/auth/rotate-key`), không thêm endpoint scheduled.
- [ ] Middleware khi expired: clear 3 cookies + trả `401` generic + set trigger flag.
- [ ] Có worker nội bộ nhận trigger và rotate theo lock.
- [ ] Worker dùng DB advisory lock; Redis chỉ dùng cho trigger flag.
- [ ] Rotation DB đúng thứ tự: create new success -> revoke old -> commit.
- [ ] Lock contention là no-op và không retry ngay.
- [ ] Key cũ revoke mềm, không hard-delete trong V1.
- [ ] Rotate success phải invalidate active-key RAM cache.
- [ ] Không log plaintext secrets/tokens trong toàn flow rotation.
- [ ] V1 không thêm runtime config mới cho worker tick/flag/backoff.
- [ ] Có đủ 4 metrics bắt buộc cho lock contention / rotate fail / delivery fail / success.
- [ ] Có runbook xử lý `delivery fail before rotate commit` cho operator.
- [ ] Scheduler flow tuân thủ delivery-before-commit: Telegram fail thì không commit rotate, giữ key cũ active.
