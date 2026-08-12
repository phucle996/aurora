# Billing Pricing Schedule Version Publish — God View

This operator workflow appends one immutable version of a controlled pricing
schedule. It never edits a historical version, never accepts a client-owned
price model, and never mutates a wallet. PostgreSQL is the pricing Source of
Truth; the outbox and Redis notification only deliver a version hint to Cost
Engine.

## API-scope contract

This is a Cost-authority critical workflow. The operator browser calls the
neutral public route and does not select a wallet, payer, owner, workspace,
tenant or Zone authority. ACR verifies the Cost Billing Alias session and the
one-time session proof. Controlplane/Cost API performs fresh
`billing:pricing_schedule:publish` authorization after the request crosses the
edge. A Zone schedule may be published only for the verified operator Zone;
the body cannot override that scope.

The endpoint does not accept `service_type`, `tier_id`, `plan_id`,
`monthly_price`, `included_quota`, `zone_multiplier`, discount, tax or formula
fields. The schedule identity and charge-kind model/unit come from the locked
Charge Kind Registry row.

## REST input and output

### Request

| Item | Contract |
|---|---|
| Method/path | `POST /api/v1/billing/critical/pricing-schedules/{code}/versions` |
| Headers used | Billing Alias cookie, exact session-proof headers, `Origin`, CSRF signal, `traceparent` |
| JSON | `expected_latest_version`, `effective_from`, `change_reason`, and typed `brackets` for the enabled Storage `PROGRESSIVE_UNIT` schedules. `FIXED_BUNDLE`/`definition` is rejected until a Hypervisor workflow registers its validator. |
| Authority | Alias session, verified operator identity and verified Zone injected by ACR; never caller `x-user-id`, `x-zone-id` or workspace header |

`brackets` use raw quantity units from the registry. Every progressive version
starts at zero, is contiguous, has one final unbounded range, uses a positive
denominator and non-negative numerator. The service canonicalizes field order
and calculates the checksum; it does not trust a client checksum.

### Response

| Status | Payload | Durable effect |
|---|---|---|
| `201` | schedule id, version id/number, charge kind, scope, model, unit, effective window, checksum | Version, typed definition/brackets and publish outbox committed atomically |
| `400` | bounded validation code | None |
| `401/403/429` | edge/proof/authorization error | None |
| `404` | charge kind or schedule absent | None |
| `409` | OCC/effective-window conflict | None |
| `500/503` | sanitized dependency error | No partial version or outbox |

The response never returns a wallet balance, secret, credential or raw database
error. A future read/quote workflow is separate from this publish mutation.

## Key and durable contract

| Key/table | Owner and invariant |
|---|---|
| `billing.charge_kind_catalog` | Controlled registry; disabled/unknown kind cannot publish |
| `billing.pricing_schedules` | One logical schedule per `(charge_kind_code, scope)` |
| `billing.pricing_schedule_versions` | Immutable version/checksum/effective interval; exclusion constraint prevents overlap |
| `billing.pricing_schedule_scalar_brackets` | Typed raw-unit bracket rows; no JSON formula |
| `billing.pricing_outbox` | PostgreSQL delivery SoT; version row and outbox commit together |
| `billing.pricing_schedule_access` | Optional operator audit projection; not pricing authority |
| `billing.pricing:version:{version_id}` | Cache is immutable performance data, never SoT |
| `billing.pricing.schedule.published.v1` | Shared Redis PubSub latency hint; loss is repaired by reconciliation |

## Phase 1 — Client → Envoy → ACR

Envoy sends the exact method, public path, used headers and bounded raw JSON in
the ext_authz `CheckRequest`. ACR executes Cost CORS, pre/post-auth rate limit,
Alias/session verification, CSRF, proof signature/hash verification and
one-time proof consumption. It removes caller authority headers and overwrites
trusted identity, tenant and Zone headers. It keeps the public path; there is
no owner rewrite.

```mermaid
sequenceDiagram
    participant UI as Cost Console
    participant E as Central Envoy
    participant A as ACR ExtAuthz
    participant AR as Auth-State Redis
    participant V as Vault proof verifier
    participant API as Cost API

    UI->>E: POST pricing schedule version with exact JSON and proof headers
    E->>A: CheckRequest method path headers bounded raw body
    A->>A: CORS and pre-auth rate limit
    A->>AR: Verify Billing Alias and source IAM session
    A->>A: Post-auth rate limit and CSRF
    A->>V: Verify exact method/path/body hash and consume one-time proof
    alt invalid identity, proof, CSRF or route authority
        A-->>E: Local 401/403/429/503
        E-->>UI: Error; no Cost API call
    else verified operator
        A->>A: Remove caller identity, Zone, workspace and proof headers
        A->>A: Inject verified user, tenant, Zone and proof marker
        A-->>E: Allow; unchanged public path/body
        E->>API: Forward trusted request
    end
```

ACR does not load schedules, validate brackets, choose a charge kind or write
an outbox. Any ACR dependency failure fails closed.

## Phase 2 — Cost API authorization and immutable transaction

The Cost API context injector parses only ACR-trusted headers. The critical
proof middleware checks the proof marker. Fresh authorization resolves the
operator permission; an L1/L2 cache hit is not enough for publish. The handler
binds the named workflow request and passes it to the Schedule service.

The service locks the Charge Kind Registry row and derives model/unit/scope from
it. It validates the typed payload, canonicalizes it and computes a checksum.
The repository locks the schedule row, compares `expected_latest_version`,
checks the effective window and inserts version, brackets/definition and one
outbox row in the same transaction. Existing versions are never updated or
deleted.

```mermaid
sequenceDiagram
    participant CI as ContextInjector
    participant SP as SessionProof middleware
    participant AU as Fresh Authorize
    participant H as PricingScheduleHandler
    participant S as PricingScheduleService
    participant R as PricingScheduleRepository
    participant DB as Billing PostgreSQL

    CI->>SP: Trusted headers and proof marker
    SP->>AU: Require fresh pricing schedule publish permission
    AU->>H: Authorized workflow request
    H->>S: Bind charge-kind path and typed JSON
    S->>DB: Lock controlled charge-kind registry row
    DB-->>S: Model, unit, scope and enabled state
    S->>S: Validate typed ranges/definition and canonical checksum
    S->>R: Append immutable version command
    R->>DB: BEGIN; lock schedule; OCC/effective checks
    R->>DB: INSERT version, brackets/definition and pricing outbox
    alt all invariants hold
        DB-->>R: COMMIT
        R-->>H: Version identity and checksum
        H-->>CI: 201 immutable version
    else conflict or invalid schedule
        DB-->>R: ROLLBACK
        R-->>H: 400/404/409/500
        H-->>CI: No partial state
    end
```

No generic context is created to reduce arguments. The request type, service
and repository belong only to this publish workflow. Any private validation
function must remain local to the schedule service and preserve the typed
registry invariant.

## Phase 3 — Outbox relay and Cost Engine activation hint

After commit, the relay claims committed outbox rows and publishes only the
version identity, checksum and scope on the versioned Redis channel. It marks
the outbox published only after the publish operation is accepted. A crash
before marking published causes a duplicate hint, not a duplicate schedule.

Cost Engine treats Redis as a latency hint. It loads the exact immutable version
from Billing PostgreSQL, checks the checksum, validates the typed model and
preloads its local cache. A running settlement keeps its pinned version; a new
report resolves the schedule at its closed `window_end` using Zone exact then
Global fallback.

```mermaid
sequenceDiagram
    participant O as PricingScheduleOutboxRelay
    participant DB as Billing PostgreSQL
    participant R as Shared Redis PubSub
    participant E as Cost Engine
    participant C as Immutable schedule cache

    O->>DB: Claim committed unpublished outbox row
    O->>R: Publish PricingScheduleVersionPublishedV1
    R-->>E: Version hint; may duplicate or be lost
    E->>DB: Load exact version/brackets/definition
    E->>E: Verify checksum, model, unit and effective window
    E->>C: Preload immutable snapshot by version id
    E->>DB: Reconcile missing hints on startup/periodic scan
```

## Failure, replay and security rules

| Failure | Behavior |
|---|---|
| Proof or fresh permission fails | Edge/API deny before PostgreSQL |
| Unknown/disabled charge kind | `404`/`400`, no schedule mutation |
| OCC/effective overlap | `409`, no version/outbox |
| PostgreSQL commits before relay crash | Outbox reconciliation republishes exact identity |
| Redis duplicate/loss | Engine reloads immutable version; no duplicate schedule or wallet debit |
| Invalid checksum/model in Engine | Fail closed for that pricing work; never guess a rate |
| New version during a running report | Existing run keeps pinned version/checksum |
| Client sends Zone/owner/model override | Ignored or rejected; trusted registry/context wins |

## Code map and implementation boundary

The implementation owner is the Cost API pricing-schedule workflow and its
dedicated repository/service/handler. It must not import Storage report
transport, wallet settlement, Zone runtime or a shared application context.
The relay and Engine loader are separate workflow owners with their own input
types and failure boundaries.

The removed legacy catalog workflow must not be used as runtime authority after
the PAYG cutover; this schedule workflow is the only pricing mutation path.
