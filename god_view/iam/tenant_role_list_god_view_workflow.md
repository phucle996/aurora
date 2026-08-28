# Tenant Role List — God View

Workflow này đọc role definition thuộc một tenant đã xác minh. Nó không đọc
platform role và không fallback sang personal khi membership tenant vắng mặt.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `GET /api/v1/iam/rbac/role` |
| Payload | Không có |
| ACR route action | Verify Trinity session với tenant concrete, rewrite `/api/v1/tenant/iam/rbac/role`, overwrite `:path`, set `x-original-path` |
| Trusted forwarded headers | `x-user-id`, `x-tenant-id`, `x-user-level`, `x-zone-id`, `x-client-device-id` after Envoy strips browser versions |
| Authorization | `middleware.Authorize("iam:role:read", L1Registry, "*")` invokes the zero-TTL durable membership loader; repository rechecks active tenant membership |

Direct browser calls to the internal tenant prefix are denied. This is not
`/me`: the actor may read tenant role definitions only through tenant authority.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Envoy
    participant A as ACR
    participant V as Vault
    participant AR as Auth-State Redis
    participant CP as Controlplane

    B->>E: GET neutral role list plus Trinity cookies
    E->>A: ExtAuthz CheckRequest
    A->>V: Verify JWT
    A->>AR: Verify runtime tenant session
    alt verified concrete tenant
        A-->>E: Rewritten tenant path plus trusted headers
        E->>CP: Forward internal GET
    else invalid or personal context
        A-->>E: 401 or 403 local response
    end
```

## Phase 2 — Controlplane authorizes and reads tenant definitions

```mermaid
sequenceDiagram
    participant R as Gin router
    participant M as Authorize middleware
    participant H as RbacTenantHandler
    participant S as RbacTenantService
    participant Repo as RbacTenantRepository
    participant DB as PostgreSQL

    R->>M: Required iam:role:read
    M->>M: Compile pinned revision from PostgreSQL
    M->>H: Authorized tenant request
    H->>S: ListTenantRoles
    S->>Repo: Read tenant-scoped roles
    Repo->>DB: Recheck active membership and tenant ownership
    DB-->>Repo: Current revisions, permission counts, total assignments and outdated assignments
    Repo-->>H: Tenant-only role list
    H-->>R: 200 roles JSON
```

| Result | Response |
|---|---|
| Authorized list | `200 {"roles":[...]}` |
| Missing permission, membership or tenant | `403` |
| Durable loader/repository failure | `500`; never platform fallback |

List chỉ project revision được `tenant_roles.current_version` chọn. Membership
không adopt head mới ở read path; `outdated_assignments_count` compares the
pinned revision's version with `current_version` để Console quyết định rollout tường minh.
