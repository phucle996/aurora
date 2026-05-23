# IAM Error Envelope Rollout V1 - Implementation Plan

Upstream global spec: `controlplane/docs/spec/app-error-envelope-v1-spec.md`  
IAM behavior spec: `controlplane/internal/iam/docs/spec/iam-error-envelope-rollout-v1-spec.md`

## 1) Mục tiêu triển khai
Triển khai đồng bộ `apperr` cho toàn bộ IAM module (không chỉ admin API key), chuẩn hóa `Kind/Reason/Cause` từ service lên handler để logging + HTTP mapping nhất quán. Done definition: các flow auth/refresh/ott/rbac/admin đều có envelope parity, reason taxonomy đầy đủ, handler không leak cause, test chính pass.  
Out-of-scope: thay đổi payload public, đổi business logic auth/rbac, hoặc refactor cross-module ngoài IAM.

## 2) Current state vs target state
### Current state
- `AdminAPIKeyService` đã dùng `apperr` cho phần lớn nhánh lỗi.
- `AuthService`, `RefreshTokenService`, `OneTimeTokenService`, `RbacService` chưa đồng bộ envelope (nhiều nhánh còn trả raw/domain error trực tiếp).
- `reason.go` thiên về admin reasons, chưa bao phủ auth/refresh/ott/rbac.
- Repo layer vẫn lẫn một số domain error mapping (đặc biệt RBAC repo) thay vì trả raw nhất quán.

### Target state
- Toàn bộ IAM service public flows trả lỗi theo envelope chuẩn.
- `reason.go` có taxonomy bounded cho tất cả IAM flows.
- Handler IAM map status theo `Kind` ổn định, log có `error_kind/error_reason/error_cause`.
- Repo giữ raw technical error; service chịu trách nhiệm map domain kind/reason.

## 3) Implementation changes (grouped by subsystem)

### Handler
**Files (SỬA)**
- `internal/iam/transport/http/handler/auth_handler.go`
- `internal/iam/transport/http/handler/refresh_token_handler.go`
- `internal/iam/transport/http/handler/rbac_handler.go`
- `internal/iam/transport/http/handler/admin_auth_handler.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `(*AuthHandler).RegisterAccount` | SỬA | Map status chủ yếu từ `errors.Is` trên lỗi cũ | Giữ mapping hiện tại, nhưng logging tận dụng envelope fields khi có | Không đổi response, tăng observability |
| `(*AuthHandler).Login` | SỬA | Có mapping theo kind nhưng thiếu reason/cause parity từ service | Giữ mapping hiện tại, log đầy đủ kind/reason/cause | Dễ triage login incident |
| `(*RefreshTokenHandler).Refresh` | SỬA | Mapping invalid/auth unavailable đã có | Giữ mapping, chuẩn hóa nhánh fallback theo envelope kind | Tránh drift semantics khi service nâng cấp |
| `(*RbacHandler).*` | SỬA | Một số branch map nội bộ chưa phân biệt invalid/notfound/dependency rõ | Chuẩn hóa mapping theo kind được service trả lên | Tránh 500 dư thừa ở lỗi business |
| `(*AdminAuthHandler).RotateKey` | SỬA | Đã fix lock busy mapping, giữ parity với envelope | Không đổi contract, giữ mapping nhất quán theo kind | Không regress admin flow |

### Service
**Files (SỬA)**
- `internal/iam/service/auth_service.go`
- `internal/iam/service/refresh_token_service.go`
- `internal/iam/service/one_time_token_service.go`
- `internal/iam/service/rbac_service.go`
- `internal/iam/service/admin_api_key_service.go` (parity pass + cleanup)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `(*AuthService).RegisterAccount` | SỬA | Trả mix raw error / domain error / fmt.Errorf | Mọi nhánh dependency/token/hash/id/persist map qua `apperr.Wrap` với reason ổn định | Log/metric phân nhóm rõ ràng |
| `(*AuthService).Login` | SỬA | Một số nhánh trả raw (`loadErr`, `idErr`, `refreshErr`, `persistErr`) | Map tất cả nhánh sang kind phù hợp + reason + cause | Không còn raw leak qua handler log |
| `(*RefreshTokenService).Refresh` | SỬA | Trả nhiều raw errors trực tiếp | Chuẩn hóa wrap theo invalid_session/auth_unavailable/token_issue/dependency | HTTP mapping ổn định hơn |
| `(*OneTimeTokenService).Issue/Consume` | SỬA | Chỉ trả domain kind, thiếu cause/reason | Wrap nhánh cache/generate/dependency error để có cause nội bộ | Quan sát lỗi cache tốt hơn |
| `(*RbacService).LoadRole/...` | SỬA | Có nhánh `fmt.Errorf` và pass-through repo error | Map chuẩn not_found/invalid/dependency + reason taxonomy RBAC | Giảm lỗi 500 không phân loại |
| `(*AdminAPIKeyService)` | SỬA | Đã gần chuẩn, còn parity check theo taxonomy | Bổ sung reason nhất quán và loại bỏ nhánh raw còn sót | Giữ chuẩn đồng nhất toàn IAM |

### Repo
**Files (SỬA)**
- `internal/iam/repository/auth_repo.go`
- `internal/iam/repository/refresh_token_repo.go`
- `internal/iam/repository/rbac_repo.go`
- `internal/iam/repository/admin_api_key_repo.go` (chỉ parity/raw preservation nếu còn)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Repo query/tx methods | SỬA | Có chỗ trả domain errors ngay tại repo (đặc biệt RBAC) + wrapped text không đồng nhất | Chuẩn hóa: repo trả raw technical/not-found parse outcomes; không map HTTP semantics | Boundary sạch service-repo |
| TX begin/commit/rollback branches | SỬA | Wrap text chưa chuẩn theo nhóm lỗi | Giữ raw cause đầy đủ để service map | Dễ truy nguyên lỗi transaction |
| Parse input branches (`uuid.Parse`, ...) | SỬA | Có chỗ trả `ErrInvalidArgument` ngay ở repo | Trả parse error raw để service map invalid-argument kind | Tránh repo dính domain class |

### Middleware
**Files (KHÔNG ĐỔI)**
- `internal/http/middleware/*`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | Middleware contract hiện tại ổn | Không đổi trong phase này | Giữ scope tập trung IAM service/handler/repo |

### Cache
**Files (SỬA tối thiểu)**
- `internal/iam/cache/*` (chỉ nơi cần expose lỗi raw rõ hơn)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Cache error returns | SỬA | Một số branch cache error khó phân biệt nhóm | Chuẩn hóa lỗi cache infra raw để service wrap | Reason mapping chính xác hơn |

### Route
**Files (KHÔNG ĐỔI)**
- `internal/iam/route.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | Endpoint và middleware order đã ổn | Không đổi API surface | Không ảnh hưởng client |

### Docs
**Files (THÊM/SỬA)**
- `internal/iam/docs/spec/iam-error-envelope-rollout-v1-spec.md` (THÊM)
- `internal/iam/docs/plan/iam-error-envelope-rollout-v1-implementation-plan.md` (THÊM)
- `internal/iam/docs/contract/*` (SỬA nếu cần delta taxonomy contract)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| IAM error docs set | THÊM/SỬA | Chưa có spec/plan dedicated cho rollout toàn IAM | Có source-of-truth để code theo phase | Giảm drift khi rollout lớn |

### Tests
**Files (THÊM/SỬA)**
- `internal/iam/test/svc_test/*`
- `internal/iam/test/transport_test/*`
- `pkg/apperr/app_error_test.go` (SỬA nếu cần coverage)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Auth service error tests | THÊM | Chưa khóa reason/cause cho auth login/register | Assert kind/reason/cause theo branch | Chống regress error mapping |
| Refresh token error tests | THÊM | Chưa khóa envelope mapping | Assert invalid_session vs dependency vs auth_unavailable | Đảm bảo status contract |
| OTT error tests | THÊM | Chưa có reason/cause matrix | Assert cache unavailable/generate fail mapping | Ổn định observability |
| RBAC service+handler tests | THÊM/SỬA | Chưa kiểm soát boundary error mapping | Assert repo raw -> service kind -> handler status | Chuẩn hóa layer contract |
| Admin flow regression tests | SỬA | Đã có một phần | Bổ sung parity theo taxonomy mới | Không vỡ flow đã ổn định |

## 4) Contract changes
### Public contract
- **No public contract change**.
- Không đổi endpoint, request, response schema.

### Internal contract
- Mở rộng `internal/iam/errorx/reason.go` cho auth/refresh/ott/rbac groups.
- Chuẩn hóa quy tắc: repo raw error, service wrap, handler map status.

### Migration/API changes
- DB migration: không bắt buộc.
- API change: không có public API change.

## 5) Test plan + acceptance
### Required tests
- Happy paths:
  - register/login/refresh/admin login/admin refresh/rbac CRUD không đổi business result.
- Error paths:
  - dependency errors map đúng kind nội bộ và đúng HTTP status hiện có.
  - invalid credentials/session vẫn generic như trước.
- Edge paths:
  - empty reason fallback,
  - nil cause,
  - nested wrap vẫn `errors.Is` đúng.

### Acceptance checklist (merge gate)
- [ ] Auth/Refresh/OTT/RBAC/Admin service đều trả envelope cho các nhánh lỗi đi ra handler.
- [ ] `reason.go` có taxonomy bounded đầy đủ cho IAM major flows.
- [ ] Repo không thêm business HTTP semantics mới.
- [ ] Handler không leak cause ra response.
- [ ] Log fields `error_kind/error_reason/error_cause` xuất hiện đúng trên IAM lỗi chính.
- [ ] Targeted svc/transport tests pass.

## 6) Rollout & operations
- Enable path:
  - rollout theo phase service-first, sau đó handler mapping harden, cuối cùng test expansion.
- Deploy verify:
  - query log theo `log_type=handler` + `error_reason` để kiểm tra taxonomy coverage.
  - theo dõi tỷ lệ 401/403/500/503 trước-sau để phát hiện mapping drift.
- Fallback:
  - nếu regress, revert theo từng subsystem commit (service/handler/repo) vì không có DB migration.
- Runtime signals:
  - alert khi `error_reason` ngoài dictionary expected xuất hiện.

## 7) Risk & mitigation
1. **Risk: đổi mapping kind làm lệch status cũ ở một số endpoint.**  
   Mitigation: giữ bảng status hiện tại, thêm test transport snapshot cho từng endpoint.

2. **Risk: reason taxonomy phình to (cardinality cao).**  
   Mitigation: chỉ dùng coarse-grained reason constants; cấm dynamic reason.

3. **Risk: repo refactor lớn gây side-effect logic.**  
   Mitigation: làm theo phase, ưu tiên service wrap trước; repo chỉ chỉnh nơi vi phạm boundary rõ ràng.

4. **Risk: mất raw cause khi wrap không đúng.**  
   Mitigation: test assert `apperr.As(err).Cause` ở các dependency branches quan trọng.

5. **Risk: rollout chậm vì phạm vi IAM lớn.**  
   Mitigation: chia 3 wave triển khai:
   - Wave 1: Auth + Refresh + OTT,
   - Wave 2: RBAC,
   - Wave 3: Admin parity + cleanup + full test matrix.
