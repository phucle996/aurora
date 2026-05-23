# IAM Error Envelope Rollout V1 - Specification

Upstream global spec: `controlplane/docs/spec/app-error-envelope-v1-spec.md`  
Upstream canonical contract: `controlplane/docs/contract/app-error-envelope-canonical-contract.md`

## 1) Purpose + Scope
### Purpose
Chuẩn hóa error behavior cho toàn bộ IAM module theo cùng cơ chế `AppError{Kind, Reason, Cause}` để:
- giữ HTTP mapping ổn định theo `Kind` domain,
- có `Reason` stable cho log/metrics/trace,
- giữ `Cause` nguyên thủy cho debug nội bộ nhưng không leak ra client.

### In-scope
- Tất cả IAM service flows đang public qua HTTP:
  - `AuthService` (register/login),
  - `RefreshTokenService` (refresh),
  - `OneTimeTokenService` (issue/consume),
  - `RbacService` (read/mutation),
  - `AdminAPIKeyService` (bootstrap/login/refresh/logout/rotation/finalize).
- Handler mapping + logging parity cho các flow trên.
- Quy tắc boundary repo/service cho lỗi raw.
- Reason taxonomy IAM bounded-set.

### Out-of-scope
- Không đổi request/response schema public của endpoint IAM.
- Không đổi business rules auth/rbac hiện có.
- Không rollout cross-module ngoài IAM trong spec này.

## 2) Terminology / Actors
- **Kind**: domain class trong `internal/iam/errorx/error.go`, dùng để map HTTP semantics.
- **Reason**: stable machine code, bounded-set theo flow IAM.
- **Cause**: lỗi kỹ thuật raw (sql/redis/network/runtime), dùng nội bộ.
- **Repo technical error**: lỗi trả từ repository/dependency, không gắn business class.
- **Service envelope mapping**: service map raw error -> `apperr.Wrap(Kind, Reason, Cause)`.
- **Handler HTTP mapping**: handler map status theo `errors.Is(err, Kind)` và trả generic safe response.

## 3) API Contract
### Public API contract
- Không thêm/xóa endpoint IAM.
- Không đổi schema request/response hiện hữu.

### Status code semantics (không đổi hành vi business)
- `ErrInvalidArgument` -> `400` hoặc `401` tùy endpoint contract hiện tại.
- `ErrInvalidCredentials` / `ErrInvalidSession` / admin invalid auth kinds -> `401` theo contract endpoint.
- `ErrVerificationRequired` -> `403`.
- `ErrAuthenticationUnavailable` -> `503` hoặc `500` theo contract endpoint hiện tại.
- Unknown kind -> generic internal error.

### Response detail policy
- Client response không chứa `Reason` và `Cause`.
- Response body giữ generic message policy như hiện tại.

## 4) Flow Behavior
### 4.1 Main flow chuẩn
1. Handler gọi service.
2. Service gọi repo/cache/external dependency.
3. Repo/dependency lỗi -> service MUST wrap sang `AppError` với `Kind` phù hợp + `Reason` stable + `Cause` raw.
4. Handler nhận lỗi:
   - map status bằng `errors.Is(..., Kind)`,
   - log qua logger field chuẩn (`error_kind`, `error_reason`, `error_cause`),
   - trả generic response theo endpoint contract.

### 4.2 Module-specific behavior
- **AuthService**:
  - DB/cache/publisher/sign/token generation failures MUST có `Reason` phân nhóm (`dependency_error`, `token_issue`, `cache_error`, `publish_error`...).
- **RefreshTokenService**:
  - session invalid vẫn trả invalid-session kind;
  - lỗi rotate/query/sign MUST có reason + cause.
- **OneTimeTokenService**:
  - cache unavailable và token generate error MUST có reason tách biệt.
- **RbacService**:
  - repo trả raw/not-found/invalid parse;
  - service map domain kind và reason cho read/mutation.
- **AdminAPIKeyService**:
  - giữ behavior hiện tại, bổ sung parity reason taxonomy nếu còn thiếu.

### 4.3 Preconditions
- Có `pkg/apperr` dùng chung.
- IAM `errorx` có đủ domain kinds.
- Logger đã hỗ trợ append app-error fields.

### 4.4 Postconditions
- Mọi lỗi từ IAM service lên handler có thể truy vết bằng `kind/reason/cause`.
- Không có raw infra text đi thẳng ra HTTP response.

### 4.5 Edge cases
- `Kind=nil` hoặc envelope không hợp lệ: fallback internal generic mapping + warning log.
- `Reason` rỗng: fallback reason mặc định theo flow.
- `Cause=nil`: hợp lệ.
- Wrapped nested error vẫn phải `errors.Is(..., Kind)` đúng.

## 5) Data & Boundary Rules
- Source of truth phân lớp:
  - Domain class: `internal/iam/errorx/error.go`.
  - Reason taxonomy: `internal/iam/errorx/reason.go`.
  - Envelope type: `pkg/apperr`.
- Boundary bắt buộc:
  - Repo: trả raw technical errors hoặc raw not-found parse errors.
  - Service: map `Kind/Reason/Cause`.
  - Handler: map HTTP + log.
- Repo MUST NOT quyết định HTTP semantics.
- Service MUST NOT bỏ raw cause khi lỗi là dependency lỗi kỹ thuật.

## 6) Security Rules
- Không log secret/token/password/otp thô.
- `error_cause` MUST sanitize theo policy trong `pkg/apperr`.
- Reason label MUST bounded, không dùng dynamic string từ SQL driver.
- Unauthorized/invalid credential response phải generic, không lộ branch kỹ thuật.

## 7) Failure Semantics
- **Fail-closed response detail**: luôn generic khi lỗi.
- **Fail-open diagnostics**: nếu thiếu cause vẫn xử lý theo kind.
- **Retry semantics**:
  - Worker/scheduler classify theo `Kind`, không theo string cause.
  - API sync path không tự retry ở envelope layer.
- **Dependency failure policy**:
  - lỗi infra không được gán nhầm invalid-credential/invalid-mfa.

## 8) Non-functional Baseline
- Không thêm DB/network call chỉ để wrap lỗi.
- Overhead chỉ ở object wrap + log field append.
- Reason cardinality bounded theo dictionary IAM.
- Observability baseline:
  - logs có `error_kind` + `error_reason` (+ sanitized `error_cause`),
  - metrics có labels stable theo reason groups.

## 9) Acceptance Criteria
- [ ] Tất cả IAM service public flows trả lỗi dạng envelope (trừ helper private không đi ra handler).
- [ ] Handler IAM map status bằng `errors.Is(kind)` nhất quán.
- [ ] Không endpoint nào leak cause/raw error text ra response body.
- [ ] `reason.go` bao phủ taxonomy cho auth/refresh/ott/rbac/admin.
- [ ] Repo không ném business HTTP semantics mới; giữ raw technical + raw not-found parse cho service map.
- [ ] Có test cho happy/error/edge ở từng flow chính sau rollout.
- [ ] Log/metric query có thể pivot theo `error_reason` mà không cardinality explosion.
