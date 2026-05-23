# IAM Outcome Source-of-Truth V1 - Specification

Upstream references:
- `controlplane/internal/iam/docs/spec/iam-error-envelope-rollout-v1-spec.md`
- `controlplane/docs/spec/app-error-envelope-v1-spec.md`
- `controlplane/docs/contract/app-error-envelope-canonical-contract.md`

## 1) Purpose + Scope
### Purpose
Chuẩn hóa toàn bộ `outcome` label trong IAM thành một nguồn sự thật duy nhất (single source of truth) để:
- loại bỏ hardcode string rải rác trong service,
- giữ thống nhất giữa metrics/log/trace,
- giảm drift khi thêm nhánh lỗi hoặc refactor flow.

### In-scope
- Outcome labels cho các flow IAM đang emit metrics:
  - register/login/refresh token,
  - admin login/admin refresh/admin rotation.
- Một file IAM duy nhất định nghĩa constants outcome.
- Quy tắc sử dụng outcome ở service layer.

### Out-of-scope
- Không đổi request/response API public.
- Không đổi domain error `Kind/Reason/Cause` contract.
- Không đổi business decision của các flow auth/rbac/admin.

## 2) Terminology / Actors
- **Outcome**: label kết quả thực thi của một flow, phục vụ observability.
- **Outcome catalog**: tập constants outcome hợp lệ theo từng flow, đặt tại `internal/iam/metrics/outcome.go`.
- **Flow owner**: service sở hữu lifecycle của outcome cho flow đó.
- **Metrics sink**: `internal/iam/metrics/*` nơi consume outcome để tăng counter/histogram.
- **Reason**: error taxonomy từ `internal/iam/errorx/reason.go`; khác với outcome nhưng phải cùng semantics mức coarse-grained.

## 3) API Contract
### Public API contract
- Không có endpoint/method/schema nào thay đổi.
- Không đổi cookie/header/status hiện tại của IAM handlers.

### Status code semantics
- Không thay đổi mapping status code hiện có.
- Outcome chỉ ảnh hưởng observability, không ảnh hưởng response contract.

## 4) Flow Behavior
### Main flow
1. Service khởi tạo `outcome = success` cho flow.
2. Mỗi nhánh validation/dependency/business failure update outcome bằng constant trong outcome catalog.
3. Khi kết thúc flow (defer), service MUST emit metrics theo outcome final.
4. Handler vẫn map response theo `Kind` hiện có; outcome không tham gia HTTP mapping.

### Error/failure branches
- Nếu flow trả error, outcome MUST phản ánh nhánh lỗi coarse-grained tương ứng.
- Outcome MUST không chứa thông tin động (SQL text, user input, token, id cụ thể).

### Preconditions
- Outcome catalog file tồn tại và được import bởi service tương ứng.
- Metrics functions chấp nhận label string như hiện tại.

### Postconditions
- Không còn hardcoded outcome string trực tiếp trong IAM service thuộc phạm vi rollout.
- Tất cả outcome emit ra metrics đều thuộc bounded set từ catalog.

## 5) Data & Boundary Rules
- Source of truth cho outcome: `internal/iam/metrics/outcome.go`.
- Boundary bắt buộc:
  - Service: chọn và set outcome.
  - Metrics package: định nghĩa catalog constants + consume outcome cho metrics emit.
  - Handler/Repo/Cache: không tự định nghĩa outcome cho service flow.
- Consistency rule:
  - Cùng một nhánh semantics MUST dùng cùng một outcome constant xuyên suốt flow.

## 6) Security Rules
- Outcome labels MUST là static constants; cấm format string động.
- Outcome labels MUST không chứa secret/token/password/otp/api-key/user input.
- Log và trace có thể gắn `outcome`, nhưng không gắn raw sensitive data vào outcome value.

## 7) Failure Semantics
- Outcome catalog missing hoặc import sai là lỗi build-time (fail-fast).
- Khi gặp nhánh lỗi chưa map explicit, flow SHOULD fallback về một outcome dependency/internal coarse-grained đã định nghĩa trong catalog của flow.
- Không có retry policy mới trong spec này; retry behavior của từng flow giữ nguyên.

## 8) Non-functional Baseline
- Không tăng dependency ngoài module IAM.
- Không đổi baseline latency đáng kể (chỉ thay hardcoded string bằng constant lookup compile-time).
- Metrics cardinality MUST giữ bounded theo catalog, không phát sinh label explosion.

## 9) Acceptance Criteria
- [ ] Có đúng 1 file IAM chứa catalog outcome constants cho các flow trong scope.
- [ ] Các service trong scope không hardcode outcome string trực tiếp.
- [ ] Metrics outcome labels trước/sau rollout không tăng cardinality ngoài catalog.
- [ ] Không đổi status code/response contract của IAM endpoints.
- [ ] Test svc/transport quan trọng vẫn pass và có test guard cho outcome constants usage (ít nhất cho auth/refresh/admin login).
