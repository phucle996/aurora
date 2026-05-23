# IAM RBAC Migration - Full Specification

## 1) Purpose + Scope

### Purpose
Định nghĩa behavior và contract migration cho RBAC mới theo mô hình multi-tenant với hidden authority level, trong đó authority level thuộc về role (không thuộc về user).

### In-scope
- DB contract đích cho RBAC:
  - `users`, `roles`, `permissions`, `role_permissions`, `user_role_assignments`, `audit_logs`.
- Quy tắc migrate dữ liệu từ schema IAM hiện tại sang schema RBAC mới.
- Quy tắc compatibility trong giai đoạn chuyển đổi.
- Failure semantics và acceptance criteria cho rollout migration.

### Out-of-scope
- Chi tiết patch code handler/service/repo cụ thể.
- UI behavior chi tiết.
- Quyết định release schedule theo môi trường.

---

## 2) Terminology / Actors

- **Actor**: user thực hiện action.
- **Target**: user/tài nguyên bị tác động.
- **Tenant context**: phạm vi tổ chức mà actor đang thao tác.
- **Permission**: mô tả action được phép làm.
- **Authority level**: mô tả cấp tác động actor lên target (số nhỏ hơn = quyền cao hơn).
- **Effective level**: `MIN(authority_level)` của actor trong tenant context.
- **Migration operator**: hệ thống/engine chạy migration.

---

## 3) API/DB Contract (Migration Target)

## 3.1 Core rule
- `role_level` (authority hierarchy) MUST nằm trên `roles`.
- `users` MUST NOT là source-of-truth cho authority hierarchy.
- `users.user_level` MUST bị loại bỏ khỏi schema đích.

## 3.2 Target schema constraints

### `users`
- Chỉ giữ identity/account status.
- Không có `users.user_level` trong schema đích.
- Authorization không được đọc level từ `users` ở bất kỳ path nào.

### `roles`
- MUST có:
  - `scope` (`global|tenant`)
  - `role_level` (integer, >=0)
  - `is_system`
  - `is_protected`
  - `is_assignable`
  - `owner_tenant_id` nullable (null cho system role, set cho tenant custom role)

### `permissions`
- MUST có `code` unique và `scope` (`global|tenant`).

### `role_permissions`
- PK `(role_id, permission_id)`.

### `user_role_assignments`
- MUST có:
  - `user_id`, `role_id`
  - `scope` (`global|tenant`)
  - `tenant_id` nullable theo rule scope
  - `assigned_by`, `created_at`
- MUST enforce:
  - `scope=global => tenant_id IS NULL`
  - `scope=tenant => tenant_id IS NOT NULL`

### `audit_logs`
- Ghi được actor, target, tenant context, action, metadata và request context.

---

## 4) Flow Behavior (Migration Behavior)

## 4.1 Migration strategy
Migration MUST theo thứ tự hành vi an toàn:

1. **Expand schema**  
   - Thêm bảng/cột/constraint mới ở trạng thái không phá read/write cũ.
2. **Backfill data**  
   - Sinh `authority_level` cho roles mặc định và map dữ liệu assignments hiện có.
3. **Switch authz source-of-truth**  
   - Quyết định authz IAM dựa trên role authority + permission.
4. **Enforce constraints**  
   - Bật cứng các check/unique/rules scope sau khi backfill sạch.
5. **Cleanup legacy**  
   - Legacy path (`users.user_level` authz) ngừng dùng; cột có thể giữ tạm cho compatibility/reporting rồi xóa ở migration sau.

## 4.2 Authority evaluation behavior
- Mọi user-critical action MUST dùng:
  1) tenant scope/context check (tenant ownership sẽ do module Tenant quản lý ở phase sau)  
  2) permission check  
  3) actor-target authority check (`actor_role_level < target_role_level`)

---

## 5) Data & Boundary Rules

- Source-of-truth authz mới:
  - tenant context validity: do module Tenant quản lý (ngoài scope migration IAM hiện tại)
  - permission: `role_permissions` + `permissions`
  - authority: `roles.role_level` qua assignments
- `tenant_id` chỉ là context filter, không thay permission/authority check.
- Migrate MUST không tạo duplicate assignment active cùng `(user_id, role_id, tenant_id/scope)`.
- Với tenant custom roles, `owner_tenant_id` MUST trỏ đúng tenant sở hữu role.

---

## 6) Security Rules

- Migration MUST fail-closed: nếu backfill `roles.role_level` không hoàn tất thì không được bật enforcement path mới.
- Không được để bất kỳ action path nào bỏ qua authority check sau switch.
- Không log raw secrets/tokens trong migration logs.
- Lỗi migration trả generic cho public surface; chi tiết chỉ ở operator logs nội bộ.

---

## 7) Failure Semantics

- **Expand step fail**: rollback transaction của migration step đó; giữ schema cũ hoạt động.
- **Backfill fail**: không được chuyển source-of-truth authz; migration trạng thái “partial-expanded”.
- **Constraint-enforce fail**: giữ mode compatibility, log records gây vi phạm để xử lý dữ liệu.
- **Cleanup fail**: không ảnh hưởng authz correctness nếu switch đã hoàn tất; xử lý ở step riêng.

Retry/backoff:
- Migration engine SHOULD retry step idempotent.
- Step không idempotent MUST có guard chống chạy lặp gây trùng dữ liệu.

---

## 8) Non-functional Baseline

- Migration phải hỗ trợ chạy nhiều lần an toàn (idempotent where possible).
- Lock time trên bảng nóng SHOULD được tối thiểu hóa (ưu tiên add nullable + backfill batch + enforce sau).
- Không làm gián đoạn login/authz path quá ngưỡng downtime cho phép của hệ thống.

---

## 9) Acceptance Criteria

- [ ] `role_level` được định nghĩa và sử dụng từ `roles`.
- [ ] `users.user_level` bị loại bỏ khỏi schema đích.
- [ ] Có đủ permission + authority checks cho user-critical actions trong scope IAM.
- [ ] Scope constraints của assignments được enforce đúng (`global|tenant`).
- [ ] Dữ liệu role/assignment hiện có được backfill không mất quyền hợp lệ.
- [ ] Audit ghi nhận được các action nhạy cảm theo tenant context.
- [ ] Migration có thể rollback an toàn ở từng bước khi fail.
