# App Error Envelope V1 - Implementation Plan

Upstream idea: `controlplane/docs/idea/app-error-envelope-full-idea.md`  
Canonical contract: `controlplane/docs/contract/app-error-envelope-canonical-contract.md`  
Behavior spec: `controlplane/docs/spec/app-error-envelope-v1-spec.md`

## 1) Mục tiêu triển khai
Triển khai shared error envelope (`Kind/Reason/Cause`) cho pilot IAM admin auth để chuẩn hóa error semantics nội bộ, không đổi public API payload. Done definition: service/handler forward được reason+cause theo boundary, status mapping vẫn dựa vào domain `Kind`, logs/metrics dùng reason ổn định, có test cho happy/error/edge.  
Out-of-scope: rollout toàn bộ module, thay đổi response schema public, thay đổi auth business flow ngoài phạm vi lỗi.

## 2) Current state vs target state
- Current:
  - Error flow chủ yếu trả `error` chung; handler map `errors.Is` theo module `errorx`.
  - Chưa có shared envelope chuẩn để bóc `reason`/`cause` xuyên tầng.
  - Log handler chưa có cấu trúc chuẩn `error_kind/error_reason/error_cause` cho pilot flow.
- Target:
  - Có shared package cho envelope dùng toàn app (`pkg/apperr`).
  - IAM service wrap lỗi bằng envelope trước khi trả handler.
  - Handler giữ generic response nhưng log được reason/cause có kiểm soát.
  - Test chứng minh `errors.Is` vẫn đúng, fallback behavior an toàn.

## 3) Implementation changes (grouped by subsystem)

### Handler
**Files (SỬA)**
- `internal/iam/transport/http/handler/admin_auth_handler.go`
- `pkg/logger/logger.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `(*AdminAuthHandler).Login` | SỬA | Chỉ log `err.Error()` chung khi unauthorized/internal | Extract envelope (nếu có) để log thêm `error_kind/error_reason/error_cause` (sanitized), response giữ generic | Tăng khả năng debug incident mà không đổi contract client |
| `(*AdminAuthHandler).Refresh` | SỬA | Log generic khi lỗi | Log theo envelope shape giống Login cho consistency | Đồng bộ observability giữa login/refresh |
| `(*AdminAuthHandler).Logout` | SỬA | Log generic khi lỗi | Log theo envelope shape giống Login cho consistency | Đồng bộ observability logout path |
| `logger.HandlerWarn` / `logger.HandlerError` (hoặc variant có fields) | SỬA | Chưa hỗ trợ thêm structured fields cho lỗi nghiệp vụ | Cho phép đính kèm field map nội bộ từ handler | Tránh ghép chuỗi log thủ công, giảm drift field name |

### Service
**Files (SỬA)**
- `internal/iam/service/admin_api_key_service.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `(*AdminAPIKeyService).AdminLogin` | SỬA | Trả domain error hoặc wrapped text theo từng nhánh, reason chưa chuẩn hóa toàn flow | Wrap lỗi theo envelope: `Kind` (module errorx), `Reason` stable, `Cause` primitive; giữ nguyên business outcome hiện có | Đảm bảo handler nhận đủ context để log và map status an toàn |
| `(*AdminAPIKeyService).RefreshAdminSession` | SỬA | Lỗi trả lên chưa thống nhất reason taxonomy | Chuẩn hóa reason/cause theo envelope cho các failure branch chính | Dễ phân tích refresh failure theo reason code |
| `(*AdminAPIKeyService).AdminLogout` | SỬA | Trả raw error từ cache/repo | Chuẩn hóa wrap để handler log theo format thống nhất | Quan sát lỗi logout rõ hơn, không leak detail |
| `(*AdminAPIKeyService).loadAdminTOTPSecret` | SỬA | Một số nhánh trả domain error trực tiếp, mất primitive cause | Giữ domain kind cũ nhưng attach cause + reason ổn định | Chẩn đoán MFA lỗi nhanh hơn, không đổi behavior client |

### Repo
**Files (KHÔNG ĐỔI ở v1)**
- `internal/iam/domain/repo/admin_api_key_repo.go`
- `internal/iam/repository/admin_api_key_repo.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | Repo trả primitive infra errors hiện hữu | Giữ nguyên; service chịu trách nhiệm gắn `Kind/Reason/Cause` trước khi lên handler | Boundary sạch: repo không quyết định domain class mapping |

### Middleware
**Files (KHÔNG ĐỔI)**
- Không thay đổi.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | N/A | N/A | Envelope không thay auth middleware path |

### Cache
**Files (KHÔNG ĐỔI)**
- `internal/iam/cache/*` (không sửa contract cache ở phase này)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | Cache runtime hiện hành | Giữ nguyên | Tránh mở rộng scope ngoài error envelope |

### Route
**Files (KHÔNG ĐỔI)**
- `internal/iam/route.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | Route/middleware chain hiện hữu | Giữ nguyên | Không ảnh hưởng API surface |

### Shared pkg
**Files (THÊM)**
- `pkg/apperr/app_error.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `type AppError` | THÊM | Chưa có shared envelope | Chuẩn hóa struct `Kind/Reason/Cause` | Tạo contract chung toàn app cho error forwarding |
| `func (e *AppError) Error()` | THÊM | N/A | Trả message có kind+reason, không ép lộ cause | Giữ semantics log-friendly và an toàn |
| `func (e *AppError) Unwrap()` | THÊM | N/A | Unwrap về `Kind` để `errors.Is` hoạt động | Giữ compatibility handler mapping hiện tại |
| `func Wrap(kind error, reason string, cause error) error` | THÊM | N/A | Helper tạo envelope thống nhất | Giảm duplicate và drift cách wrap lỗi |

### Docs
**Files (SỬA/THÊM)**
- `docs/idea/app-error-envelope-full-idea.md` (SỬA nếu cần delta note)
- `docs/contract/app-error-envelope-canonical-contract.md` (SỬA nếu phát sinh delta contract khi code)
- `docs/spec/app-error-envelope-v1-spec.md` (SỬA nếu acceptance chi tiết hơn)
- `docs/plan/app-error-envelope-v1-implementation-plan.md` (THÊM)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| App-error docs set | SỬA/THÊM | Có idea/contract/spec, chưa có plan triển khai | Có blueprint implementation decision-complete | Tránh drift giữa code và contract/spec |

### Tests
**Files (THÊM/SỬA)**
- `pkg/apperr/app_error_test.go` (THÊM)
- `internal/iam/test/svc_test/admin_api_key_service_error_test.go` (THÊM)
- `internal/iam/test/transport_test/admin_auth_handler_test.go` (SỬA)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `AppError` tests | THÊM | Chưa có test envelope | Test `Error`, `Unwrap`, `errors.Is`, nil-cause | Khóa correctness lớp shared |
| IAM service error tests | THÊM | Chưa verify reason/cause forwarding | Assert mỗi nhánh lỗi chính trả `Kind` đúng + reason ổn định + cause tương ứng | Chống regress mapping lỗi |
| Admin handler tests | SỬA | Test chủ yếu status/cookie | Bổ sung assert generic response + structured logging fields (qua hook/logger test helper) | Khóa security + observability behavior |

## 4) Contract changes
### Public/API contract
- No public contract change.
- Không thêm endpoint mới, không đổi request/response schema hiện hữu (`APPERR-API-001`).

### Internal contract
- Thêm internal shared contract artifact: `pkg/apperr` theo `APPERR-ERR-001`.
- Module-local `errorx` vẫn là source-of-truth của domain class (`Kind`) theo `APPERR-GOV-001`.

### Migration plan
- DB migration: không có cho v1 (`APPERR-DB-001`).
- Runtime migration: incremental adoption theo pilot IAM flow, không yêu cầu downtime.

### API changes
- Không có API surface change; chỉ đổi internal error forwarding/logging semantics.

## 5) Test plan + acceptance
### Required tests
- Happy path:
  - Login success không bị thay đổi response/cookie behavior hiện tại.
- Error path:
  - Sai API key/MFA vẫn trả `401 unauthorized` generic.
  - Lỗi dependency/internal trả `500 internal_error` generic.
- Edge path:
  - Envelope thiếu `Cause` vẫn map status đúng.
  - Envelope reason rỗng/invalid fallback theo module rule.
  - `errors.Is(appErr, kind)` luôn đúng với `Unwrap()`.

### Acceptance checklist (merge gate)
- [ ] Có `pkg/apperr` với `Kind/Reason/Cause`, `Wrap`, `Unwrap`.
- [ ] IAM pilot flow wrap lỗi theo reason stable, không dùng raw SQL text làm reason label.
- [ ] Handler map status theo `errors.Is(..., Kind)` không regress.
- [ ] Response client vẫn generic/safe, không leak cause.
- [ ] Log nội bộ có `error_kind/error_reason/error_cause` và đã redact thông tin nhạy cảm.
- [ ] Unit/transport tests mới pass, không phá test hiện hữu.

## 6) Rollout & operations
- Enable path:
  - Deploy code bình thường, không cần feature flag cho v1 pilot.
- Verify sau deploy:
  - Theo dõi login/refresh/logout warnings/errors và xác nhận presence của `error_kind/error_reason`.
  - Kiểm tra reason cardinality trong dashboard/log query không tăng đột biến do dynamic strings.
- Fallback behavior:
  - Nếu gặp regression logging/envelope, rollback về release trước (không cần rollback DB).
  - Trường hợp envelope parse/extract fail ở handler: fallback log generic + status mapping hiện hành.
- Rollback plan:
  - Revert commit pilot IAM + `pkg/apperr` integration.
  - Re-run smoke test admin login/logout để xác nhận behavior cũ ổn định.

## 7) Risk & mitigation
1. **Risk: reason cardinality tăng cao do code mới dùng text động.**  
   Mitigation: chỉ cho phép reason từ danh sách stable constants trong service tests.

2. **Risk: leak thông tin nhạy cảm qua `Cause` trong logs.**  
   Mitigation: sanitize log fields + test case cho secret/token/OTP không xuất hiện.

3. **Risk: regress mapping HTTP status vì wrap sai `Kind`.**  
   Mitigation: bắt buộc `Unwrap()->Kind`, test `errors.Is` cho các lỗi chính.

4. **Risk: mở rộng scope sang nhiều module gây kéo dài delivery.**  
   Mitigation: khóa scope pilot IAM trước, module khác xử lý ở phase sau.

5. **Risk: thay logger API ảnh hưởng call sites khác.**  
   Mitigation: giữ backward-compatible function cũ, chỉ thêm variant mới cho structured fields.
