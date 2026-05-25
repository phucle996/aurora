# Plan: Admin CIDR Policy from YAML v1

## 1. Bối cảnh và mục tiêu thay đổi

Hiện tại `admin_cidr` middleware đang nhận allowlist từ `cfg.Security.AdminAllowedCIDRs` trong bootstrap app. Mục tiêu thay đổi là chuyển nguồn dữ liệu sang policy YAML runtime qua `policyengine`, giữ nguyên logic middleware, chỉ đổi source input.

Mục tiêu chính:

- Không restart process khi đổi admin CIDR policy.
- Parse YAML bằng typed struct (không truyền `map[string]interface{}` qua caller boundary).
- Middleware `admin_cidr` chỉ nhận typed runtime var đã compile, không đổi logic enforce.

## 2. Phạm vi

### Trong phạm vi

- Đọc `policies.admin_cidr` từ policy snapshot runtime.
- Parse typed model từ YAML (`config-yaml/policies.go`) và nạp runtime variables generic cho caller dùng.
- Đổi app wiring: bỏ nguồn `cfg.Security.AdminAllowedCIDRs`, thay bằng source policyengine runtime.
- Giữ nguyên code logic trong `internal/http/middleware/admin_cidr.go`.

### Ngoài phạm vi

- Không thay đổi semantics auth/admin flow khác.
- Không thêm API route mới cho policy runtime.
- Không mở rộng sang rate-limit policy trong plan này.

## 3. Pre-Change Log

- `internal/http/middleware/admin_cidr.go`
  - CURRENT_CODE: middleware dùng danh sách CIDR đã compile trong package-level state.
  - CURRENT_SOURCE: caller khởi tạo qua `middleware.InitAdminCIDR(cfg.Security.AdminAllowedCIDRs)`.
- `internal/app/module.go`
  - CURRENT_CODE: init middleware lấy CIDR từ config tĩnh.
  - GAP: không dùng policyengine snapshot cho admin_cidr.
- `internal/policyengine/runtime/configyaml/policies.go`
  - CURRENT_CODE: đã có typed struct `PoliciesFile` và `AdminCIDRPolicy`.
  - GAP: chưa có flow compile typed admin_cidr runtime var cấp cho middleware.
- `internal/policyengine/runtime/engine_service.go`
  - CURRENT_CODE: parse YAML contract v1 và giữ snapshot runtime.
  - GAP: snapshot payload vẫn generic map; chưa expose typed accessor cho admin_cidr.
- Docs mismatch risk:
  - Spec đã chốt typed parse + không dùng map pass-through, code caller hiện vẫn đọc config tĩnh.

## 4. Naming Plan

- Typed runtime variables:
  - `CompiledAdminCIDRPolicy` (new): model đã chuẩn hóa để middleware dùng trực tiếp.
- Service accessor:
  - `GetAdminCIDRPolicy(ctx)` (new) trên policyengine service interface hoặc adapter layer trung gian.
- YAML parse model giữ nguyên:
  - `PoliciesFile`, `PoliciesRuntimeRoot`, `AdminCIDRPolicy`.
- Rename decisions:
  - Không rename middleware symbols để tránh tác động rộng.

## 5. File-Scoped Action Plan (gộp file + function)

- `internal/policyengine/runtime/engine_service.go` (layer: service contract)
  - `type EngineService`
    - Current state: `Start`, `Current`, `Reload`.
    - Planned action: **keep** API generic, không thêm method nghiệp vụ theo middleware cụ thể.
    - Expected behavior: policyengine không biết domain admin_cidr ở tầng service contract.

- `internal/policyengine/runtime/types/policy.go` (layer: domain entity)
  - `type PolicySet`
    - Planned action: **update** để chứa runtime typed variables generic (đã parse/compile), không tạo getter theo module consumer.
    - Expected behavior: caller đọc biến typed từ snapshot, không parse CIDR per-request.

- `internal/policyengine/runtime/engine_service.go` (layer: service impl)
  - `func parsePoliciesYAML(...)` (new)
    - Planned action: **add** hàm chuyên parse YAML -> typed struct `PoliciesFile`.
    - Expected behavior: lock contract parse theo model typed, không dùng map generic.
  - `func compileRuntimeVariables(...)` (new)
    - Planned action: **add** hàm chuyên compile typed struct -> runtime variables dùng cho caller/middleware.
    - Expected behavior: parse/compile một lần trên reload, không parse lại ở hot path.
  - `func loadPolicySnapshotFromSource(...)`
    - Planned action: **update** gọi `parsePoliciesYAML` + `compileRuntimeVariables`, validate `allowlist` non-empty.
    - Expected behavior: reject snapshot invalid, giữ last-known-good.
  - `func Reload(ctx)`
    - Planned action: **update** sau parse thành công thì compile `CompiledAdminCIDRPolicy` và lưu cùng snapshot runtime.

- `internal/policyengine/policies.go` (layer: config model)
  - `type PoliciesFile`, `type AdminCIDRPolicy`
    - Planned action: **keep**; nếu thiếu field default/mode enum thì bổ sung validation hook ở service.

- `internal/http/middleware/admin_cidr.go` (layer: middleware)
  - `func InitAdminCIDR(...)`
    - Planned action: **no logic change**; chỉ đổi input source từ app wiring.
    - Expected behavior: middleware giữ nguyên enforcement path hiện tại.

- `internal/app/module.go` (layer: app bootstrap)
  - `func initMiddlewares(...)`
    - Current state: gọi `InitAdminCIDR(cfg.Security.AdminAllowedCIDRs)`.
    - Planned action: **update** lấy typed runtime variable đã nạp trong snapshot rồi truyền vào `InitAdminCIDR`.
    - Ordering before/after:
      - Before: config -> middleware init
      - After: policyengine snapshot(runtime variables) -> middleware init
    - Fail-fast: nếu không lấy được typed policy hợp lệ khi bootstrap -> return error để app dừng.

- `internal/policyengine/module.go` (layer: module wiring)
  - `func NewModule(...)`
    - Planned action: **ensure** worker start trước khi app init middleware gọi accessor lần đầu.
    - Expected behavior: bootstrap order không race với snapshot availability.

- `internal/policyengine/docs/spec/spec-admin-cidr-policy-from-yaml-v1.md` (layer: docs)
  - Planned action: **sync** nếu có đổi signature accessor hoặc runtime model naming.

## 7. Contract & Boundary Checks

- Boundary chain:
  - app bootstrap -> policyengine service typed accessor -> middleware init.
- Contract checks:
  - Caller không truyền `map[string]interface{}`.
  - `allowlist` bắt buộc non-empty.
  - Không thêm service method nghiệp vụ theo middleware cụ thể (ví dụ `GetAdminCIDRPolicy`).
  - middleware logic không đổi (chỉ đổi input source).
- Fail behavior:
  - bootstrap không có admin_cidr typed policy hợp lệ -> fail-fast app init.
- Leakage checks:
  - Không đưa YAML parsing logic vào middleware.
  - Không đưa config struct transport concern vào domain entity.

## 8. Risk / Impact Analysis

- Risk 1: bootstrap race (middleware init trước snapshot ready).
  - Mitigation: bootstrap order rõ + accessor fail-fast.
- Risk 2: invalid YAML làm app không lên sau restart.
  - Mitigation: CI policy validation + last-known-good khi runtime reload.
- Risk 3: drift giữa spec typed contract và code parse.
  - Mitigation: test parse mapping + update spec khi đổi signature.
- Risk 4: behavior change ngoài ý muốn trong middleware.
  - Mitigation: không sửa logic middleware, chỉ thay input source.

## 9. Verification Plan

- Service checks:
  - Parse YAML -> typed struct pass với sample hợp lệ.
  - `allowlist` rỗng -> reject đúng error.
  - `GetAdminCIDRPolicy` trả typed model non-empty blocks.
- Middleware checks:
  - Không thay đổi code path xử lý deny/allow so với baseline.
  - Input source mới vẫn enforce đúng CIDR list.
- Bootstrap checks:
  - `initMiddlewares` không dùng `cfg.Security.AdminAllowedCIDRs` nữa.
  - Fail-fast nếu policyengine chưa có typed policy hợp lệ.
- Observability checks:
  - reload success/fail logs vẫn đúng op contract.
  - checksum/version log phản ánh policy đang active.
- Success criteria:
  - Admin CIDR enforce theo YAML sau reload mà không restart.
  - Không có `map[string]interface{}` pass-through ở caller boundary.

## 10. Rollback Plan

- Rollback code:
  - tạm trả source admin_cidr về `cfg.Security.AdminAllowedCIDRs` tại app bootstrap.
- Rollback runtime:
  - re-apply `policies.yaml` revision trước đó nếu policy mới invalid.
- Rollback safety:
  - giữ middleware logic cũ nên rollback chỉ là đổi source input.

## 11. Open Questions

- Typed accessor placement:
  - expose trực tiếp trên `EngineService` hay qua adapter mỏng ở app-layer?
- Bootstrap readiness:
  - khi policyengine chưa load lần đầu, app fail ngay hay retry ngắn trước khi fail?
- `mode=monitor`:
  - ở phase này có bật monitor branch cho admin_cidr hay giữ enforce-only như hiện tại?
