# Platform Role Update — God View

Workflow này thay name, description và permission set của một platform role,
đồng bộ role metadata trên affected `user_role`, rồi invalidate authorization
state sau durable commit. Permission projection được compile JIT ở lần load kế tiếp.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `PUT /api/v1/iam/rbac/role/:role_id` |
| Payload | `name`, `description`, non-empty `permission_ids` UUID list |
| ACR | Verify personal session, rewrite `/api/v1/personal/iam/rbac/role/:role_id`, set `x-original-path`, inject verified actor headers |
| Authorization | `Authorize("iam:role:write", L1Registry, "2")`; repository locks target role, verifies weaker hierarchy and actor permission subset |

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Envoy
    participant A as ACR
    participant CP as Controlplane

    B->>E: PUT neutral role path plus JSON and Trinity cookies
    E->>A: ExtAuthz CheckRequest
    A->>A: Verify personal Trinity session
    A-->>E: Rewritten personal route plus trusted headers
    E->>CP: Forward PUT
```

## Phase 2 — Controlplane commits role and assignment metadata

```mermaid
sequenceDiagram
    participant M as Authorize middleware
    participant H as RbacPlatformHandler
    participant S as RbacPlatformService
    participant Repo as RbacPlatformRepository
    participant DB as PostgreSQL

    M->>H: iam:role:write level 2 accepted
    H->>H: Parse role and permission UUIDs
    H->>S: UpdateRole
    S->>Repo: Repeatable-read transaction
    Repo->>DB: Lock target and validate hierarchy subset and catalog IDs
    Repo->>DB: Update role version and mappings
    Repo->>DB: Sync role name and version on every affected user_role
    DB-->>Repo: Commit and affected user IDs
    Repo-->>S: Durable success
```

## Phase 3 — Authorization invalidation after commit

```mermaid
sequenceDiagram
    participant S as RbacPlatformService
    participant L1 as Local L1 cache
    participant AR as Auth-State Redis
    participant L2 as Shared Redis

    loop each affected user
        S->>L1: Delete user_role entry
        S->>AR: Increment billing generation and delete snapshot atomically
        S->>L2: Publish authz.invalidate.billing only after fence succeeds
    end
```

If any post-commit invalidation fails, the handler returns `500`; the durable
role update remains committed. Auth-State generation is the correctness fence;
Shared Redis fanout is only best-effort replica L1 invalidation.

| Other result | Response |
|---|---|
| Invalid ID/body or invalid permission selection | `400` |
| Role absent | `404` |
| Permission subset or hierarchy denied | `403` |
