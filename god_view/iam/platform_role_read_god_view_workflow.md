# Platform Role Detail Read — God View

Workflow này đọc một platform role và catalog permissions đã gắn với role đó.
Actor chỉ được đọc role có authority thấp hơn mình theo numeric hierarchy.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `GET /api/v1/iam/rbac/role/:role_id` |
| Path input | `role_id` UUID |
| ACR | Verify personal session then rewrite to `/api/v1/personal/iam/rbac/role/:role_id`, set `x-original-path`, overwrite trusted headers |
| Authorization | `Authorize("iam:role:read", L1Registry, "2")` over `user_role`; repository requires `target.role_level > caller_level` |

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Envoy
    participant A as ACR
    participant CP as Controlplane

    B->>E: GET neutral role detail plus Trinity cookies
    E->>A: ExtAuthz CheckRequest
    A->>A: Verify Trinity session and personal context
    alt verified
        A-->>E: Internal personal path plus trusted headers
        E->>CP: Forward GET
    else rejected
        A-->>E: 401 or 403
    end
```

## Phase 2 — Controlplane reads the hierarchy-scoped role

```mermaid
sequenceDiagram
    participant M as Authorize middleware
    participant H as RbacPlatformHandler
    participant S as RbacPlatformService
    participant Repo as RbacPlatformRepository
    participant DB as PostgreSQL

    M->>M: Check iam:role:read and level 2
    M->>H: Verified caller context
    H->>H: Parse role UUID
    H->>S: GetRoleDetails caller level
    S->>Repo: Read role and linked permissions
    Repo->>DB: Query role join mappings and enforce hierarchy
    DB-->>Repo: Definition and three-part permissions
    Repo-->>H: Role detail
    H-->>M: 200 role JSON
```

| Result | Response |
|---|---|
| Found and visible | `200 {"role":{...,"permissions":[...]}}` |
| Invalid UUID | `400` |
| Absent role | `404` |
| Equal or stronger target role | `403` |
| Store failure | `500` |

