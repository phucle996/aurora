# IAM RBAC V1 - Specification

## 1) Purpose + Scope

### Purpose
Định nghĩa behavior source-of-truth cho RBAC V1 trong IAM:
- authorize theo permission,
- quản trị role/permission/binding,
- bảo đảm consistency trong môi trường HA.

### In-scope
- Authorization decision semantics (`allow|deny|error`).
- Permission taxonomy và evaluation rule.
- Runtime cache + invalidate + self-heal contract (mức hành vi).
- Security/failure semantics cho RBAC.

### Out-of-scope
- SQL schema chi tiết.
- HTTP DTO chi tiết từng endpoint.
- Phân quyền tổ chức phức tạp (ABAC/ReBAC).

---

## 2) Terminology / Actors

### Actors
- **Principal**: admin user/service actor cần authorize.
- **AuthZ Middleware/Guard**: gọi RBAC check tại request path.
- **RBAC Service**: evaluate quyền, điều phối cache/invalidation.
- **RBAC Repository**: source-of-truth qua DB.
- **Redis Runtime**: cache shared + invalidate bus.

### Terms
- **Permission**: quyền atomic theo format `<domain>.<resource>.<action>`.
- **Role**: tập permission có ý nghĩa vận hành.
- **Binding**: gán role cho principal.
- **Policy Context**: context runtime bổ sung cho authorize decision.

---

## 3) Authorization Contract

### Decision Rule
- Mặc định **deny**.
- **Allow** khi principal có ít nhất 1 role chứa permission yêu cầu.
- Context constraints (nếu có) có thể ép deny dù permission match.

### Behavioral Input
- `principal_id` (bắt buộc)
- `permission` (bắt buộc)
- `policy_context` (tùy chọn)

### Behavioral Output
- `allow`: principal đủ quyền.
- `deny`: principal không đủ quyền hoặc context không thỏa.
- `error`: hệ thống không evaluate được (dependency/runtime failure).

### HTTP Mapping Baseline
- `deny` -> `403` generic.
- `error` -> `5xx` generic.
- Không leak lý do nội bộ ra client.

---

## 4) Permission and Role Rules

- Permission naming bắt buộc theo `<domain>.<resource>.<action>`.
- Handler/middleware check theo permission, không check role string trực tiếp.
- Role chỉ là grouping layer; không là API contract công khai cho client.
- Role mutation phải làm mới cache theo invalidation semantics.

### Scope invariant (tenant/workspace)

- `platform` scope:
  - `tenant_id = null`
  - `workspace_id = null`
- `tenant` scope:
  - `tenant_id != null`
  - `workspace_id = null`
- `workspace` scope:
  - `tenant_id != null` **bắt buộc**
  - `workspace_id != null` **bắt buộc**

Nghĩa nghiệp vụ:
- Workspace luôn là thực thể con của tenant (tổ chức).
- Không tồn tại workspace role “mồ côi” ngoài tenant.
- Quyền ở workspace là quyền trong một tổ chức cụ thể, và tổ chức đó quyết định ai được làm gì ở workspace nào.

---

## 5) Runtime Behavior

### Main authorize flow
1. Nhận authorize request (`principal`, `permission`, `context`).
2. Resolve role set của principal từ runtime cache.
3. Cache miss thì load từ DB source-of-truth và populate cache.
4. Evaluate permission theo decision rule.
5. Trả `allow|deny|error` + reason nội bộ cho observability.

### Mutation flow (role/permission/binding)
1. Persist mutation vào DB.
2. Invalidate cache liên quan (role-specific hoặc all theo policy).
3. Publish invalidate event cho replicas.
4. Replica consume event và evict cache local.

---

## 6) Cache + Consistency Semantics

- DB là source-of-truth cuối cùng.
- Runtime cache được phép eventual consistency ngắn.
- Invalidation event là primary path để đồng bộ nhanh.
- Periodic sync/checkpoint là secondary path để tự-heal khi miss event.
- Khi phát hiện drift version/epoch, hệ thống phải invalidate theo policy an toàn.

---

## 7) Security Rules

- Deny-by-default.
- Least privilege.
- Không log secret/token/runtime fragment.
- Không dựa vào client-supplied role claims làm source-of-truth.
- RBAC mutation phải có audit trail.

---

## 8) Failure Semantics

- DB read fail khi authorize -> `error` (không fail-open).
- Redis/cache fail -> fallback DB read nếu policy cho phép; nếu không evaluate được thì `error`.
- Invalidation publish fail không được làm rollback DB mutation, nhưng phải log/metric và để sync loop tự-heal.
- Unknown runtime error luôn fail-closed cho path authorization.

---

## 9) Observability Baseline

### Metrics
- `iam_rbac_authorize_total{result}`
- `iam_rbac_authorize_deny_total{reason}`
- `iam_rbac_cache_hit_total{layer}`
- `iam_rbac_cache_miss_total{layer}`
- `iam_rbac_invalidation_total{kind}`
- `iam_rbac_sync_total{result}`

### Logging
Structured log fields tối thiểu:
- `request_id`, `principal_id`, `permission`, `result`, `reason`, `cache_layer`.

### Audit
Audit event cho mutation và critical deny:
- actor, action, target, decision, timestamp, correlation id.

---

## 10) Acceptance Criteria

- [ ] Authorization dùng permission-first, deny-by-default.
- [ ] RBAC mutation làm invalidate cache theo contract.
- [ ] Replica sync đảm bảo tự-heal khi miss invalidate event.
- [ ] Không fail-open khi dependency lỗi.
- [ ] Metrics/log/audit đủ để incident debugging.
