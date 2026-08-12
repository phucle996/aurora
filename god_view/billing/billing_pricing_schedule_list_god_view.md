# Billing Pricing Schedule List — God View

This is one operator read workflow. It lists controlled PAYG schedule
identities; it never lists plans, packs, subscriptions or monthly prices.

## API-scope contract

`GET /api/v1/billing/pricing-schedules` is a Cost-authority operator route. The
browser sends only pagination, `charge_kind` and search. ACR verifies the
Billing Alias/session and forwards the trusted identity; Cost API performs
fresh `billing:pricing_schedule:read` authorization. No owner, wallet or Zone
is selected by the query.

## Phase 1 — Client → Envoy → ACR

| Input | Contract |
|---|---|
| Method/path | `GET /api/v1/billing/pricing-schedules?page&limit&charge_kind&search` |
| Headers used | Alias cookie, `Origin`, CSRF signal when required, `traceparent` |
| Payload | none |

```mermaid
sequenceDiagram
    participant UI as Cost Console
    participant E as Central Envoy
    participant A as ACR ExtAuthz
    participant S as Auth State Redis
    participant API as Cost API
    UI->>E: GET pricing-schedules with query and session cookie
    E->>A: CheckRequest exact method/path/used headers/no body
    A->>A: CORS, pre-auth rate limit, session verification
    A->>S: Load Billing Alias and source IAM session
    alt invalid session, origin or rate limit
        A-->>E: local 401/403/429/503
        E-->>UI: same error; API is not called
    else verified operator
        A->>A: Remove caller identity/proof override headers
        A->>A: Inject verified user and trace headers
        A-->>E: allow unchanged public path/query
        E->>API: forward exact GET
    end
```

ACR does not read the catalog and does not rewrite a schedule scope.

## Phase 2 — API handler → service → repository → PostgreSQL

```mermaid
sequenceDiagram
    participant CI as ContextInjector
    participant AU as Fresh Authorize
    participant H as PricingScheduleHandler.List
    participant S as PricingScheduleService.GetPricingSchedules
    participant R as PricingScheduleRepository.ListPricingSchedules
    participant DB as Billing PostgreSQL
    CI->>AU: parse only ACR-trusted identity
    AU->>H: require billing:pricing_schedule:read
    H->>H: bound page/limit; trim charge kind/search
    H->>S: named list request
    S->>R: page, limit, controlled charge-kind filter
    R->>DB: count schedules with indexed identity/search predicate
    R->>DB: select schedule metadata ordered by created_at/code
    DB-->>R: rows and total
    R-->>S: typed schedule identities
    S-->>H: read-only result
    H-->>CI: 200 pricing_schedules + pagination
```

The repository never joins usage, wallet or ledger data. Unknown charge-kind
filters return an empty result, while malformed query values return `400`.
Redis cache is not used as authority for this list.

## Output, keys and failure boundary

| Output | Contract |
|---|---|
| `200` | schedule id/code/display name, charge kind, model, scope, currency and metadata version |
| `401/403/429` | edge or authorization denial; no catalog data |
| `500` | sanitized PostgreSQL failure; no mutation |

Durable keys are `billing.pricing_schedules` and
`billing.charge_kind_catalog`. The list is side-effect free and retry-safe.
