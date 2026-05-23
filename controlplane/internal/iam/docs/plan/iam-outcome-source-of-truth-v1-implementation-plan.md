# IAM Outcome Source-of-Truth V1 - Implementation Plan

Derived spec: `controlplane/internal/iam/docs/spec/iam-outcome-source-of-truth-v1-spec.md`

## 1) Mục tiêu triển khai
Chuẩn hóa outcome labels IAM về một nguồn sự thật duy nhất để giảm drift observability và bỏ hardcode string rải rác trong service. Done definition: có một catalog constants, các service trong scope dùng constants đó, test quan trọng vẫn pass, không đổi API contract. Out-of-scope: không đổi business logic auth/rbac/admin, không đổi HTTP status mapping, không rollout cross-module ngoài IAM.

## 2) Current state vs target state
### Current state
- Outcome strings đang hardcode rải rác trong:
  - `internal/iam/service/auth_service.go`
  - `internal/iam/service/refresh_token_service.go`
  - `internal/iam/service/admin_api_key_service.go`
- Không có file catalog outcome tập trung cho IAM.
- Drift risk cao khi rename/add branch outcome.

### Target state
- Có 1 file catalog outcome duy nhất trong IAM để làm source-of-truth.
- Service trong scope chỉ dùng constants từ catalog thay vì hardcode literal.
- Metrics emit behavior giữ nguyên semantics, chỉ đổi nguồn label.

## 3) Implementation changes (grouped by subsystem)

### Handler
**Files (KHÔNG ĐỔI)**
- `internal/iam/transport/http/handler/*.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | Handler không quản lý outcome flow label | Không đổi | Không ảnh hưởng API client |

### Service
**Files (SỬA)**
- `internal/iam/service/auth_service.go`
- `internal/iam/service/refresh_token_service.go`
- `internal/iam/service/admin_api_key_service.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `RegisterAccount` | SỬA | `result` hardcode string literal | Dùng constants từ outcome catalog | Đồng bộ label register outcome |
| `Login` | SỬA | `loginOutcome` hardcode string literal | Dùng constants từ outcome catalog | Đồng bộ label login outcome |
| `Refresh` (refresh token) | SỬA | `refreshOutcome` hardcode string literal | Dùng constants từ outcome catalog | Đồng bộ label refresh outcome |
| `AdminLogin` | SỬA | `loginOutcome` hardcode string literal | Dùng constants từ outcome catalog | Đồng bộ label admin-login outcome |
| `RefreshAdminSession` | SỬA | `refreshOutcome` hardcode string literal | Dùng constants từ outcome catalog | Đồng bộ label admin-refresh outcome |
| `runAdminRotationScheduler` caller flow | SỬA | dùng literal rải rác ở scheduler/service metrics | Dùng constants từ outcome catalog | Đồng bộ label admin-rotation outcome |

### Metrics
**Files (THÊM)**
- `internal/iam/metrics/outcome.go` (THÊM)

**Files (SỬA)**
- `internal/iam/metrics/metrics_auth.go` (SỬA)
- `internal/iam/metrics/metrics_admin.go` (SỬA)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Outcome catalog constants | THÊM | Chưa có file catalog outcome tập trung | Tạo `metrics/outcome.go` làm SoT cho outcome labels | Loại bỏ hardcode rải rác |
| Observe*Outcome compatibility | SỬA | Nhận string literal từ service | Nhận constants từ outcome catalog | Giữ behavior metrics, giảm drift |

### Repo
**Files (KHÔNG ĐỔI)**
- `internal/iam/repository/*.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | Repo không sở hữu outcome | Không đổi | Giữ boundary raw-first |

### Middleware
**Files (KHÔNG ĐỔI)**
- `internal/http/middleware/*.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | Access middleware không thuộc IAM service outcome | Không đổi | Không đổi access log contract |

### Cache
**Files (KHÔNG ĐỔI)**
- `internal/iam/cache/*.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | Cache chỉ trả runtime data/error | Không đổi | Giữ ownership ở service |

### Route
**Files (KHÔNG ĐỔI)**
- `internal/iam/route.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | Route không chứa outcome taxonomy | Không đổi | Không đổi API surface |

### Docs
**Files (THÊM)**
- `internal/iam/docs/spec/iam-outcome-source-of-truth-v1-spec.md` (THÊM)
- `internal/iam/docs/plan/iam-outcome-source-of-truth-v1-implementation-plan.md` (THÊM)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Outcome SoT docs | THÊM | Chưa có spec/plan dedicated cho outcome catalog | Có spec + execution blueprint rõ | Giảm scope drift khi code |

### Tests
**Files (SỬA)**
- `internal/iam/test/svc_test/auth_service_test.go` (SỬA)
- `internal/iam/test/svc_test/refresh_token_service_test.go` (SỬA)
- `internal/iam/test/svc_test/admin_api_key_service_error_test.go` (SỬA)

**Files (THÊM)**
- `internal/iam/test/svc_test/outcome_catalog_test.go` (THÊM)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Auth/Refresh/Admin svc tests | SỬA | Assert logic chính, chưa guard drift outcome catalog | Assert outcome constants expected trên nhánh chính | Chặn regress rename literal |
| Outcome catalog test | THÊM | Không có guard tính bounded của catalog | Validate constants non-empty + uniqueness theo flow | Chặn duplicate/empty label |

## 4) Contract changes
### Public contract
- **No public contract change**.

### Internal contract
- THÊM 1 file catalog outcome trong IAM module tại `internal/iam/metrics/outcome.go`.
- Service trong scope MUST dùng constants từ catalog, không hardcode literal.

### Endpoint/DTO/Entity/Repo interface/Error mapping
- Endpoint/DTO/Entity/Repo interface: KHÔNG ĐỔI.
- Error mapping (`Kind/Reason/Cause`): KHÔNG ĐỔI semantics.

## 5) Test plan + acceptance
### Required tests
- Happy path:
  - login/refresh/admin login/admin refresh vẫn emit success outcome đúng constant.
- Error path:
  - dependency/auth-invalid/token-issue branches emit đúng outcome constants hiện hành.
- Edge path:
  - constants không rỗng,
  - không duplicate trong cùng flow catalog,
  - không còn hardcoded literal ở flow đã migrate (kiểm tra bằng targeted grep trong CI hoặc test helper).

### Acceptance checklist (merge gate)
- [ ] Có đúng 1 file catalog outcome IAM.
- [ ] Auth/Refresh/Admin service đã thay literal outcome bằng constants.
- [ ] Không đổi API status/response contract.
- [ ] `go test ./internal/iam/test/svc_test ./internal/iam/test/transport_test` pass.
- [ ] Không còn hardcoded outcome literal trong các function thuộc scope rollout.

## 6) Rollout & operations
- Enable path:
  - Merge code + chạy test suite IAM target.
- Required config:
  - Không yêu cầu config mới.
- Fallback behavior:
  - Revert riêng commit outcome catalog migration nếu phát hiện metric dashboard mismatch.
- Monitoring/log signals:
  - Query Prometheus/Grafana theo label `result` trước/sau rollout để xác nhận không tăng cardinality.
  - Theo dõi tỷ lệ `unknown`/fallback outcome (nếu có) phải bằng 0.

## 7) Risk & mitigation
1. **Risk: đổi tên constant làm vỡ dashboard query cũ.**  
   Mitigation: giữ nguyên semantic label hiện tại trong V1; không rename tùy tiện.

2. **Risk: migrate thiếu một nhánh outcome trong service.**  
   Mitigation: thêm checklist grep literal outcome + test guard cho flow chính.

3. **Risk: trộn ownership outcome giữa service và metrics package.**  
   Mitigation: khóa rule: metrics chỉ consume string, không define catalog.

4. **Risk: mở rộng scope sang handler/repo gây churn không cần thiết.**  
   Mitigation: giữ strict scope service + docs + tests trong phase này.

5. **Risk: trùng/empty constants trong catalog.**  
   Mitigation: thêm `outcome_catalog_test.go` để assert uniqueness/non-empty.
