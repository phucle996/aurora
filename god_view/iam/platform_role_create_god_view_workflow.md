# Platform Role Create — God View

Workflow này tạo một platform role definition và permission mapping. Đây là
platform-owned mutation; valid workflow input cần verified personal context,
và direct internal path luôn bị ACR từ chối.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `POST /api/v1/iam/rbac/role` |
| Payload | `code`, `name`, `description`, `role_level`, `scope`, `permission_ids` UUID list |
| ACR | Verify personal session, rewrite `/api/v1/personal/iam/rbac/role`, set `x-original-path`, inject trusted identity and level |
| Authorization | `Authorize("iam:role:write", L1Registry, "2")`; handler rejects a requested stronger level and repository rechecks caller permission subset before commit |

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Envoy
    participant A as ACR
    participant CP as Controlplane

    B->>E: POST neutral role route plus JSON and Trinity cookies
    E->>A: ExtAuthz CheckRequest with bounded body
    A->>A: Verify session and choose personal branch
    A-->>E: Rewritten personal path plus trusted headers
    E->>CP: Forward POST
```

## Phase 2 — Controlplane authorizes and commits definition

```mermaid
sequenceDiagram
    participant M as Authorize middleware
    participant H as RbacPlatformHandler
    participant S as RbacPlatformService
    participant Repo as RbacPlatformRepository
    participant DB as PostgreSQL

    M->>M: Check iam:role:write and level 2
    M->>H: Authorized actor
    H->>H: Normalize code and parse permission UUIDs
    H->>S: CreateRole
    S->>S: Generate role UUID
    S->>Repo: Create role transaction
    Repo->>DB: Recheck caller permission subset and hierarchy
    DB-->>Repo: Insert platform role and mappings atomically
    Repo-->>H: Commit result
    H-->>M: 201 created
```

| Result | Response |
|---|---|
| Created | `201` |
| Invalid JSON/code/permission UUID | `400` |
| Unauthorized permission subset or hierarchy | `403` |
| Database error | `500`; no partial mapping |
