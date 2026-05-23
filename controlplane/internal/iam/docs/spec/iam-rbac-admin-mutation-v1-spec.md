# IAM RBAC Admin Mutation V1 - Specification

## 1) Purpose + Scope

### Purpose
Chuẩn hóa hành vi mutation RBAC bởi admin:
- quản trị role,
- gán/thu hồi permission cho role,
- gán/thu hồi role cho principal.

### In-scope
- Mutation semantics ở mức hành vi.
- Validation baseline.
- Post-mutation cache/audit semantics.

### Out-of-scope
- DTO/endpoint chi tiết.
- UI/UX admin console.

---

## 2) Mutation Operations

- `CreateRole`
- `UpdateRole`
- `DeleteRole`
- `AssignPermissionToRole`
- `RevokePermissionFromRole`
- `AssignRoleToPrincipal`
- `RevokeRoleFromPrincipal`

---

## 3) Validation Rules

- Tên role phải unique trong scope định nghĩa.
- Permission phải hợp lệ theo taxonomy chuẩn.
- Không cho binding tới principal không tồn tại.
- Với protected/system roles: mutation bị chặn theo policy hệ thống.

### Scope validation invariant

- `platform` assignment:
  - `tenant_id = null`
  - `workspace_id = null`
- `tenant` assignment:
  - `tenant_id != null`
  - `workspace_id = null`
- `workspace` assignment:
  - `tenant_id != null` (bắt buộc)
  - `workspace_id != null` (bắt buộc)

Reject ngay (`4xx`) nếu vi phạm invariant, đặc biệt trường hợp có `workspace_id` nhưng thiếu `tenant_id`.

---

## 4) Behavioral Semantics

### Generic mutation flow
1. Validate input và policy constraints.
2. Persist DB mutation trong transaction phù hợp.
3. Emit audit event mutation.
4. Invalidate cache liên quan.
5. Publish invalidation event cho replica khác.

### Failure handling
- Validate fail -> `4xx`.
- DB mutation fail -> rollback transaction, không invalidate.
- Publish invalidate fail -> mutation vẫn commit, ghi log/metric để sync loop tự-heal.

---

## 5) Security Rules

- Chỉ principal có permission admin RBAC mới được mutation.
- Response lỗi generic, không leak trạng thái nội bộ nhạy cảm.
- Ghi audit trail bắt buộc cho mọi mutation thành công/thất bại.

---

## 6) Observability

- `iam_rbac_mutation_total{operation,result}`
- `iam_rbac_mutation_duration_seconds{operation}`
- `iam_rbac_mutation_invalidation_total{operation,result}`

Logs tối thiểu:
- `request_id`, `actor_id`, `operation`, `target`, `result`, `reason`.

---

## 7) Acceptance Criteria

- [ ] Mọi mutation đi qua validate + transaction + audit.
- [ ] Cache invalidation chạy sau mutation commit.
- [ ] Mutation không fail-open về authz.
- [ ] Replica eventual consistency đạt qua invalidate + sync.
