# Personal Permission Catalog List — God View

Workflow này trả permission catalog cho platform actor đang cấu hình personal
platform role. Đây là personal owner flow, không phải tenant branch hay `/me`.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `GET /api/v1/iam/rbac/permissions` |
| Payload | None |
| ACR | With verified personal session, rewrite to `/api/v1/personal/iam/rbac/permissions`, overwrite `:path`, set `x-original-path`, inject trusted identity/context headers |
| Authorization | `Authorize("iam:permissions:read", L1Registry, "2")` loads `user_role`; repository rechecks durable caller guard |

Direct personal internal paths are denied. A concrete tenant session belongs to
a different tenant catalog workflow, never fallback authority.

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
    A->>AR: Verify personal runtime session
    A-->>E: Rewrite personal route plus trusted headers
    E->>CP: Forward internal GET
```

## Phase 2 — Controlplane authorizes personal actor and reads catalog

```mermaid
sequenceDiagram
    participant M as Authorize middleware
    participant H as RbacPlatformHandler
    participant S as RbacPlatformService
    participant Repo as RbacPlatformRepository
    participant DB as PostgreSQL
    M->>M: Load user_role and check iam:permissions:read level 2
    M->>H: Authorized personal request
    H->>S: ListPermissions verified caller
    S->>Repo: Read platform-visible catalog
    Repo->>DB: Query permission rows and caller guard
    DB-->>Repo: Canonical permission catalog
    Repo-->>H: Personal-visible permissions
    H-->>M: 200 permissions JSON
```

| Result | Response |
|---|---|
| Authorized | `200 {"permissions":[...]}` |
| Missing personal permission/level | `403` |
| Dependency failure | `500` |

