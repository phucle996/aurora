# Personal Render Context Read — God View

Workflow này trả personal composition root và capability presentation cho
platform-owned session. Đây không phải `/me`: browser gọi route neutral, ACR
chọn personal owner context rồi rewrite internal `/personal` route.

## API scope and contract

| Part | Contract |
|---|---|
| Browser request | `GET /api/v1/iam/context/read` |
| Input | Trinity cookies hoặc Bearer access token; browser không gửi trusted identity, role hay permission header |
| Payload | None |
| ACR output | `GET /api/v1/personal/iam/context/read`; overwrite `:path`, set `x-original-path`, inject `x-user-id`, `x-user-name`, `x-user-level`, `x-tenant-id=platform`, `x-zone-id`, `x-client-device-id` |
| Authorization | Personal `user_role` loads permission and required level, then repository rechecks active assignment |

Direct `/personal/**` browser paths are denied. A concrete tenant session is a
different tenant workflow, not a fallback result here.

The route intentionally does not add a second generic `middleware.Authorize`:
this workflow returns the caller's capability projection itself. The repository
still rechecks the active durable assignment and compiles its current role
mappings JIT; missing or stale authority fails closed.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Envoy
    participant A as ACR
    participant V as Vault
    participant AR as Auth-State Redis
    participant CP as Controlplane
    B->>E: GET neutral context route plus Trinity cookies
    E->>A: ExtAuthz CheckRequest
    A->>V: Verify JWT
    A->>AR: Verify access key secret and runtime session
    alt verified personal context
        A-->>E: Rewrite personal path plus trusted headers
        E->>CP: Forward internal GET
    else invalid session
        A-->>E: 401 local response
    end
```

## Phase 2 — Controlplane reads personal authority and compiles its projection JIT

```mermaid
sequenceDiagram
    participant R as Gin router
    participant H as PersonalRenderContextHandler
    participant S as PersonalRenderContextService
    participant Repo as PersonalRbacRepository
    participant DB as PostgreSQL
    R->>H: Internal personal context route
    H->>S: GetPersonalRenderContext verified user
    S->>Repo: Load personal authority
    Repo->>DB: Read active user_role plus current role mappings
    Repo->>Repo: Compile deterministic five-part permission entries
    Repo-->>S: Personal permissions
    S->>S: Sort capabilities and navigation
    S-->>H: Personal context
    H-->>R: 200 personal JSON
```

| Result | Response |
|---|---|
| Success | `200 {"kind":"personal","navigation":[...],"capabilities":{...}}` |
| Missing/stale assignment | `403`; never tenant result |
| Cache/PostgreSQL failure | `503`; never empty-context success |

## Key contract

| Key / record | Purpose |
|---|---|
| L1 `user_role:{user_id}` | Sensitive in-process JIT-compiled personal `RoleEntry` |
| PostgreSQL `user_role` + role mappings | Durable personal authorization SoT |
