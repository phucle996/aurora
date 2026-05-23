# Secret Family Rotation Scheduler V1 - Specification

## 1) Purpose + Scope

### Purpose
Định nghĩa behavior source-of-truth cho cơ chế scheduler rotation định kỳ của core secret families để đảm bảo secret lifecycle luôn được duy trì ổn định, an toàn, và không phụ thuộc thao tác thủ công.

### In-scope
- Scheduler nội bộ chạy nền để evaluate rotation due-time.
- Rotation cho các family core: `access_token`, `refresh_token`, `admin_api_key`, `one_time_token`.
- Orchestration dùng `PlanRotation(...)` và `RotateSecretFamily(...)` hiện có.
- Lock đa instance, retry/backoff nội bộ, cache invalidation liên node.

### Out-of-scope
- Thiết kế endpoint HTTP mới cho scheduler control.
- Thay đổi public API của token issuance/verify.
- Thêm runtime config mới trong V1 cho tick/backoff (dùng fixed internal constants).

---

## 2) Terminology / Actors

### Actors
- **Rotation Scheduler Worker**: goroutine nội bộ chạy ticker để kiểm tra due-time.
- **SecretRotationService**: cung cấp `PlanRotation` và `RotateSecretFamily`.
- **SecretRepository**: lock + DB transaction persistence.
- **CacheAsideSecretProvider**: cache local family secrets.
- **RedisSecretInvalidationBus**: broadcast invalidation đa node.

### Terms
- **Family**: nhóm secret theo mục đích (`access_token`, `refresh_token`, ...).
- **Primary**: version ưu tiên dùng để sign.
- **Candidate**: version hợp lệ dùng verify fallback.
- **RotateAt**: mốc thời gian được tính từ `PlanRotation`.

---

## 3) API Contract

- Không có API endpoint mới cho scheduler flow.
- Scheduler là non-HTTP runtime mechanism.
- Hành vi rotate được kích hoạt nội bộ bởi worker khi tới `RotateAt`.

---

## 4) Flow Behavior

### 4.1 Trigger conditions
- Worker MUST chạy ticker định kỳ bằng fixed internal constant (ví dụ 30–60s).
- Mỗi tick, worker MUST evaluate từng family bằng `PlanRotation(...)`.
- Worker MUST chỉ gọi `RotateSecretFamily(...)` khi `now >= RotateAt`.

### 4.2 Main flow
1. Worker tick.
2. Với mỗi family trong danh sách managed families:
   - gọi `PlanRotation(...)` để lấy `RotateAt`.
3. Nếu chưa due, skip family đó.
4. Nếu due, gọi `RotateSecretFamily(...)`.
5. `RotateSecretFamily` acquire lock, rotate transaction, publish invalidation.
6. Các instance khác nhận invalidation và clear cache local.

### 4.3 Failure branches
- Plan fail: log nội bộ + retry tick sau.
- Lock contention: no-op (instance khác đang rotate).
- Rotate fail: log nội bộ + retry tick sau.
- Invalidation bus fail: không rollback DB rotate; cache sẽ tự hết TTL và reload.

### 4.4 Preconditions
- Secret families đã được bootstrap initial version.
- DB và Redis connectivity sẵn sàng.
- Worker đang chạy cùng process server.

### 4.5 Postconditions
- Due family được rotate thành công, primary mới được promote.
- Cache local liên node được invalidate hoặc hết TTL tự nhiên.

### 4.6 State transitions
- `healthy -> due -> rotating -> rotated`
- `rotating -> due` khi fail để retry.

---

## 5) Data & Boundary Rules

### Source-of-truth
- DB là source-of-truth cho secret family/version set.
- Cache local chỉ là read optimization.

### Consistency rules
- Rotation MUST atomic trong DB transaction.
- Active set MUST hợp lệ theo invariant service (1 primary, tối đa 2 active candidates trong cửa sổ chuyển tiếp).
- Scheduler MUST không mutate trực tiếp DB ngoài `RotateSecretFamily`.

### TTL/expiry rules
- Cache TTL theo `cfg.Security.SecretCacheTTL`.
- Worker tick/backoff dùng fixed internal constants V1.

---

## 6) Security Rules

- Không log plaintext secret ở bất kỳ path nào.
- Rotation MUST chạy dưới lock để tránh split-brain rotate.
- Verify path MUST hỗ trợ candidate set để không gãy phiên ngay lúc rotate.
- Invalidation failure MUST không làm lộ secret; chỉ ảnh hưởng freshness cache ngắn hạn.

---

## 7) Failure Semantics

- Scheduler failure không làm crash process; MUST degrade gracefully.
- Rotate fail là fail-closed ở mutate path: không promote nửa vời.
- Invalidation fail: DB rotate vẫn là committed source-of-truth; cache fallback theo TTL reload.
- Client-facing behavior giữ generic như hiện trạng token middleware.

---

## 8) Non-functional Baseline

- Scheduler overhead MUST thấp (check nhẹ theo tick, không poll dày).
- Rotation job MUST idempotent dưới multi-replica lock contention.
- Required dependencies:
  - DB transaction path,
  - lock mechanism,
  - Redis invalidation bus (best-effort).

---

## 9) Acceptance Criteria

- [ ] Có scheduler worker nội bộ cho core secret families.
- [ ] Worker evaluate due-time bằng `PlanRotation` và chỉ rotate khi due.
- [ ] Rotate execution dùng `RotateSecretFamily` (không duplicate rotate logic).
- [ ] Multi-instance không rotate trùng nhờ lock.
- [ ] Rotate thành công invalidate cache liên node (hoặc TTL fallback nếu bus lỗi).
- [ ] Không thêm endpoint HTTP mới cho scheduler.
- [ ] Không log plaintext secret trong toàn flow.
- [ ] V1 dùng fixed internal constants, không thêm config mới.
