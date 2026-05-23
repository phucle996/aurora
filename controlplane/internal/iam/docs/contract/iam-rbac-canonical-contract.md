# IAM RBAC Canonical Contract

## 1) Scope & Ownership

### Scope
Canonical contract layer cho IAM RBAC + admin-guarded RBAC mutation surfaces.

### Owners
- Primary owner: IAM module maintainers
- Review owner: security/reviewer role

### Versioning Rule
- Contract file là source-of-truth cho stable/public behavior.
- Bất kỳ thay đổi public behavior MUST update file này trong cùng PR.

### Change Policy
Cần update contract khi đổi bất kỳ phần nào:
- DB schema/invariants cho RBAC entities.
- API request/response/status/header/cookie semantics.
- Event/job payload/ordering/dedupe semantics.
- Public error taxonomy hoặc mapping semantics.
- Permission matrix / critical guard requirements.

### Drift Control
Spec/plan MUST reference contract items theo id (`new/changed/deprecated`) thay vì copy lại full contract.

---

## 2) Database Contract

### Contract Item: DB-RBAC-001
- **Owner**: IAM Repository layer
- **Rules**: RBAC canonical entities là `roles`, `permissions`, `role_permissions`, `subject_roles` (hoặc equivalent names từ migrations/models).
- **Invariants**:
  - Role identity unique + stable.
  - Permission identity theo `<domain>.<resource>.<action>` và unique.
  - Role-permission relation unique per pair.
  - Subject-role relation unique per pair.
- **Failure Semantics**:
  - Duplicate identity/relation -> conflict-style behavior.
  - Missing identity/relation -> not-found style behavior.
- **Verification Evidence**:
  - `internal/iam/migrations/000002_iam_tables.up.sql`
  - `internal/iam/migrations/000003_iam_indexes.up.sql`

### Contract Item: DB-RBAC-002
- **Owner**: IAM Repository + RBAC cache sync service
- **Rules**: RBAC mutation write DB first, rồi phát invalidation/sync signal.
- **Invariants**:
  - DB là source-of-truth.
  - Cache không bao giờ là canonical writer.
- **Failure Semantics**:
  - Invalidation channel failure không được fail-open authz; runtime có thể degrade sang DB read path.
- **Verification Evidence**:
  - `internal/iam/service/rbac_service.go`
  - `internal/iam/cache/rbac_cache_bus.go`

### Contract Item: DB-ADMIN-DEVICE-001
- **Owner**: IAM admin repository/service
- **Rules**: Admin device tracking table (`000007_iam_admin_devices.up.sql`) lưu stable device identity/public key material cho critical-signature verification.
- **Invariants**:
  - Device public key retrieval theo device identity là deterministic.
- **Failure Semantics**:
  - Missing device cho critical action -> deny.
- **Verification Evidence**:
  - `internal/iam/repository/admin_api_key_repo.go`
  - `internal/http/middleware/admin_critical_signature.go`

---

## 3) API Contract

### Contract Item: API-RBAC-READ-001
- **Owner**: IAM handler/service
- **Rules**: `GET /admin/rbac/roles` trả role listing cho authenticated admin session.
- **Invariants**:
  - Bắt buộc qua admin API auth middleware.
- **Failure Semantics**:
  - Unauthorized khi admin auth invalid.
- **Verification Evidence**:
  - `internal/iam/route.go`
  - `internal/iam/transport/http/handler/rbac_handler.go`

### Contract Item: API-RBAC-MUTATE-001
- **Owner**: IAM handler/service
- **Rules**:
  - `POST /admin/rbac/roles`
  - `PUT /admin/rbac/roles/:id`
  - `DELETE /admin/rbac/roles/:id`
  mutate RBAC role resources under admin-auth guard.
- **Invariants**:
  - Mutation endpoints yêu cầu authenticated admin context.
  - Permission checks vẫn deny-by-default ở authorization layer.
- **Failure Semantics**:
  - Validation error -> bad request.
  - Auth failure -> unauthorized.
  - Conflict/not-found mapped từ service errors.
- **Verification Evidence**:
  - `internal/iam/route.go`

### Contract Item: API-ADMIN-CRITICAL-001
- **Owner**: IAM admin auth handler + middleware chain
- **Rules**:
  - `POST /admin/auth/refresh` và `POST /admin/auth/rotate-key` là critical-path guarded bằng CIDR + admin auth + signature guard (và step-up cho rotate-key).
- **Invariants**:
  - Critical signature headers canonical:
    - `X-Admin-Signature`
    - `X-Admin-Timestamp`
    - `X-Admin-Nonce`
  - Step-up headers canonical:
    - `X-Admin-StepUp-Method`
    - `X-Admin-StepUp-Code`
- **Failure Semantics**:
  - Missing/invalid critical headers -> unauthorized.
  - Nonce replay -> unauthorized.
- **Verification Evidence**:
  - `pkg/constant/http_header.go`
  - `internal/http/middleware/admin_critical_signature.go`
  - `internal/http/middleware/admin_critical_stepup_2fa.go`

---

## 4) Event / Job Contract

### Contract Item: EVT-RBAC-INVALIDATE-001
- **Owner**: RBAC service + cache bus
- **Rules**: Mọi RBAC mutation MUST emit invalidation/sync signal cho cache coherence across replicas.
- **Invariants**:
  - Mutation không invalidation là contract violation.
  - Replica eventual converge qua bus + periodic sync.
- **Failure Semantics**:
  - Bus transient failure -> degrade mode + rely on periodic sync/self-heal.
- **Verification Evidence**:
  - `internal/iam/cache/rbac_cache_bus.go`
  - `internal/iam/service/rbac_cache_sync.go`

### Contract Item: EVT-RBAC-SYNC-001
- **Owner**: RBAC sync scheduler/service
- **Rules**: Replica định kỳ reconcile RBAC cache state bằng version/epoch checkpoint semantics.
- **Invariants**:
  - Sync process idempotent.
- **Failure Semantics**:
  - Sync error increments observability counters và retry ở cycle kế.
- **Verification Evidence**:
  - `internal/iam/docs/spec/iam-rbac-cache-sync-v1-spec.md`
  - `internal/iam/service/rbac_cache_sync.go`

---

## 5) Error Contract

### Contract Item: ERR-RBAC-001
- **Owner**: IAM service + transport mapping
- **Rules**: Client-facing RBAC errors generic, non-leaky; internal reasons chỉ ở log/observability.
- **Invariants**:
  - Không lộ secret/token/internal SQL detail ra client.
- **Failure Semantics**:
  - authz deny -> unauthorized/forbidden theo endpoint policy.
  - eval/system error -> internal error path.
- **Verification Evidence**:
  - `internal/iam/errorx`
  - handler mapping

### Contract Item: ERR-CRITICAL-ACTION-001
- **Owner**: HTTP middleware layer
- **Rules**: Critical middleware (signature/step-up) fail-closed.
- **Invariants**:
  - Missing dependency hoặc invalid verification state không được fail-open.
- **Failure Semantics**:
  - dependency unavailable -> service unavailable.
  - invalid credential/signature/code -> unauthorized.
- **Verification Evidence**:
  - `internal/http/middleware/admin_critical_signature.go`
  - `internal/http/middleware/admin_critical_stepup_2fa.go`

### Contract Item: ERR-LOG-REDACTION-001
- **Owner**: logging/handler policy
- **Rules**: Log có reason code + correlation id nhưng MUST redact secrets/tokens/device_secret.
- **Invariants**:
  - Không log raw credential material.
- **Failure Semantics**:
  - Redaction failure được coi là security defect.
- **Verification Evidence**:
  - logger usage trong IAM handlers + review checklist.

---

## 6) Permission Contract

### Contract Item: PERM-NAMING-001
- **Owner**: IAM RBAC domain owner
- **Rules**: Permission naming format MUST là `<domain>.<resource>.<action>`.
- **Invariants**:
  - Names stable + semantically meaningful.
- **Failure Semantics**:
  - Unknown permission ở authorize path => deny-by-default.
- **Verification Evidence**:
  - `internal/iam/docs/idea/iam-rbac-full-idea.md`

### Contract Item: PERM-EVAL-001
- **Owner**: IAM authorization service
- **Rules**: Authorization permission-first và deny-by-default.
- **Invariants**:
  - Cấm hardcode role string trong handler.
  - Allow chỉ khi subject có role chứa permission required và context constraints pass.
- **Failure Semantics**:
  - No permission match -> deny.
  - Evaluation dependency error -> error (fail-closed).
- **Verification Evidence**:
  - `internal/iam/service/rbac_service.go`
  - `internal/iam/docs/spec/iam-rbac-v1-spec.md`

### Contract Item: PERM-CRITICAL-GUARD-001
- **Owner**: IAM security policy
- **Rules**: RBAC allow là điều kiện cần, chưa đủ cho critical actions.
- **Invariants**:
  - Critical actions cần thêm runtime guard theo policy (CIDR/device-signature/step-up).
- **Failure Semantics**:
  - Thiếu guard bổ sung -> deny.
- **Verification Evidence**:
  - `internal/iam/route.go`
