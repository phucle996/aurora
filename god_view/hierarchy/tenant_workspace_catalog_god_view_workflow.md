# Tenant Workspace Catalog — God View

This is the small, Zone-filtered workspace selector read used while composing
Tenant context. It intentionally returns only `id`, `code`, and `name`.

## API and authority contract

| Item | Contract |
|---|---|
| Browser API | `GET /api/v1/hierarchy/workspaces/catalog` |
| Internal target | `GET /api/v1/tenant/hierarchy/workspaces/catalog` |
| Scope | verified Tenant, verified selected Zone, verified current user |
| Authority source | service loads current membership RoleEntry and filters `workspace:read` grants |
| Durable read | `hierarchy.tenant_workspaces` |

This route is a bootstrap exception: router has no `Authorize` middleware, but
the service still loads the actor's membership-role projection and applies its
workspace allowlist before the repository query.

## Key contracts

| Record / key | Rule |
|---|---|
| verified `x-tenant-id` and `x-zone-id` | only ACR chooses Tenant and Zone scope |
| `membership_role:{user_id}:{tenant_id}` | zero-TTL loader compiles the pinned immutable revision or fails closed |
| `tenant_workspaces` | query filters both tenant and Zone, then wildcard/UUID allowlist |

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Central Envoy
    participant A as ACR
    participant SR as Session Redis
    participant CP as Controlplane

    B->>E: GET neutral workspace catalog
    E->>A: CheckRequest method path authority headers
    A->>A: CORS then IP device pre-auth rate limit
    A->>SR: Verify Trinity session and revocation state
    A->>A: Post-auth limit CSRF and verified Zone Tenant resolution
    A->>A: Remove browser context and select Tenant target
    A-->>E: allow verified context and rewritten path
    E->>CP: GET /api/v1/tenant/hierarchy/workspaces/catalog
```

ACR's CheckRequest has no payload. It reads SessionManager Redis plus
rebuildable Zone state, overwrites client identity/Tenant/Zone/device/original
path headers, injects verified `x-user-id`, `x-user-name`, `x-user-level`,
`x-tenant-id`, `x-zone-id`, `x-client-device-id`, and
`x-session-proof-verified: false`, sets `x-original-path`, and replaces `:path`.
CORS/rate/session/CSRF/context denial is local and has no upstream forward.

## Phase 2 — Controlplane catalog projection

```mermaid
sequenceDiagram
    participant H as WorkspaceTenantHandler
    participant S as TenantWorkspaceService
    participant C as Durable membership role loader
    participant P as TenantWorkspaceRepository
    participant PG as PostgreSQL

    H->>S: Catalog tenant Zone actor command
    S->>C: Load compiled membership role
    C-->>S: wildcard or allowed workspace IDs
    S->>P: Catalog query
    P->>PG: Filter tenant_id zone_id and allowlist
    PG-->>H: id code name rows
    H-->>H: 200 minimal catalog
```

The missing route middleware is deliberate only for the selector bootstrap
cycle; it does not authorize unfiltered access. If the membership-role loader
cannot read or compile, the implementation returns `500` today rather than
leaking rows. A later hardening change may normalize that fail-closed result to
`403`, but must not broaden the query.
