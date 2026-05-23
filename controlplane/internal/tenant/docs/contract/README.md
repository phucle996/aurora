# Tenant Module Canonical Contract

## Contract Metadata
- Contract Scope: `controlplane/internal/tenant`
- Architecture Decision ID: `ARCH-TENANT-MODULE-V1`
- Requirement ID: `REQ-TENANT-001`
- Owner: Controlplane Backend Team
- Status: Draft v1
- Versioning Rule: Backward-compatible changes increment minor; breaking changes require architecture review and major bump.

## Purpose
This contract is the single source of truth for tenant module behavior and boundaries. Specs/plans/implementation must reference contract item IDs from this folder and only describe delta.

## Module Ownership Boundary
- Tenant module owns tenant lifecycle, domain mapping, membership, and tenant-scoped roles.
- IAM module owns global identity primitives (user credentials, token/session primitives).
- App module owns composition/wiring only.

## Canonical Contract Files
- `db-contract.md`
- `api-contract.md`
- `event-job-contract.md`
- `error-contract.md`
- `permission-contract.md`

## Change Policy
1. Any stable behavior change must update this contract set in the same PR.
2. No plan/spec may redefine SoT or ownership outside this contract.
3. Contract items must remain testable and observable.
