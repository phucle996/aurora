# Tenant Role Assignment Upgrade — God View

This explicit workflow moves existing memberships of one tenant role to its
current revision. It is separate from revision creation so tenant admins control
when authority changes.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `POST /api/v1/critical/iam/rbac/role/:role_id/assignments/upgrade`; empty body |
| Headers | Trinity cookies and session-proof headers |
| ACR | ExtAuthz verifies tenant session/proof, consumes nonce, removes browser context, rewrites to `/api/v1/tenant/critical/iam/rbac/role/:role_id/assignments/upgrade`, injects verified identity/tenant and proof marker |
| Authorization | `RequireSessionProof`, `Authorize("iam:role:assign", membership_role, "*")`; repository rechecks the same pinned `iam:role:assign` permission and hierarchy |

## Phase 1 — Client → Envoy → ACR

ACR returns local `401/403` on invalid proof/context. Only a verified empty POST
is forwarded to Controlplane.

## Phase 2 — Durable rollout

In one transaction the repository locks the actor membership/assignment and the
target role head with update-strength row locks. It resolves the current
immutable revision, then the mutation CTE rechecks actor permission, hierarchy,
revision ID and head version before updating only assignments whose pinned
revision version is old. A head changed while lock acquisition waited returns
`409`; retry resolves the new head. The response returns target version and the
number actually changed, and retries remain idempotent.

## Phase 3 — Immediate runtime observation

There is no Redis settlement phase. The tenant authority loader has zero TTL
and recompiles each assignment's pinned revision from PostgreSQL on every read,
so the next authorization request observes the committed rollout immediately.
The HTTP workflow succeeds when the PostgreSQL transaction commits and cannot
return a false `500` because a non-authoritative cache fanout was unavailable.
