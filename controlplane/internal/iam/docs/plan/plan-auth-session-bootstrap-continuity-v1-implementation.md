# Plan Auth Session Bootstrap Continuity V1 - Implementation Plan

## 1) Mục tiêu

Triển khai `GET /api/v1/auth/session` theo spec `auth-session-bootstrap-continuity-v1-spec.md` để:

- giữ semantics auth fail-closed đúng chuẩn,
- loại bỏ spam `401/429` do session probe loop,
- giữ metrics theo hướng vận hành tối giản (metric nào có action thì giữ).

---

## 2) Scope và Non-scope

### In-scope

- Cập nhật behavior backend cho endpoint `/api/v1/auth/session`.
- Chốt middleware chain đúng boundary cho session probe.
- Bổ sung test cho các nhánh `200/401/503` và anti-spam related behavior (ở mức backend contract).
- Đồng bộ docs/spec/plan để team FE + BE dùng cùng source-of-truth.

### Out-of-scope

- Redesign auth architecture sang BFF.
- Thay đổi lifecycle core của fragment access/refresh + device sign.
- Mở rộng payload business data lớn ở session bootstrap (user/permissions/feature flags) trong phase này.

---

## 2.1 Implementation Decisions (chốt trước khi code)

1. **Auth source cho `Session()`**
   - `Session()` không tự verify token độc lập.
   - Endpoint dựa vào auth middleware đã chạy trước đó; handler chỉ đọc auth state từ context đã được middleware inject.
   - Nếu context thiếu/invalid -> trả `401`; nếu dependency verify unavailable -> `503`.

2. **Route chain cho `/api/v1/auth/session`**
   - Giữ chain auth runtime chuẩn của user channel.
   - Không gắn mutation-only guard hoặc critical-signature guard.
   - Rate-limit endpoint-specific cho session probe được bật trong phase observability rollout (không trộn vào semantics auth).

3. **Metrics ownership**
   - Metric bổ sung đặt tại `internal/iam/metrics/`.
   - Middleware global chỉ cung cấp per-route HTTP metrics nền.
   - Nhãn metric IAM session probe chỉ dùng low-cardinality labels (`status`, `result_class`, `channel`), không dùng `user_id/device_id/request_id/raw_ip`.

4. **Test matrix tối thiểu bắt buộc**
   - `200`: auth context hợp lệ.
   - `401`: missing/invalid auth context.
   - `503`: dependency verify/runtime unavailable.
   - `read-only`: gọi session không gây mutation state.

---

## 3) Change Map theo file

## 3.1 Spec lock (Docs)

- **File**: `internal/iam/docs/spec/auth-session-bootstrap-continuity-v1-spec.md`
- **Thay đổi**:
  - Chốt state machine đầy đủ: initial bootstrap + subsequent recheck.
  - Chốt anti-spam gates: cooldown, in-flight dedupe, min recheck interval, cross-tab dedupe.
  - Chốt SLI/SLO/alerts theo hướng metric minimalism.
- **Lý do**: tránh lệch implementation giữa FE/BE và loại bỏ ambiguity.
- **Risk**: scope phình nếu nhồi thêm future design.
- **Mitigation**: giữ out-of-scope rõ ràng trong spec.

## 3.2 Route wiring

- **File**: `internal/iam/route.go`
- **Thay đổi**:
  - Verify chain cho `GET /api/v1/auth/session` đúng boundary auth hiện hành.
  - Đảm bảo endpoint này không bị gắn guard không phù hợp (critical signature/mutation-only guard).
- **Lý do**: session probe phải lightweight và đúng ngữ nghĩa read-only auth check.
- **Risk**: gắn thiếu middleware gây false allow.
- **Mitigation**: transport tests explicit cho unauth/authenticated path.

## 3.3 Handler contract

- **File**: `internal/iam/transport/http/handler/auth_handler.go`
- **Thay đổi**:
  - `Session()` trả đúng envelope contract của spec.
  - Bỏ stub response kiểu always-200.
  - Mapping lỗi rõ `401` vs `503` vs `500` theo runtime verify outcome.
- **Lý do**: source-of-truth auth state nằm ở backend, không để FE đoán.
- **Risk**: map sai lỗi làm tăng false-positive logout.
- **Mitigation**: test theo từng error class + review với runtime auth middleware behavior.

## 3.4 Observability minimalism

- **File**: `internal/iam/metrics/` (nơi đặt metric bổ sung của IAM session probe)
- **File**: `internal/http/middleware/observability.go` (chỉ verify route labels đã có)
- **Thay đổi**:
  - Tận dụng metric per-route sẵn có cho `/api/v1/auth/session`.
  - Metric bổ sung cho session probe phải đặt trong IAM metrics module (`internal/iam/metrics/`), không đặt rải rác ở middleware global.
  - Chỉ bổ sung metric mới nếu có action vận hành rõ (không thêm high-cardinality labels).
- **Lý do**: tránh metrics noise/cardinality explosion.
- **Risk**: thiếu visibility cho session spam regression.
- **Mitigation**: dashboard/query tập trung status ratio + per-user/session probe volume từ labels an toàn.

## 3.5 Tests

- **File(s)**: `internal/iam/test/transport_test/...` (và các test liên quan handler/auth route)
- **Thay đổi**:
  - Thêm test contract cho `GET /api/v1/auth/session`:
    - `200` khi auth pass.
    - `401` khi auth invalid/missing fragments.
    - `503` khi dependency runtime unavailable.
  - Thêm test bảo đảm endpoint không mutate state.
- **Lý do**: khoá behavior chống drift khi refactor.
- **Risk**: test phụ thuộc setup runtime nặng.
- **Mitigation**: tách test transport nhẹ + mock/stub dependency hợp lý.

---

## 4) Phase triển khai

### Phase 1 - Contract Freeze

- Lock spec v1, không mở rộng scope payload.
- Chốt route/handler expected behavior matrix.
- **Status**: completed.
- **Freeze decisions**:
  - Session endpoint contract giữ `200/401/503` theo `auth-session-bootstrap-continuity-v1-spec.md`.
  - FE anti-spam state machine đã chốt: cooldown + in-flight dedupe + cross-tab dedupe + bounded retry.
  - Scope phase này không chuyển sang BFF architecture.
  - Metrics bổ sung thuộc ownership `internal/iam/metrics/`.

### Phase 2 - Backend Implementation

- Patch `route.go` + `auth_handler.go` theo matrix.
- Verify middleware chain đúng boundary.

### Phase 3 - Test & Verification

- Bổ sung/chạy test transport cho session endpoint.
- Chạy smoke local: unauth/authenticated/dependency failure.
- **Status**: completed.
- **Evidence**:
  - `TestAuthHandlerSessionUnauthorizedWithoutAuthContext` -> `401`.
  - `TestAuthHandlerSessionSuccessWithAuthContext` -> `200` + `authenticated=true`.
  - `TestAuthHandlerSessionServiceUnavailableWhenAccessMiddlewareMissing` -> `503`.
  - `TestAuthHandlerSessionReadOnlyNoSetCookie` -> xác nhận read-only (không set cookie).

### Phase 4 - Observability & Rollout Gate

- Xác nhận per-route metric đã theo dõi được session endpoint.
- Implement metric bổ sung tại `internal/iam/metrics/` theo spec SLI/SLO.
- Chốt alert ngưỡng theo spec trước khi rollout rộng.
- **Status**: completed (implementation side).
- **Implemented**:
  - Added IAM metric `iam_auth_session_total{result}` in `internal/iam/metrics/metrics_auth.go`.
  - Handler `AuthHandler.Session` emit outcomes:
    - `result=success` khi auth context hợp lệ.
    - `result=unauthorized` khi thiếu/invalid auth context.
  - Metric ownership giữ trong `internal/iam/metrics/` (không đẩy vào middleware global).

---

## 5) Verification Checklist

- [ ] `/api/v1/auth/session` không còn always-200 stub.
- [ ] Semantics `200/401/503` đúng theo spec.
- [ ] Endpoint là read-only, không ghi DB/runtime mutation.
- [ ] Route không gắn nhầm middleware mutation-only/critical-signature.
- [ ] Metric route-level đủ để theo dõi spam regression.
- [ ] Không thêm label high-cardinality cho metric/log.

---

## 6) Rollback Plan

- Revert thay đổi `route.go` + `auth_handler.go` về behavior trước đó nếu phát sinh false-positive diện rộng.
- Giữ nguyên spec để phục vụ postmortem, update addendum nêu rõ nguyên nhân rollback.
- Chỉ rollback runtime behavior, không rollback toàn bộ docs history.

---

## 7) Definition of Done

- Contract endpoint khớp spec v1.
- Test pass cho các nhánh chính `200/401/503`.
- Không còn spam loop do backend mis-semantics.
- Dashboard/alert tối thiểu theo spec đã khả dụng cho vận hành.
