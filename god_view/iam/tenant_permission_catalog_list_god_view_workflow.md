# Tenant Permission Catalog List — God View

Workflow này trả permission catalog cho tenant actor cấu hình tenant-owned role.
Tenant `membership_role` là authority source duy nhất; `user_role` không fallback
vào workflow này.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `GET /api/v1/iam/rbac/permissions` |
| Payload | None |
| ACR | With verified concrete tenant session, rewrite to `/api/v1/tenant/iam/rbac/permissions`, overwrite `:path`, set `x-original-path`, inject trusted identity and tenant headers |
| Authorization | `Authorize("iam:permissions:read", L1Registry, "*")` loads `membership_role`; repository rechecks durable tenant caller guard |

Direct tenant internal paths are denied. A personal session belongs to a separate
personal catalog workflow, never fallback authority.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Envoy
    participant A as ACR
    participant V as Vault
    participant AR as Auth-State Redis
    participant CP as Controlplane
    B->>E: GET neutral permissions route plus Trinity cookies
    E->>A: ExtAuthz CheckRequest
    A->>V: Verify JWT
    A->>AR: Verify concrete tenant runtime session
    A-->>E: Rewrite tenant route plus trusted headers
    E->>CP: Forward internal GET
```

## Phase 2 — Controlplane authorizes tenant actor and reads catalog

```mermaid
sequenceDiagram
    participant M as Authorize middleware
    participant H as RbacPlatformHandler
    participant S as RbacPlatformService
    participant Repo as RbacPlatformRepository
    participant DB as PostgreSQL
    M->>M: Load membership_role and check iam:permissions:read
    M->>H: Authorized tenant request
    H->>S: ListPermissions verified caller
    S->>Repo: Read tenant-visible catalog
    Repo->>DB: Query permission rows and tenant caller guard
    DB-->>Repo: Canonical permission catalog
    Repo-->>H: Tenant-visible permissions
    H-->>M: 200 permissions JSON
```

| Result | Response |
|---|---|
| Authorized | `200 {"permissions":[...]}` |
| Missing tenant membership permission | `403` |
| Dependency failure | `500` |

