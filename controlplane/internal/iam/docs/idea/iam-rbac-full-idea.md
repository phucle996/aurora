# IAM RBAC (Multi-Tenant + Hidden Authority Level) - Full End-to-End Idea

## 1) Bài toán cần giải quyết

RBAC hiện tại trong IAM đã có nền role/permission/assignment nhưng chưa đủ chặt cho mô hình multi-tenant và các user-critical actions.  
Vấn đề lớn nhất là hệ thống mới trả lời tốt câu hỏi **“actor làm được action này không?”** nhưng chưa trả lời đầy đủ câu hỏi **“actor được làm action này lên target user nào?”**.

Hệ quả là dễ xuất hiện privilege escalation:
- Admin tenant có thể chạm vào owner tenant.
- User có permission quản trị thành viên nhưng tác động được lên user cấp cao hơn.
- Custom role có thể bị cấu hình vượt quyền actor tạo role.

Mục tiêu của RBAC là đưa authorization về mô hình 3 lớp rõ ràng:
- **Where**: actor có thuộc tenant context hợp lệ không (tenant membership do Tenant module quản lý).
- **What**: actor có permission để làm action không.
- **Who**: actor có authority đủ cao để tác động target không.

---

## 2) Định hướng kiến trúc RBAC

RBAC theo nguyên tắc:
- `User` chỉ là identity/account, không gắn level trực tiếp.
- `Role` là nơi chứa quyền (permissions) và cấp quyền ẩn (`role_level`).
- `Assignment` gán role cho user theo scope (`global` hoặc `tenant`).
- `Tenant membership` xác định user có thuộc tenant hay không (nguồn sự thật nằm ở Tenant module).
- `Permission` mô tả action cụ thể.
- `Role level` kiểm soát quan hệ actor-target trong critical actions.

Câu chốt của model:

> Permission defines what an actor can do.  
> Role level defines who the actor can do it to.  
> Tenant membership defines where the actor can act.

---

## 3) Mục tiêu sản phẩm

RBAC cần đạt:
- Chuẩn multi-tenant: cùng một user có thể có vai trò khác nhau theo tenant.
- Chống leo thang quyền ở user-critical actions.
- Cho phép tenant custom role linh hoạt nhưng không phá security boundary.
- Hỗ trợ audit đầy đủ cho mọi hành động nhạy cảm.
- Giữ được khả năng mở rộng cho workspace/project scope về sau.

---

## 4) Thực thể lõi (End-to-End Conceptual Model)

Minimum domain entities:
- `users`
- `tenants`
- `roles`
- `permissions`
- `role_permissions`
- `user_role_assignments`
- `audit_logs`

Optional:
- `role_assignment_history`
- `tenant_invitations`
- `user_sessions`
- `mfa_factors`

---

## 5) Quy tắc scope

### 5.1 Role scope
- `global`: role dùng cho platform-level operations.
- `tenant`: role dùng trong tenant context.

### 5.2 Permission scope
- `global.*`: tác vụ platform.
- `tenant.*`: tác vụ trong tenant.

### 5.3 Assignment scope
- `global` assignment: không gắn tenant_id.
- `tenant` assignment: bắt buộc tenant_id.

---

## 6) Hidden Role Level (cơ chế bảo vệ ẩn)

`role_level` là thuộc tính nội bộ của role, không để tenant admin tùy ý set trực tiếp trong UI.

Quy ước:
- Số càng nhỏ => quyền càng cao.

Ví dụ baseline:
- `0`: Root system
- `1`: System admin
- `2`: Support/operator
- `3`: Tenant owner
- `4`: Tenant admin
- `5+`: Tenant custom/business roles
- `6`: Member
- `7`: Viewer

`effective_level` của user trong tenant là `MIN(role_level)` qua tất cả role assignments active trong tenant đó.

---

## 7) Luật authorization cho user-critical actions

Với action có target user (suspend, remove, reset MFA, revoke session, assign role...), phải qua đủ 4 check:

1. Actor có tenant context hợp lệ (membership active do Tenant module xác nhận).
2. Target thuộc tenant context hợp lệ (do Tenant module xác nhận).
3. Actor có permission cần thiết.
4. `actor_level < target_level`.

Nếu vi phạm 1 trong 4 điều kiện => từ chối.

---

## 8) Luật assign role

Khi actor gán role cho target trong tenant:
- Actor phải có `tenant.role.assign`.
- `actor_level < target_level`.
- `actor_level < new_role.role_level`.
- `new_role.scope = tenant`.
- `new_role.is_assignable = true`.
- `new_role` thuộc system tenant role hoặc custom role của chính tenant hiện tại.

---

## 9) Luật tạo custom role

Khi tenant admin tạo custom role:
- Input từ UI chỉ gồm: `name`, `permissions`.
- Actor không được nhập `role_level`.
- Backend tự tính level an toàn, ví dụ:
  - `role_level = max(actor_level + 1, 5)`.
- Actor không được gán permission mà bản thân không có.
- Không cho tạo role ngang hoặc cao hơn actor.

---

## 10) Tenant context là cổng vào tenant

Mọi tenant-scoped request nên bị chặn sớm nếu:
- actor không có tenant membership active trong tenant context (check bởi Tenant module).

`tenant_id` trong request/query chỉ là context filter, không thay thế authorization check.

---

## 11) Audit-first cho hành động nhạy cảm

Các action nhạy cảm bắt buộc audit:
- member invite/remove/suspend/reset_mfa/revoke_session
- role create/update/delete/assign/revoke
- owner transfer
- global user/tenant suspend

Audit cần lưu:
- actor
- target
- tenant context
- action
- old/new data (khi phù hợp)
- request metadata (ip, user agent, request_id)

---

## 12) Guardrails an toàn bắt buộc

- Không được remove/downgrade owner cuối cùng của tenant.
- Không cho actor tác động user cùng cấp hoặc cấp cao hơn.
- Không cho custom role vượt quyền actor tạo role.
- Không để permission check thay thế authority check.
- Không để authority check thay thế membership check.

---

## 13) Tương thích với codebase hiện tại

RBAC mới sẽ thay thế dần mô hình cũ theo hướng:
- Giảm phụ thuộc vào `users.user_level` cho authorization.
- Dồn quyền thực thi vào `role + permission + assignment + role_level` và tenant context check từ Tenant module.
- Ưu tiên migration additive trước (thêm cột/bảng/constraint), tránh phá runtime đột ngột.

---

## 14) Non-goals của idea này

- Chưa chốt chi tiết migration SQL cuối cùng.
- Chưa chốt endpoint contract cụ thể cho từng API.
- Chưa chốt policy workspace/project sâu hơn tenant.

Các phần này sẽ đi ở `spec` và `plan`.

---

## 15) Kết luận

RBAC mới cần được xem là security model chứ không chỉ là bảng quyền.

Mô hình đúng là:
- Identity tách khỏi authority.
- Permission tách khỏi actor-target hierarchy.
- Tenant membership là boundary bắt buộc (nằm ở Tenant module).

Với hướng này, hệ thống vừa đủ linh hoạt cho custom role, vừa giữ được safety boundary cho các user-critical actions trong môi trường multi-tenant.
