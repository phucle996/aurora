# Personal Storage Estimate — God View (Master SoT)

This self-user read returns a display quote for one hour of storage capacity from
the currently effective immutable pricing snapshot. It does not reserve
capacity, authorize a storage API, debit a wallet, or promise the price a later
billing run will pin. Storage is pay-as-you-go; this workflow never fabricates a
monthly commitment.

## API-scope contract

The browser uses the neutral public route. This is `/personal`: ACR derives the platform owner only from verified Cloud Trinity context or a verified Billing Alias/source IAM session, rewrites to the internal personal route, removes raw proof/workspace headers, overwrites caller identity headers, and injects trusted self context. The Cost API does not call `Authorize`/required-level middleware for self user and does not use wallet balance as an estimate input.

| Boundary | Contract |
|---|---|
| Browser method/path | `GET /api/v1/billing/wallet/estimate/storage?capacity_bytes={positive_int64}` |
| Browser headers used | `Origin`; Cloud Trinity cookies on Cloud authority or Billing Alias ID/secret cookies on Cost authority |
| Browser payload | no body; `capacity_bytes` is a base-10 query integer, `1..1<<60` |
| ACR upstream path | `/api/v1/personal/billing/wallet/estimate/storage?capacity_bytes=...` |
| Response | current hourly micro-unit estimate with snapshot code/version/checksum lineage |
| Durable effect | none |

## Phase 1 — Client → Envoy → ACR

The edge steps are deliberately the same trusted self-context selection as other personal Billing reads: CORS, pre-auth IP/device quota, Cloud Trinity verification or Alias secret/source IAM-session verification, post-auth user quota, then platform-owner path rewrite. GET skips CSRF mutation enforcement and session proof. Direct `/api/v1/personal/...` input is rejected rather than trusted.

| CheckRequest input | ACR use |
|---|---|
| `:method`, `:path`, `Origin` | CORS and neutral GET route selection |
| `X-Forwarded-For`, optional device cookie | pre-auth rate-limit key |
| Cloud Trinity or Billing Alias cookies | Cloud session verification or Cost alias record/secret and source-session recheck |
| query `capacity_bytes` | forwarded intact; not an ACR pricing value |
| client user/tenant/workspace/proof headers | removed as spoofable |

| ACR forward | Rule |
|---|---|
| rewritten `:path` | exact internal personal estimate route and original query |
| Cloud-authority injected headers | verified `x-user-id`, `x-user-name`, `x-user-level`, `x-zone-id`, `x-tenant-id`, `x-client-device-id`, `x-session-proof-verified: false`, `x-original-path`, plus `x-workspace-id` only from verified `workspace_id` cookie |
| Cost-authority injected headers | Alias `x-user-id`, `x-user-name`, `x-zone-id`, `x-tenant-id`, `x-session-proof-verified: false`, `x-original-path`; no level/device/workspace header |
| no forward | CORS, rate, alias/source session, or non-platform owner failure |

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Envoy
    participant A as ACR ext_authz
    participant CG as CorsGate
    participant RL as ACR rate limiter
    participant TV as verify_edge_session
    participant TM as TokenManager
    participant BV as BillingAliasVerifier
    participant SM as SessionManager
    participant AR as Auth-State Redis
    participant OR as OwnerPathRewriter
    participant HB as TrustedHeaderBuilder
    participant API as Cost API
    B->>E: GET neutral estimate with capacity_bytes
    E->>A: CheckRequest headers cookies path
    A->>CG: validate origin and neutral owner route
    A->>RL: pre-auth quota
    alt Cloud authority
        A->>TV: verify Trinity session
        TV->>TM: verify token binding
        TV->>SM: load IAM runtime session
        SM->>AR: GET IAM session
    else Cost authority
        A->>BV: read Alias and compare secret
        BV->>SM: load Alias then source session
        SM->>AR: GET alias and IAM session
    end
    A->>RL: post-auth quota
    A->>OR: require platform tenant and rewrite personal path
    OR->>HB: set original path and trusted headers
    HB-->>E: CheckResponse internal personal path
    E->>API: GET internal estimate route
```

## Phase 2 — Capacity validation and effective-price lookup

The storage estimate handler parses `capacity_bytes` as an `int64`, rejects zero,
negative, malformed, and values above `1<<60`, then supplies a two-second
operation context to the storage quote service. The service obtains an effective
Storage pricing snapshot from the pricing cache; cache layers accelerate a read
but cannot become pricing Source of Truth.

| Input | Validation/use |
|---|---|
| `capacity_bytes` | integer in inclusive range `1..1<<60` |
| charge kind | fixed internally to `storage.capacity.gb_hour`; caller cannot request another metric |
| L1 snapshot | in-process, one-minute TTL, immutable, current effective window required |
| L2 snapshot | Shared Redis key during migration, five-minute maximum TTL, fully revalidated before use |
| DB fallback | Scoped Pricing Schedule repository is durable authority after schedule cutover |

```mermaid
sequenceDiagram
    participant H as StorageQuoteHandler
    participant S as StorageQuoteWorkflow
    participant L1 as Pricing L1
    participant L2 as Shared Redis
    participant R as PricingScheduleRepository
    participant DB as Billing PostgreSQL
    H->>H: parse capacity_bytes and set 2s deadline
    H->>S: EstimateStorage capacity
    S->>L1: get effective STORAGE snapshot
    alt valid L1 hit
        L1-->>S: immutable snapshot
    else L1 miss
        S->>L2: GET active storage snapshot
        alt valid L2 bytes
            L2-->>S: snapshot after schema range checksum validation
        else cache miss invalid or stale
            S->>R: Get active Storage schedule snapshot
            R->>DB: select scoped effective schedule version and brackets
            DB-->>S: immutable snapshot
            S->>L2: best-effort cache write
        end
    end
```

### Cache integrity and recovery

Before a Redis payload can answer an estimate, the cache validates IDs, service type, version/effective window, currency/checksum shape, continuous non-negative ranges from zero to one infinity range, and recalculates a 64-character checksum when present. `singleflight` prevents local miss stampedes. An invalidation generation fence prevents an in-flight old read from repopulating L1 after a pricing publish. Redis failure/missed PubSub never makes stale cache authoritative: the request falls through to PostgreSQL, and TTL/cold start rebuilds state.

## Phase 3 — Exact progressive quote calculation and response

For each effective range, the storage quote service calculates the covered bytes
and micro-unit price using checked integer arithmetic, rejects numeric overflow,
and returns one hourly amount. It returns snapshot lineage so the UI can explain
which quote was observed; that lineage is not a future billing reservation.

| Response payload field | Meaning |
|---|---|
| `capacity_bytes` | requested capacity |
| `hourly_estimate_micro_units` | rounded-up progressive hourly charge |
| `currency` | snapshot currency |
| `pricing_schedule_code`, `pricing_schedule_id`, `pricing_schedule_version_id` | immutable effective schedule identity |
| `pricing_version`, `pricing_checksum`, `pricing_effective_from` | audit/display lineage |
| `estimated_at` | UTC calculation time |

| Result | HTTP response | Durable effect |
|---|---|---|
| valid snapshot and arithmetic | `200` estimate payload | none |
| invalid capacity or invalid durable range | `400` | none |
| no effective storage schedule or cache/DB timeout/error | `503` | none |

```mermaid
sequenceDiagram
    participant S as StorageQuoteWorkflow
    participant PS as PricingSnapshot
    participant H as StorageQuoteHandler
    S->>PS: iterate continuous progressive ranges
    S->>S: price decimal-GB capacity using checked integer arithmetic
    S->>S: return one hourly amount with final rounding
    S-->>H: estimate plus immutable snapshot lineage
    H-->>H: encode 200 payload
```

## Key contract

| Key/table | Owner and rule |
|---|---|
| Cloud Trinity session or `iam:domain_alias:billing:{alias_id}` | ACR self-context binding; Cost Alias rechecks source IAM session |
| process L1 pricing map | one-minute performance cache, generation-fenced |
| `cost-manager:pricing:schedule:v2:storage.capacity.gb_hour:zone:{zone_id}` | Shared Redis five-minute performance cache; must pass full integrity checks |
| effective Pricing Schedule/version/brackets in Billing PostgreSQL | durable pricing SoT |
| `billing.wallets`, `payment_intents`, ledger | intentionally untouched by quote workflow |

## Code map

[`acr/src/gateway/ext_authz.rs`](../../acr/src/gateway/ext_authz.rs), [`cost-manager/api/internal/transport/http/handler/pricing_schedule_handler.go`](../../cost-manager/api/internal/transport/http/handler/pricing_schedule_handler.go), [`cost-manager/api/internal/service/pricing_schedule_service.go`](../../cost-manager/api/internal/service/pricing_schedule_service.go), and [`cost-manager/api/internal/service/pricing_cache.go`](../../cost-manager/api/internal/service/pricing_cache.go).
