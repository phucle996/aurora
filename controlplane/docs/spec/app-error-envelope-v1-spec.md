# App Error Envelope V1 - Specification

Upstream idea: `controlplane/docs/idea/app-error-envelope-full-idea.md`  
Canonical contract: `controlplane/docs/contract/app-error-envelope-canonical-contract.md`

## 1) Purpose + Scope
### Purpose
Định nghĩa behavior chuẩn cho error envelope dùng chung toàn controlplane để đảm bảo:
- map HTTP theo domain class (`Kind`) nhất quán,
- log/metrics có `Reason` ổn định,
- giữ `Cause` cho debug nội bộ mà không leak ra client.

### In-scope
- Shared envelope behavior cho `Kind/Reason/Cause` theo `APPERR-ERR-001`.
- Handler behavior khi nhận error đã được wrap.
- Logging/metrics redaction và cardinality rule theo `APPERR-ERR-002`.
- Pilot behavior cho IAM admin auth flows theo `APPERR-GOV-001` + `APPERR-API-001`.

### Out-of-scope
- Không thay đổi public API payload contract hiện hữu.
- Không rollout toàn bộ module trong một lần.
- Không thiết kế full taxonomy cho mọi domain ngay trong v1.

## 2) Terminology / Actors
- **Kind**: domain class error từ module-local `errorx` (nguồn map business + HTTP).
- **Reason**: stable machine code để log/metric (bounded set).
- **Cause**: lỗi kỹ thuật nguyên thủy (driver/sql/runtime), chỉ dùng nội bộ.
- **AppError**: envelope shared chứa `Kind/Reason/Cause`.
- **Handler**: map status + redact response + emit log/metric.
- **Service/Repo**: phát sinh/wrap error theo boundary trách nhiệm.
- **Ops/SRE**: tiêu thụ reason/cause trong hệ observability theo RBAC.

## 3) API Contract
Theo `APPERR-API-001`:
- Không thêm endpoint mới.
- Không đổi request schema của endpoint hiện tại.
- Response client khi lỗi vẫn theo module contract hiện hữu (generic/safe).

### Status code semantics
- Handler MUST map status dựa trên `errors.Is(err, Kind)`.
- Nếu không match được `Kind`, handler MUST fallback về generic internal error policy.

### Error detail exposure
- `Reason` mặc định internal-only.
- `Cause` tuyệt đối không trả trực tiếp ra response body.

## 4) Flow Behavior
### 4.1 Feature Groups
- **G1 - Envelope creation**: Repo/Service wrap lỗi thành `AppError` với `Kind` bắt buộc, `Reason` ổn định, `Cause` tùy chọn.
- **G2 - Transport mapping**: Handler unwrap/match `Kind`, map HTTP, ghi log/metric theo reason.
- **G3 - Observability contract**: reason label bounded, cause được sanitize theo policy.

### 4.2 Main Flow (sync request)
1. Client gọi API.
2. Handler gọi service.
3. Service có thể gọi repo/dependency.
4. Khi lỗi:
   - nếu có domain class -> wrap vào envelope (`Kind`, `Reason`, `Cause`),
   - nếu không có domain class -> map về internal kind chuẩn của module trước khi trả lên handler.
5. Handler nhận error:
   - map status theo `Kind`,
   - ghi structured log với `error_kind`, `error_reason`, `error_cause` (internal),
   - trả response generic theo endpoint contract.

### 4.3 Preconditions
- Module có domain sentinel trong `internal/<module>/errorx`.
- Handler có bảng map `Kind -> HTTP status` hợp lệ.

### 4.4 Postconditions
- Client nhận status/message đúng policy và không chứa dữ liệu nhạy cảm.
- Internal log có đủ context `kind/reason/cause` để debug.

### 4.5 Validation Rules
- `Kind` MUST non-nil.
- `Reason` MUST deterministic và thuộc danh sách stable reason của flow.
- `Reason` MUST NOT lấy trực tiếp từ raw SQL/driver error string.
- `Cause` MAY nil.

### 4.6 Edge Cases
- **Missing Kind**: coi là contract violation -> fallback internal error + warning log.
- **Empty/unstable Reason**: fallback reason chuẩn của module + warning log.
- **Nil Cause**: vẫn hợp lệ, handler log `cause=nil`.
- **Wrapped nested errors**: `errors.Is` vẫn phải match theo `Kind`.

## 5) Data & Boundary Rules
Theo `APPERR-DB-001` và `APPERR-GOV-001`:
- Không có migration bắt buộc cho v1.
- Envelope không là source-of-truth cho business data; chỉ là error transport object.
- Source-of-truth phân lớp:
  - Domain class: `internal/<module>/errorx/*`.
  - Shared envelope: `pkg` shared package.
  - HTTP mapping: handler layer.
  - Metrics label set: module-level reason dictionary.
- Redis/DB state contract của business flow giữ nguyên, không thay đổi bởi spec này.

## 6) Security Rules
Theo `APPERR-ERR-002` và `APPERR-PERM-001`:
- Handler MUST không trả `Cause` ra client.
- Log MUST redact secret/token/API key/OTP.
- Metrics MUST dùng bounded reason label để tránh cardinality explosion.
- Quyền xem detail `reason/cause` chỉ dành cho operator có quyền truy cập observability systems.

## 7) Failure Semantics
- **Fail-closed (public response detail)**: nếu uncertain, luôn trả generic message.
- **Fail-open (internal diagnostics)**: nếu thiếu một phần envelope (vd `Cause=nil`), request vẫn kết thúc với mapping theo `Kind`.
- **Retry policy**:
  - Envelope layer không tự retry.
  - Retry classification ở async worker (nếu có) MUST dựa vào `Kind`, không dựa text `Cause` (`APPERR-EVT-001`).
- **Unknown dependency error**: map vào module internal kind trước khi lên handler.

## 8) Non-functional Baseline
- Không được thêm network call/DB call mới chỉ để phục vụ envelope.
- Overhead mỗi request chỉ gồm tạo object error + structured fields cho log/metric.
- Reason cardinality cho mỗi flow phải bounded và review được.
- Baseline observability:
  - log có `error_kind` + `error_reason`,
  - metric reason labels ổn định theo flow auth/critical.

## 9) Acceptance Criteria
- [ ] Service/repo có thể forward `Kind/Reason/Cause` lên handler mà không phá boundary.
- [ ] Handler map HTTP bằng `errors.Is(..., Kind)` nhất quán.
- [ ] Client response không leak raw `Cause`.
- [ ] Reason labels không lấy từ raw DB/driver error text.
- [ ] Structured log có đủ `error_kind`, `error_reason`, `error_cause` (internal).
- [ ] Pilot IAM admin auth flow dùng được spec này mà không đổi API payload public.
- [ ] Có test chứng minh `errors.Is`/fallback behavior cho case edge quan trọng.
