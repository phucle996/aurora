# Tenant Workspace Delete — God View

This workflow removes one Tenant workspace only when the Tenant retains at
least one workspace. It is a direct hierarchy deletion, not resource teardown.

## API and authority contract

| Item | Contract |
|---|---|
| Browser API | `DELETE /api/v1/hierarchy/workspaces` |
| Internal target | `DELETE /api/v1/tenant/hierarchy/workspaces` |
| Authority | tenant `hierarchy:workspace:delete` at level `*` |
| Durable action | scoped hard delete from `hierarchy.tenant_workspaces` |
| Guard | serialize full Tenant workspace scope and deny deletion of final workspace |

The target is exclusively the verified active `x-workspace-id` injected by ACR
from the current workspace context. The browser sends no workspace identifier,
so it cannot authorize one workspace and delete another.

## Key contracts

| Record | Rule |
|---|---|
| `tenant_workspaces` | `tenant_id` must equal verified current Tenant |
| lock set | all workspaces for that Tenant are locked before counting/deleting |
| membership RoleEntry | route authorization source for delete capability |

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Central Envoy
    participant A as ACR
    participant SR as Session Redis
    participant CP as Controlplane

    B->>E: DELETE neutral workspace path with empty body
    E->>A: CheckRequest original path headers and empty body
    A->>A: CORS and pre-auth rate limit
    A->>SR: Verify Trinity session access-secret and revocation
    A->>A: Post-auth limit CSRF Zone and Tenant resolution
    alt concrete Tenant verified
        A->>A: Remove client context and rewrite Tenant path
        A-->>E: allow verified headers
        E->>CP: DELETE internal Tenant workspace path
    else denied or personal context
        A-->>E: local denial or personal branch selection
    end
```

ACR receives the exact method/path/headers in CheckRequest. It reads Redis
session state and must remove browser proof/context headers before it overwrites
identity/Tenant/Zone/device/original-path and active workspace headers from
verified context. It injects `x-session-proof-verified: false`, sets public
`x-original-path`, and replaces `:path` with the Tenant target. It does not
forward browser context or make a Controlplane request on CORS, rate, session,
CSRF, or context failure.

## Phase 2 — Controlplane scoped delete

```mermaid
sequenceDiagram
    participant M as Authorize middleware
    participant H as WorkspaceTenantHandler
    participant S as TenantWorkspaceService
    participant P as TenantWorkspaceRepository
    participant PG as PostgreSQL

    M->>H: Require tenant workspace delete
    H->>H: Read verified active workspace and tenant context
    H->>S: Delete command
    S->>P: Delete scoped workspace
    P->>PG: Lock every workspace in tenant scope
    P->>PG: Count then delete only if count exceeds one
    PG-->>H: deleted or guarded outcome
    H-->>M: 200 or mapped failure
```

Missing active workspace returns `403`; absent workspace returns `404`;
final-workspace guard returns `409`; database
failure returns `500`. The present repository has no resource-teardown check:
it enforces only the final-workspace invariant. Any future workload teardown
must be a separate durable workflow before changing this deletion contract.

## Gateway workspace-context boundary

The former path/header mismatch has been removed. `middleware.Authorize` and
the handler receive the same active workspace context. ACR removes every
browser `x-workspace-id` header, then injects only the value read from
`workspace_id` cookie. A missing active workspace fails closed before deletion;
the Console clears it after success and enters explicit workspace-selection
onboarding.

## Phase 3 — Console continuation onboarding

```mermaid
sequenceDiagram
    participant UI as Workspace screen
    participant WC as Workspace context
    participant ON as Continue onboarding

    UI->>WC: Remove deleted workspace from catalog
    WC->>WC: Clear workspace_id cookie and active selection
    UI->>ON: Navigate to tenant workspaces continue
    ON->>ON: Render remaining catalog without auto selection
    ON->>WC: Select user chosen workspace
    WC-->>UI: Persist new active workspace then continue
```

This client-local phase runs only after the PostgreSQL delete succeeded. It is
not a replacement for durable settlement and cannot alter the deletion result.
