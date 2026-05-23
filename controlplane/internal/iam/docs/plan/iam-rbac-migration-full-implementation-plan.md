# IAM RBAC Migration - Full Implementation Plan

## 1) Mục tiêu triển khai

Triển khai migration IAM RBAC theo mô hình mới với source-of-truth authority nằm ở `roles.role_level`, loại bỏ phụ thuộc `users.user_level` trong logic phân quyền.  
Done definition: IAM có schema/contract RBAC mới hoạt động độc lập cho permission + role-level authorization, đồng thời giữ tương thích an toàn trong quá trình chuyển đổi dữ liệu.  
**Lưu ý phạm vi quan trọng**: plan này **chưa triển khai Tenant module** và **không tạo ownership của tenant membership** trong IAM.  
Không nằm trong scope: xây mới tenant membership service/module, UI tenant governance, hoặc policy cross-module ngoài IAM.

---

## 2) Current state vs target state

### Current state
- IAM migration hiện tại còn dư âm `users.user_level`.
- RBAC có role/permission/assignment nhưng chưa chuẩn hóa `role_level` làm authority hierarchy.
- Tenant context checks hiện chưa được tách rõ dependency ownership với Tenant module.
- Bảng assignment hiện tại là `user_roles`, chưa đồng bộ terminology mới `user_role_assignments`.

### Target state
- IAM RBAC dùng `roles.role_level` làm authority hierarchy duy nhất trong IAM scope.
- `users.user_level` không còn là source-of-truth cho IAM authorization path.
- Bảng assignment được chuẩn hóa tên thành `user_role_assignments`.
- IAM chỉ xử lý permission + role-level checks; tenant membership/context validation là dependency từ Tenant module (chưa làm trong plan này).

---

## 3) Implementation changes (grouped by subsystem)

### A) Migration

**Files**
- `SỬA` `controlplane/internal/iam/migrations/000002_iam_tables.up.sql`
- `SỬA` `controlplane/internal/iam/migrations/000003_iam_indexes.up.sql`
- `SỬA` `controlplane/internal/iam/migrations/000002_iam_tables.down.sql`
- `SỬA` `controlplane/internal/iam/migrations/000003_iam_indexes.down.sql`
- `THÊM` migration delta mới (nếu cần để tránh sửa migration đã chạy ở môi trường shared)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| RBAC tables contract | SỬA | Roles chưa chuẩn `role_level`, users còn `user_level` path | Chuẩn hóa cột `roles.role_level` + flags role governance | Đưa authority về đúng role |
| RBAC indexes/constraints | SỬA | Scope uniqueness/check chưa đầy đủ theo contract mới | Thêm/điều chỉnh unique/check/index cho assignments + role code scoping | Giảm drift dữ liệu, chặn assignment lỗi |
| Assignment table rename | SỬA | Bảng `user_roles` | Đổi tên thành `user_role_assignments` + cập nhật references | Đồng bộ contract và terminology |
| Legacy compatibility | THÊM | Legacy `user_level` có thể còn được đọc gián tiếp | Introduce migration-safe compatibility gate trước khi cleanup | Giảm rủi ro break runtime |

#### Migration SQL scope bắt buộc (rõ thay đổi migration)

1) **Rename bảng assignment**
- `ALTER TABLE user_roles RENAME TO user_role_assignments;`

2) **Rename index liên quan bảng assignment**
- `user_roles_user_id_idx` -> `user_role_assignments_user_id_idx`
- `user_roles_role_id_idx` -> `user_role_assignments_role_id_idx`
- `user_roles_scope_type_idx` -> `user_role_assignments_scope_type_idx`
- `user_roles_tenant_workspace_idx` -> `user_role_assignments_tenant_workspace_idx`
- `user_roles_platform_scope_uidx` -> `user_role_assignments_platform_scope_uidx`
- `user_roles_tenant_scope_uidx` -> `user_role_assignments_tenant_scope_uidx`
- `user_roles_workspace_scope_uidx` -> `user_role_assignments_workspace_scope_uidx`

3) **Chuẩn hóa role level**
- Đảm bảo cột authority hierarchy là `roles.role_level` (không dùng `authority_level`).

4) **Loại bỏ user level khỏi schema đích**
- Stop-read trước, rồi drop cột:
- `ALTER TABLE users DROP COLUMN user_level;`

5) **Boundary Tenant module**
- Migration IAM này không tạo `tenant_memberships` và không sở hữu tenant membership data.

### B) Domain Entity / Model

**Files**
- `SỬA` `controlplane/internal/iam/domain/entity/rbac.go`
- `SỬA` `controlplane/internal/iam/model/rbac.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Role entity/model fields | SỬA | Thiếu `role_level` làm nguồn authority chính | Thêm `role_level`, `is_assignable`, `is_protected`, owner tenant metadata nếu nằm trong IAM RBAC scope | Entity/model parity với migration mới |
| Assignment shape | SỬA | Scope fields chưa phản ánh full rule mới | Chuẩn hóa scope + tenant context fields trong IAM assignment | Tránh mismatch repo mapping |

### C) Repository

**Files**
- `SỬA` `controlplane/internal/iam/repository/rbac_repo.go`
- `SỬA` `controlplane/internal/iam/domain/repo/rbac_repo.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Role CRUD mapping | SỬA | Chưa đọc/ghi đầy đủ role-level fields | Persist/load `role_level` + governance flags | Service có dữ liệu authority chuẩn |
| Assignment queries | SỬA | Query effective authority chưa chuẩn role-level flow | Query `MIN(role_level)` theo tenant scope trong IAM context | Bảo đảm decision logic nhất quán |
| Permission checks | SỬA | Check chỉ dừng ở permission existence | Kết hợp query hỗ trợ role-level comparison cho user-critical action | Khóa escalation path trong IAM |
| Assignment table mapping | SỬA | Query/map vào `user_roles` | Query/map vào `user_role_assignments` | Đồng bộ với migration rename |

### D) Service

**Files**
- `SỬA` `controlplane/internal/iam/service/rbac_service.go`
- `SỬA` `controlplane/internal/iam/domain/service/rbac_service.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| User-critical authorization | SỬA | Dựa nhiều vào permission, authority path chưa rõ | Áp dụng role-level comparison rõ ràng trong IAM scope | Chặn hành động lên equal/higher role level |
| Role assignment guard | SỬA | Guard assign role chưa chặt theo role_level | Enforce `actor_level < target_level` và `< new_role_level` | Ngăn assign vượt quyền |
| Custom role creation guard | SỬA | Chưa khóa chặt quyền grant | Không cho grant permission vượt quyền actor trong IAM scope | Giữ boundary role governance |

### E) Handler / Transport

**Files**
- `SỬA` `controlplane/internal/iam/transport/http/handler/rbac_handler.go`
- `SỬA` `controlplane/internal/iam/route.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| RBAC mutation responses | SỬA | Error mapping chưa phản ánh đầy đủ authority-denied semantics | Map chuẩn unauthorized/forbidden theo error contract hiện tại | Client nhận semantics nhất quán |
| Route security | SỬA | Middleware chain hiện hữu nhưng chưa nêu dependency tenant-context rõ | Ghi rõ dependency external tenant-context ở transport contract (không implement tenant module trong plan này) | Tránh hiểu nhầm scope triển khai |

### F) Cache / Sync

**Files**
- `SỬA` `controlplane/internal/iam/cache/rbac_sync_store.go`
- `SỬA` `controlplane/internal/iam/service/rbac_cache_sync.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Cache payload schema | SỬA | Payload cache có thể thiếu `role_level`/flags mới | Bổ sung fields phục vụ read-path authorization mới | Giảm stale authorization decisions |
| Cache invalidation semantics | SỬA | Invalidation chưa cover full authority-related changes | Invalidate khi role_level / role-permission / assignment thay đổi | Đảm bảo cache parity |

### G) Docs

**Files**
- `SỬA` `controlplane/internal/iam/docs/idea/iam-rbac-full-idea.md`
- `SỬA` `controlplane/internal/iam/docs/spec/iam-rbac-migration-full-spec.md`
- `THÊM` `controlplane/internal/iam/docs/plan/iam-rbac-migration-full-implementation-plan.md`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Terminology sync | SỬA | Lẫn `authority_level` và `role_level` wording | Chuẩn hóa `role_level` toàn bộ IAM docs | Tránh implement sai contract |
| Scope declaration | SỬA | Dễ hiểu nhầm IAM sẽ làm tenant module | Ghi rõ tenant module chưa triển khai trong plan này | Khóa scope, tránh scope creep |

### H) Tests

**Files**
- `SỬA` `controlplane/internal/iam/test/repo_test/*` (RBAC-related)
- `SỬA` `controlplane/internal/iam/test/svc_test/*` (RBAC-related)
- `SỬA` `controlplane/internal/iam/test/transport_test/*` (RBAC-related)
- `THÊM`/`SỬA` migration tests cho rename `user_roles` -> `user_role_assignments`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Repo tests | SỬA | Chưa assert đủ role-level mapping/constraints | Verify schema mapping + query effective role level | Chặn regression data layer |
| Service tests | SỬA | Chưa đủ case escalation guards | Verify actor-target/new-role comparisons | Khóa rule bảo mật chính |
| Transport tests | SỬA | Chưa đủ authority-denied path | Verify status semantics theo contract | API behavior ổn định |
| Migration tests | THÊM | Chưa lock behavior rename bảng/index | Verify migrate up/down với table/index names mới | Tránh drift giữa môi trường |

---

## 4) Contract changes

### Endpoint / API
- Không thêm endpoint công khai mới bắt buộc trong phase migration này.
- Có thể thay đổi behavior validation/error semantics của RBAC mutation theo rule role-level mới (vẫn theo envelope hiện hành).

### DTO / Entity / Repo interface
- Thêm/bổ sung trường `role_level` và role governance flags ở entity/model/repo contract.
- Chuẩn hóa assignment scope fields theo migration contract.

### Error mapping
- Các case authority violation map thành lỗi business chuyên biệt để handler trả về status phù hợp theo contract hiện có.

### Cross-module boundary
- Tenant membership/context validation là dependency từ Tenant module và **không được implement trong plan IAM này**.

---

## 5) Test plan + acceptance

### Test plan
- **Happy path**
  - Tạo/sửa role và assignment hợp lệ với `role_level` contract.
  - Permission check + role-level check pass cho action hợp lệ.
- **Error path**
  - Actor cố tác động target equal/higher level -> reject đúng semantics.
  - Actor cố gán role equal/higher level -> reject.
  - Actor cố grant permission ngoài quyền actor -> reject.
- **Edge path**
  - Multi-role assignment cùng tenant -> effective level lấy `MIN(role_level)`.
  - Cache sync sau mutation role_level/permission/assignment không stale.

### Acceptance checklist
- [ ] `roles.role_level` là authority source-of-truth trong IAM.
- [ ] Không còn path authz dựa vào `users.user_level`.
- [ ] RBAC mutation guard đúng theo role-level rules.
- [ ] Test repo/service/transport RBAC liên quan pass.
- [ ] Docs IAM ghi rõ tenant module chưa nằm trong scope triển khai này.

---

## 6) Rollout & operations

- **Enable path**
  1) Apply migration expand/additive.
  2) Backfill role-level + assignment parity.
  3) Switch service logic sang role-level source-of-truth.
  4) Enforce constraints và theo dõi logs/metrics.
- **Disable/fallback path**
  - Rollback theo migration down/feature-flag behavior path (nếu có) ở mức service.
  - Giữ fail-closed cho action nhạy cảm khi parity chưa đạt.
- **Required config**
  - Không thêm config mới bắt buộc, trừ khi chọn feature flag rollout.
- **Monitoring**
  - Tỷ lệ authority-denied tăng bất thường sau switch.
  - Lỗi migration/backfill.
  - Cache sync divergence liên quan role-level data.

---

## 7) Risk & mitigation

1. **Risk**: Drift dữ liệu role cũ không map sạch sang role-level mới.  
   **Mitigation**: backfill script có report anomalies + chặn enforce nếu còn anomaly.
2. **Risk**: Vẫn còn code path đọc `users.user_level`.  
   **Mitigation**: grep gate trong CI + test fail nếu path legacy còn được gọi.
3. **Risk**: Scope creep sang tenant membership/module.  
   **Mitigation**: hard scope statement trong docs + PR checklist “no Tenant module changes”.
4. **Risk**: Cache stale gây quyết định authz sai tạm thời.  
   **Mitigation**: invalidate rõ theo mutation events + sync tests.
5. **Risk**: Thay đổi guard làm tăng lỗi forbidden ở production.  
   **Mitigation**: staged rollout + observability dashboard cho authority-denied trends.
