# Billing Plan List — God View (Master SoT)

This operator read lists plans for the caller's verified concrete Zone. It is
not a wallet owner workflow and client `zone_id` may only repeat the session Zone.

## Phase 1 — Client → Envoy → ACR

| Part | Contract |
|---|---|
| Method/path | `GET /api/v1/billing/plans?cursor&limit&service_type&status&zone_id` |
| Headers used | Cost Billing Alias cookies and `Origin`; Cloud authority is denied for this operator route |
| Payload | query only; no client identity or permission header |
| ACR output | verifies session/alias, removes raw proof/workspace headers and overwrites client identity headers with `x-user-id`, `x-zone-id`, `x-tenant-id`; keeps public path; no proof |
| Failure | invalid identity `401`; direct owner prefix rejected; no Cost call |


## Phase 2 — Cost API read

`ContextInjector` parses only ACR headers. `Authorize(billing:plan:read)` uses
the generation-fenced IAM Billing projection; `PlanHandler.ListPlans` validates
cursor/filter and rejects query Zone mismatch before `PlanService.ListPlans` and
repository read. PostgreSQL is the plan authority; no mutation/outbox occurs.

```mermaid
sequenceDiagram
    participant API as Gin
    participant M as Identity and Authorize
    participant H as PlanHandler
    participant S as PlanService
    participant DB as Billing PostgreSQL
    API->>M: Parse trusted identity and resolve exact permission
    M->>H: authorized request
    H->>H: Validate cursor, filters and bound Zone
    H->>S: ListPlans
    S->>DB: Cursor page scoped to x-zone-id
    DB-->>S: plans and next cursor
    S-->>H: result
    H-->>API: 200 JSON page
```

## Complete edge and authorization execution

### Client input, CheckRequest and exact ACR forward

This operator endpoint is available only on Cost Console authority; Cloud
authority may call neutral owner-wallet routes but is denied on this path. The
browser sends no body and no identity/permission input.

| Request part | ACR use | Accepted value / rule |
|---|---|---|
| Method/path | route gate | exact `GET /api/v1/billing/plans`; query is preserved to Cost API |
| `Origin` | CORS gate | must match configured allowed origin when present |
| `X-Forwarded-For`, `client_device_id` cookie | rate limiter | pre-auth IP/device bucket before alias verification |
| Billing alias ID/secret cookies | `verify_billing_alias` | alias secret hash and referenced IAM source session/proof key must match |
| Query `cursor`, `limit`, `service_type`, `status`, `zone_id` | handler only | ACR does not trust `zone_id`; handler compares it to verified alias Zone |
| `x-user-*`, `x-tenant-id`, `x-zone-id`, permission/proof/workspace headers | rejected as authority | ACR removes/overwrites them; client never chooses actor/Zone/tenant |

ACR first performs CORS and pre-auth rate limiting, then validates Billing Alias
and source IAM session. It applies post-auth rate limiting to alias user ID;
GET bypasses CSRF mutation checks. Billing Zone/tenant are bound in the alias,
so ACR does not resolve zone/tenant from browser cookies. It does not rewrite
this operator path and forwards exactly the original method, public path/query
and empty body after removing client proof/workspace headers. It overwrites:

```text
x-user-id, x-user-name, x-zone-id, x-tenant-id
x-session-proof-verified: false
```

It does not forward role, level, permission, alias secret, source access key or
proof key. `x-original-path` is absent because no rewrite occurred.

```mermaid
sequenceDiagram
    participant UI as Cost Console
    participant E as Envoy
    participant X as ACR ExtAuthzService
    participant CG as CorsAndAuthorityGate
    participant RL as ACR RateLimiter
    participant AV as BillingAliasVerifier
    participant SM as SessionManager
    participant AR as Auth-State Redis
    participant HB as TrustedHeaderBuilder
    participant API as Cost API

    UI->>E: GET plans query and alias cookies
    E->>X: CheckRequest authority headers query
    X->>CG: Validate CORS and Cost authority
    X->>RL: Pre-auth IP device bucket
    X->>AV: Verify alias ID secret and source session
    AV->>SM: Load alias then referenced IAM session
    SM->>AR: GET alias then IAM source session
    X->>RL: Post-auth alias user bucket
    X->>HB: Remove raw proof/workspace and overwrite alias headers
    HB-->>E: CheckResponse keeps public path
    E->>API: GET same public path and query
```

### Cost component chain and REST output

`ContextInjector` parses ACR UUID headers. `Authorize` calls
`AuthorizationResolver.Resolve(user, critical=false)`: L1 hit is valid only
for five seconds; miss reads the three generation-fenced Auth-State Redis keys.
If L2 misses/stales, one Cost pod wins a two-second lock, subscribes to
`iam.authorization.billing.reply.{request_uuid}` before publishing the fixed
32-byte request to `iam.authorization.billing.get`, waits at most 900ms for IAM,
and Lua-writes projection only if generation did not change. Waiters jitter and
retry L2. Every result must contain exact `billing:plan:read`.

After authorization `PlanHandler.ListPlans` bounds limit, decodes the opaque
cursor, validates service type/status, obtains verified Zone from context and
rejects mismatched query Zone. `PlanService.ListPlans` then calls repository;
PostgreSQL returns one cursor page and next cursor. No business Redis key,
outbox, wallet or ledger changes.

| Result | Response headers | Response payload | Durable effect |
|---|---|---|---|
| `200` | Cost API JSON envelope | plan items and optional next cursor | none |
| `400` | JSON | malformed cursor/filter/Zone UUID | none |
| `401` | ACR JSON/denial | no plan data | none |
| `403` | ACR authority/CORS or Cost permission/Zone mismatch | no plan data | none |
| `429` | ACR limiter denial | no plan data | none |
| `503` | Cost JSON | IAM resolver or dependency unavailable | none |

```mermaid
sequenceDiagram
    participant G as Gin ContextInjector
    participant M as Authorize middleware
    participant L1 as Cost L1
    participant L2 as Auth-State Redis
    participant SR as Shared Redis
    participant IAM as IAM Billing authorization responder
    participant H as PlanHandler
    participant S as PlanService
    participant Repo as PlanRepository
    participant DB as Billing PostgreSQL

    G->>M: trusted alias identity context
    M->>L1: Read user permission cache
    alt L1 miss and L2 stale
        M->>L2: MGET data generation data_generation
        M->>SR: Subscribe reply then publish IAM request
        SR-->>IAM: fixed request user UUID
        IAM-->>SR: canonical Billing RoleEntry reply
        M->>L2: Lua generation-fenced projection write
    end
    M->>H: require exact billing plan read
    H->>H: validate cursor filters and bound Zone
    H->>S: ListPlans
    S->>Repo: read cursor page
    Repo->>DB: query Zone-scoped plans
    DB-->>H: items next cursor
```

## Failure, cache and recovery semantics

Invalidation is initiated by IAM role/status mutations: IAM increments Auth-State
generation and removes projection, publishes `authz.invalidate.billing` on
Shared Redis, and every Cost replica drops its local user entry. PubSub only
accelerates L1 eviction; the generation comparison prevents stale L2 writes.
Resolver/Redis/IAM failure is `503`, never permissive. A list retry is safe
because the workflow performs no durable mutation.

## Key contract

| Key/record | Store | Rule |
|---|---|---|
| `authz:billing:{user_id}:data/generation` | Auth-State Redis | L1/L2 may serve only matching generation; IAM is fallback authority |
| `plans.zone_id` | Billing PostgreSQL | trusted `x-zone-id` scopes query; user query cannot cross Zone |

## Code map

[`cost-manager/api/internal/app/route.go`](../../cost-manager/api/internal/app/route.go),
[`cost-manager/api/internal/transport/http/handler/plan_handler.go`](../../cost-manager/api/internal/transport/http/handler/plan_handler.go).
