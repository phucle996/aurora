# Platform Role Delete — God View

Workflow này xóa một platform role definition only when caller hierarchy allows
it and the role has no active assignment that would make deletion unsafe.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `DELETE /api/v1/iam/rbac/role/:role_id` |
| Payload | None |
| ACR | Verify personal session, rewrite `/api/v1/personal/iam/rbac/role/:role_id`, set `x-original-path`, inject trusted headers |
| Authorization | `Authorize("iam:role:delete", L1Registry, "2")`; repository rechecks target hierarchy and assignment precondition |

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Envoy
    participant A as ACR
    participant CP as Controlplane
    B->>E: DELETE neutral role path plus Trinity cookies
    E->>A: ExtAuthz CheckRequest
    A->>A: Verify session and personal context
    A-->>E: Rewritten personal path plus trusted headers
    E->>CP: Forward DELETE
```

## Phase 2 — Controlplane authorizes and removes the role

```mermaid
sequenceDiagram
    participant M as Authorize middleware
    participant H as RbacPlatformHandler
    participant S as RbacPlatformService
    participant Repo as RbacPlatformRepository
    participant DB as PostgreSQL

    M->>H: iam:role:delete level 2 accepted
    H->>H: Parse role UUID
    H->>S: DeleteRolePlatform caller level
    S->>Repo: Delete transaction
    Repo->>DB: Recheck target level and active assignments
    alt allowed
        DB-->>Repo: Delete role and mappings
        Repo-->>H: Success
        H-->>M: 200
    else rejected
        Repo-->>H: Not found or hierarchy precondition
    end
```

| Result | Response |
|---|---|
| Deleted | `200` |
| Invalid UUID | `400` |
| Role absent | `404` |
| Stronger/equal role or role still assigned | `403` |
| Store failure | `500` |

