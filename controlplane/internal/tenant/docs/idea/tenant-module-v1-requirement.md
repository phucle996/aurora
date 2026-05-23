# Tenant Module V1 Requirement

## Requirement ID
- `REQ-TENANT-001`

## Problem Context
Hệ thống hiện có IAM global identity nhưng chưa có boundary tenant độc lập và tenant-scoped auth context qua domain.

## Stakeholders
- Platform Owner
- Tenant Admin
- Tenant Member
- Security/Compliance
- Backend Engineering

## In-Scope
- Tenant module riêng ngang IAM.
- Migrations cho tenant, domain, membership, tenant role binding.
- Login `username@domain`.
- Auto seed default tenant roles + creator membership link.

## Out-of-Scope
- Billing
- Domain verification full lifecycle (phase sau)
- SSO federation

## Functional Requirements
- FR-001: Tạo tenant với primary domain.
- FR-002: Domain resolve ra tenant duy nhất.
- FR-003: User login bằng `username@domain` vào đúng tenant context.
- FR-004: Tenant role/membership scoped theo tenant.
- FR-005: Create tenant phải tự seed default roles và gán creator owner.

## Non-Functional Requirements
- Security: generic auth failure, no enumeration.
- Reliability: create tenant transactional rollback full.
- Performance: domain resolve < 50ms p95 tại scale mục tiêu.
- Audit: ghi nhận create tenant, grant/revoke membership roles.

## Dependencies
- IAM user identity & credential verification.
- Existing auth/session pipeline.

## Open Questions
- Bộ default role cuối cùng có cần `viewer` không?
- Login không domain có policy fallback nào?
- Domain alias strategy phase v1 hay v2?
