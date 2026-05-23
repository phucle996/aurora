# Tenant Module V1 Detailed Implementation Plan

## Plan ID
- `PLAN-TENANT-V1-001`

## Source of Truth
- Spec: `controlplane/internal/tenant/docs/spec/tenant-module-v1-spec.md`
- Runtime map: `controlplane/internal/tenant/docs/spec/tenant-module-v1-runtime-map.md`
- Architecture decision: `controlplane/internal/tenant/docs/spec/tenant-module-v1-architecture.md`
- Story acceptance: `controlplane/internal/tenant/docs/plan/tenant-module-v1-story-acceptance.md`
- Contract root: `controlplane/internal/tenant/docs/contract/README.md`

---

## 1) Scope Lock

### Must change
- Tenant DB schema + indexes + constraints theo `DB-*` contract.
- Tenant domain/repo/service contracts.
- Tenant repository SQL implementation.
- Tenant service transactional bootstrap + domain resolver + membership role reads.
- Tenant transport handlers + route wiring.
- IAM integration point cho login `username@domain` theo runtime map.
- Error mapping theo stable envelope/code contract.

### Must not change
- Không cho IAM truy cập trực tiếp tenant tables (chỉ qua tenant contract/service).
- Không đưa SQL ra khỏi repository layer.
- Không mở scope sang billing/IdP federation.
- Không đổi public behavior ngoài spec v1.

### Acceptance gates (must pass)
- AC-001..AC-005 mapped to code-path có evidence test.
- Cross-tenant deny-by-default enforced.
- Create tenant bootstrap transaction rollback toàn bộ nếu seed/link fail.
- Login mismatch (domain/user/membership) trả generic auth failure.

---

## 2) Workstreams & File-Level Changes

## 2.1 Migration Workstream

### THÊM/SỬA
- `controlplane/internal/tenant/migrations/*`

### Required schema surfaces
- `tenants`
- `tenant_domains` (global unique domain)
- `tenant_memberships` (tenant_id + user_id scoped)
- `tenant_roles`
- `tenant_membership_roles`
- Outbox table (nếu chưa có ở tenant boundary)

### Constraints/index requirements
- `UNIQUE tenant_domains(domain)`
- `UNIQUE tenant_memberships(tenant_id, user_id)` (active scope theo contract)
- FK integrity cho membership/role mapping
- Query indexes cho:
  - domain resolve,
  - membership lookup,
  - role lookup by membership.

---

## 2.2 Domain Contract Workstream

### THÊM/SỬA
- `controlplane/internal/tenant/domain/entity/*`
- `controlplane/internal/tenant/domain/repo/*`
- `controlplane/internal/tenant/domain/service/*`

### Domain contracts
- Tenant lifecycle entity + state transitions (`active -> suspended -> deleted`)
- Membership lifecycle entity (`invited -> active -> revoked`)
- Repo interfaces:
  - `CreateTenantTx(...)`
  - `ResolveTenantByDomain(...)`
  - `GetMembershipAndRoles(...)`
  - `SeedDefaultTenantRoles(...)`
  - `BindCreatorOwnerMembership(...)`
- Service interfaces:
  - `CreateTenant(...)`
  - `ResolveLoginContextByDomain(...)`
  - `AuthorizeTenantScope(...)`

---

## 2.3 Repository Workstream

### THÊM/SỬA
- `controlplane/internal/tenant/repository/*`

### Rules
- SQL chỉ ở repo.
- Mọi write multi-step phải đi transaction ở repo/service boundary đã chốt.
- Deterministic error mapping cho duplicate domain/duplicate membership.

### Pre-change function table
| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `CreateTenantTx` | THÊM | Chưa có bootstrap tx contract hoàn chỉnh | Insert tenant + domain + seed role + bind owner trong 1 tx | AC-004, AC-005 |
| `ResolveTenantByDomain` | THÊM/SỬA | Resolve chưa canonical | Resolve bằng unique domain index | AC-001 |
| `GetMembershipAndRoles` | THÊM/SỬA | Tenant role read path rời rạc | Trả membership + effective roles scoped tenant | AC-001, AC-002 |

---

## 2.4 Service Workstream

### THÊM/SỬA
- `controlplane/internal/tenant/service/*`

### Business behaviors
- Create tenant:
  - Validate request
  - Open tx
  - Insert tenant+domain
  - Seed default roles (idempotent)
  - Bind creator owner membership
  - Commit
  - Publish outbox `tenant.created.v1`
- Login resolve:
  - Parse `username@domain`
  - Resolve tenant by domain
  - Verify credential (IAM)
  - Read membership+roles in tenant
  - Return tenant auth context

### Error semantics
- Generic auth failure cho mismatch domain/user/membership.
- Conflict deterministic cho duplicate domain.
- Dependency/internal map chuẩn envelope.

---

## 2.5 Transport Workstream

### THÊM/SỬA
- `controlplane/internal/tenant/transport/http/handler/*`
- `controlplane/internal/tenant/route.go` (hoặc file route tương ứng)
- `controlplane/internal/tenant/module.go` (wiring)

### Endpoint scope (from API contract)
- Tenant create
- Tenant membership management
- Tenant role assignment/revoke (nếu có trong spec v1)

### Middleware/order
- Auth -> tenant scope guard -> handler.
- Deny-by-default cross-tenant mutation.

---

## 2.6 IAM Integration Workstream

### THÊM/SỬA
- `controlplane/internal/iam/service/auth_service.go` (hoặc parser integration point hiện hữu)
- Tenant contract adapter layer giữa IAM và Tenant module

### Contract
- IAM không đọc tenant tables trực tiếp.
- IAM gọi tenant resolver + membership role API nội bộ.

---

## 2.7 Testing Workstream (handoff to workflow-tester for full matrix)

### Required test layers
- Repo integration tests:
  - domain unique conflict
  - tx rollback on seed/link failure
  - membership scope correctness
- Service unit tests:
  - login resolve success/mismatch
  - bootstrap idempotency
  - generic auth failure invariants
- Handler transport tests:
  - status/error envelope mapping
  - permission boundary responses
- Workflow tests:
  - end-to-end create tenant bootstrap
  - `username@domain` login context
  - cross-tenant deny scenario.

---

## 3) Milestones

- **M1**: migration + repository foundation complete.
- **M2**: service transaction bootstrap + tenant domain resolve complete.
- **M3**: handler/route + IAM integration complete.
- **M4**: contract/doc sync + test evidence ready.

---

## 4) Risks & Mitigations

- Risk: cross-tenant leak via missing scope filter.
  - Mitigation: mandatory tenant_id filter in repo methods + transport guard tests.
- Risk: partial bootstrap create tenant.
  - Mitigation: single tx + rollback on any seed/link failure.
- Risk: auth enumeration by detailed mismatch errors.
  - Mitigation: generic auth failure envelope for all mismatch cases.
- Risk: duplicate provisioning via retries.
  - Mitigation: idempotency key + deterministic unique constraints.

---

## 5) Exit Criteria

- Contract items (`DB-*`, `API-*`, `ERR-*`, `PERM-*`) có evidence mapping.
- No boundary violation: IAM không truy cập trực tiếp tenant DB tables.
- AC-001..AC-005 có test evidence pass.
- Runtime flow khớp `tenant-module-v1-runtime-map.md`.
