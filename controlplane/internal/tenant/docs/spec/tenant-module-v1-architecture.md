# Tenant Module V1 Architecture

## 1) Summary & Context
- Fact: Controlplane đang dùng modular structure với module độc lập như IAM.
- Fact: Tenant module đã có folder structure tại `internal/tenant`.
- Decision: Tenant được triển khai module riêng ngang cấp IAM, tích hợp qua contract sync interface.
- Architecture Decision ID: `ARCH-TENANT-MODULE-V1`.

## 2) Goals & Non-Goals
- Goals:
  - Tách tenant boundary độc lập và testable.
  - Hỗ trợ login `username@domain` để vào tenant context đúng.
  - Tenant role và membership là tenant-scoped SoT.
- Non-goals:
  - Không redesign toàn bộ IAM session model.
  - Không đưa billing/subscription vào phase v1.

## 3) System Boundary & Ownership
- App module: compose IAM + Tenant + Core.
- IAM module: identity primitives + credential verification.
- Tenant module: tenant lifecycle/domain/membership/tenant role.
- DB ownership: bảng tenant* chỉ do Tenant repository ghi.

## 4) Data Ownership / SoT
- `users` SoT: IAM.
- `tenants`, `tenant_domains`, `tenant_memberships`, `tenant_roles`, `tenant_membership_roles` SoT: Tenant.
- No cache as SoT.

## 5) Communication Workflow
- Sync path:
  - Auth request `username@domain` -> IAM parser -> Tenant `ResolveDomain` -> IAM verify credential -> Tenant `GetMembershipAndRoles`.
- Async path:
  - Tenant created/membership granted events qua outbox.

## 6) Security/Trust Boundary
- Generic auth failure cho mọi mismatch user/domain/membership.
- No secret/token logging.
- Cross-tenant role checks deny-by-default.

## 7) Consistency Model
- Strong consistency trong transaction create-tenant.
- Eventual consistency cho async event consumers via outbox.

## 8) Reliability/Failure Model
- Create tenant transaction rollback toàn bộ nếu seed role/link membership fail.
- Outbox retry + backoff + DLQ cho event publish failure.

## 9) Scalability/Performance
- Unique index trên `tenant_domains(domain)` và `(tenant_id,user_id)`.
- Resolve domain path phải O(logN) bằng index.

## 10) Observability/Operability
- Metrics:
  - `tenant_create_total`, `tenant_create_fail_total`
  - `tenant_domain_resolve_latency_ms`
  - `tenant_auth_context_mismatch_total`
- Audit events bắt buộc cho create tenant và role grant.

## 11) Alternatives & Tradeoffs
- Option A: Nhét tenant vào IAM (rejected): boundary lẫn, khó scale governance.
- Option B: Module riêng tenant (chosen): rõ ownership, dễ evolve.

## 12) Final Decision
- Chọn Option B: `internal/tenant` module riêng, contract-first integration với IAM.

## 13) Canonical Contract Layer
- Contract refs:
  - `controlplane/internal/tenant/docs/contract/README.md`
  - `controlplane/internal/tenant/docs/contract/db-contract.md`
  - `controlplane/internal/tenant/docs/contract/api-contract.md`
  - `controlplane/internal/tenant/docs/contract/event-job-contract.md`
  - `controlplane/internal/tenant/docs/contract/error-contract.md`
  - `controlplane/internal/tenant/docs/contract/permission-contract.md`

## Downstream Contract Bundle
- architecture decision id: `ARCH-TENANT-MODULE-V1`
- boundary ownership map: section 3
- source-of-truth map: section 4
- runtime map path: `controlplane/internal/tenant/docs/spec/tenant-module-v1-runtime-map.md`
- contract reference path: `controlplane/internal/tenant/docs/contract/README.md`
- risk/assumption register: `controlplane/internal/tenant/docs/spec/tenant-module-v1-risk-register.md`
