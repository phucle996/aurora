# Platform User Role Assign — God View

Workflow này thay platform global assignment của một target user bằng role được
actor quản lý. Assignment được compile thành deterministic five-part
permissions, rồi authorization state của target bị fenced sau durable commit.

## API scope and contract

| Part | Contract |
|---|---|
| Browser API | `POST /api/v1/iam/rbac/user-role` |
| Payload | `user_id` UUID target and `role_id` UUID |
| ACR | Verify personal session, rewrite `/api/v1/personal/iam/rbac/user-role`, set `x-original-path`, inject verified actor headers |
| Authorization | `Authorize("iam:role:assign", L1Registry, "2")` loads actor `user_role`; repository requires actor level stronger than both target current role and selected role |

This is platform owner administration, never a `/me` route and never tenant
membership assignment.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Envoy
    participant A as ACR
    participant CP as Controlplane
    B->>E: POST neutral assignment route plus JSON and Trinity cookies
    E->>A: ExtAuthz CheckRequest
    A->>A: Verify Trinity session and personal context
    A-->>E: Rewritten personal path plus trusted headers
    E->>CP: Forward POST
```

## Phase 2 — Controlplane compiles and persists one assignment

```mermaid
sequenceDiagram
    participant M as Authorize middleware
    participant H as RbacPlatformHandler
    participant S as RbacPlatformService
    participant Repo as RbacPlatformRepository
    participant DB as PostgreSQL

    M->>H: iam:role:assign level 2 accepted
    H->>H: Parse target and role UUIDs
    H->>S: AssignUserRole caller level
    S->>Repo: Assignment transaction
    Repo->>DB: Read target username selected role and catalog mappings
    Repo->>Repo: Compile username:nil-workspace:permission keys
    Repo->>DB: Recheck hierarchy then replace global user_role
    DB-->>Repo: Commit one compiled assignment
    Repo-->>S: Durable success
```

## Phase 3 — Target authorization invalidation

```mermaid
sequenceDiagram
    participant S as RbacPlatformService
    participant L1 as Local L1 cache
    participant AR as Auth-State Redis
    participant L2 as Shared Redis

    S->>L1: Delete user_role target entry
    S->>AR: Increment billing generation and delete alias snapshot atomically
    S->>L2: Publish authz.invalidate.billing after successful fence
```

Durable PostgreSQL assignment commits first. If Auth-State fence or Shared Redis
publish errors after commit, response is `500`; retry must not create a second
assignment and Auth-State generation is still the correctness boundary.

| Other result | Response |
|---|---|
| Unknown target user or role | `404` |
| Invalid UUID | `400` |
| Permission or hierarchy denial | `403` |

## Key contract

| Record / key | Rule |
|---|---|
| `user_role` with nil workspace UUID | One global platform assignment for target user |
| `RoleEntry.list_perm` | Deterministically encoded `username:workspace:module:object:behavior` keys |
| `authz:billing:{user_id}:generation` | Auth-State Redis stale-write fence, TTL one day |
| `authz.invalidate.billing` | Shared Redis best-effort L1 invalidation fanout |

