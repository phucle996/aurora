# Tenant Role Revision Create — God View

Updating a tenant role creates an immutable next revision. Existing membership
and invitation grants are not rewritten by this workflow.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `PUT /api/v1/critical/iam/rbac/role/:role_id` |
| Payload | `expected_version`, `name`, optional `description`, `role_level` 4–99, 1–256 `permission_ids` |
| Headers | Trinity cookies plus session-proof challenge ID, timestamp and signature |
| ACR | ExtAuthz verifies session/proof, consumes nonce, strips client context, rewrites exact method/path to `/api/v1/tenant/critical/iam/rbac/role/:role_id`, injects verified tenant/identity and `x-session-proof-verified:true` |
| Authorization | `RequireSessionProof`, `Authorize("iam:role:write", membership_role, "*")`, then repository rechecks actor permission and both old/new hierarchy levels |

## Phase 1 — Client → Envoy → ACR

Invalid session/proof/context gets a local `401/403`; there is no upstream
mutation. A verified request is forwarded with the exact JSON body unchanged.

## Phase 2 — Controlplane commits immutable revision

One CTE locks the role head, validates all permission IDs, requires
`head.current_version=expected_version`, inserts revision `expected+1` and its
permission mappings, then advances `current_version` in the same commit. The
reserved `tenant_root` cannot be revised through this API. A stale writer gets
`409`; invalid selection `400`; missing/invisible role `404/403`; success `201`.

## Security and failure invariants

- Memberships continue authorizing from their pinned `tenant_role_revision_id`;
  runtime reads compile that immutable mapping and never adopt the new head.
- Existing invitations remain pinned but their join/preview current-version
  fence makes them unusable after the head advances.
- Any insertion/head-update failure rolls back the whole revision.
