# Platform Role List — God View

Đây là workflow đọc các platform role mà platform actor được phép quản lý. Nó
không đọc tenant role và không có fallback từ tenant sang personal.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `GET /api/v1/iam/rbac/role` |
| Payload | None |
| ACR | With verified personal context, rewrite to `/api/v1/personal/iam/rbac/role`, overwrite `:path`, set `x-original-path`, inject verified identity/context headers |
| Authorization | `Authorize("iam:role:read", L1Registry, "2")` loads `user_role`; role list is restricted to `role_level > caller_level` |

Direct `/personal/**` browser access is denied. This is a platform-owned route,
not `/me`.

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
    A->>AR: Verify access key secret and runtime session
    alt verified personal context
        A-->>E: Rewritten personal path plus trusted headers
        E->>CP: Forward internal GET
    else invalid session
        A-->>E: 401 local response
    end
```

## Phase 2 — Controlplane authorizes and lists visible roles

```mermaid
sequenceDiagram
    participant R as Gin router
    participant M as Authorize middleware
    participant H as RbacPlatformHandler
    participant S as RbacPlatformService
    participant Repo as RbacPlatformRepository
    participant DB as PostgreSQL

    R->>M: Require iam:role:read level 2
    M->>M: Load L1 user_role by verified user
    M->>H: Authorized actor and level
    H->>S: ListPlatformRoles caller level
    S->>Repo: Query visible roles
    Repo->>DB: Read roles where target level is weaker
    DB-->>Repo: Roles with assignment and permission counts
    Repo-->>H: Visible platform roles
    H-->>R: 200 roles JSON
```

| Result | Response |
|---|---|
| Authorized | `200 {"roles":[...]}` |
| Missing role grant or level | `403` |
| L1/repository failure | `500`; no tenant fallback |
