# Account Verification and Personal Wallet Provisioning — God View (Master SoT)

Workflow này bắt đầu khi user mở verification link của pending account. Nó là
owner duy nhất của transition `pending-active → active`, default `platform_user`
role và durable request tạo personal USD wallet. Registration không thực hiện
những side effect này.

| Phase | Owner | Input | Output |
|---|---|---|---|
| 1. Confirm verification | User → Cloud Console → IAM | Fragment OTT then REST confirmation | Validated event-scoped activation proof |
| 2. Activate atomically | IAM → PostgreSQL | Valid proof or already-active retry | Active user, platform role and Billing outbox record commit together |
| 3. Provision wallet | IAM outbox relay → Shared Redis Stream → Cost Manager | Committed personal-wallet event | Idempotent `(user_id, PERSONAL, USD)` wallet at balance zero |

## Phase 1 — Confirm event-scoped activation proof

The mail link is intentionally a browser fragment: mailbox scanners and network
proxies cannot activate an account, and plaintext OTT does not enter request URL
logs. The landing page clears it before an explicit user confirmation.

### REST input

| Part | Contract |
|---|---|
| Landing | `GET /activate#user_id=<uuid>&event_id=<uuid>&token=<plaintext-ott>` |
| Method/path | `POST /api/v1/auth/verify` |
| Headers used | `Content-Type: application/json`; no session, tenant or Zone |

```json
{
  "user_id": "019f...",
  "event_id": "019f...",
  "token": "plaintext-ott"
}
```

| Field | Contract |
|---|---|
| `user_id`, `event_id` | Required non-nil UUIDs |
| `token` | Trimmed bearer proof, 32–256 chars |

### Browser and IAM processing/output

1. `/activate` reads fragment once, sets `Referrer-Policy: no-referrer`, clears
   history and waits for user confirmation.
2. IAM first reads durable active state. Already active is a safe retry branch.
3. Pending user validates SHA-256 OTT in Security Redis but does not consume it
   yet. If a concurrent verify wins, IAM rereads active state and returns success.

| Result | Output |
|---|---|
| Valid pending proof or already active | Continue Phase 2 |
| Invalid UUID/body or wrong/missing/expired OTT while pending | `400`; pending-login resend is recovery |
| Redis unavailable before activation | Dependency error; no durable mutation |

### Key contract

| Key | Store | Operation | Owner / purpose |
|---|---|---|---|
| `iam:ott:account_verify:{user_id}:{event_id}` | Security Redis | Hash comparison before DB transaction; compare-and-delete only after commit | Event replay/expiry fence |
| `iam.users.status` | PostgreSQL | Read before OTT validation and in activation transaction | Durable idempotency authority |

## Phase 2 — Commit activation, baseline role and outbox together

The durable boundary is one IAM PostgreSQL transaction. No active user can
commit without both baseline role and the corresponding wallet-provision event.

### IAM processing and REST output

1. Service derives deterministic `billing_event_id = UUID-SHA1(OID,
   "billing.wallet.personal.provision:" + user_id)` and marshals
   `PersonalWalletProvisionRequestedV1`.
2. Repository locks user. Pending row becomes `active`; active retry ensures
   the same role and event instead of creating a second logical event.
3. It ensures `platform_user` in `iam.user_role` and inserts one
   `iam.billing_outbox_records` record with `ON CONFLICT DO NOTHING` under the
   deterministic event ID.
4. After commit IAM wakes relay. Redis OTT consume is cleanup; its failure never
   changes committed activation into an HTTP failure.

#### Response headers and payload

| Result | Headers / payload |
|---|---|
| Success | `200`, `Content-Type: application/json`, `{"message":"account activated successfully"}` |
| Role/outbox/activation transaction failure | Dependency error; OTT remains valid for retry if user is still pending |

### Key contract

| Durable record | Store | Operation | Owner / purpose |
|---|---|---|---|
| `iam.users.status` | PostgreSQL | `pending-active → active` | Account authority |
| platform role mapping / `iam.user_role` | PostgreSQL | Ensure `platform_user` in same transaction | Minimum post-activation authorization |
| `iam.billing_outbox_records` | PostgreSQL | Insert deterministic event with `PENDING` status | Durable handoff to Billing |
| `billing_event_id` | UUID-SHA1 | Same user always produces same ID | Retry/concurrency idempotency |

```mermaid
sequenceDiagram
    participant UI as Activation page
    participant H as IAM handler
    participant S as AuthService
    participant R as Security Redis
    participant DB as PostgreSQL
    participant Relay as Billing outbox relay

    UI->>UI: Read and clear fragment; user confirms
    UI->>H: POST /api/v1/auth/verify
    H->>S: VerifyAccount(user_id, event_id, token)
    S->>DB: Read active state
    alt pending account
        S->>R: Validate OTT hash
    end
    S->>DB: Lock user; activate + platform_user + outbox; COMMIT
    S->>R: Compare-and-delete OTT best effort
    S->>Relay: Notify after commit
    S-->>UI: 200 account activated
```

## Phase 3 — Apply personal wallet provisioning event

Outbox delivery is asynchronous and at-least-once. Account activation is already
durable and successful before Cost Manager sees the event; wallet projection must
therefore be idempotent rather than feeding failure back into the verify request.

### Transport and processing

1. Relay claims committed `PENDING` IAM outbox rows and writes the protobuf to
   `billing:wallet:personal:provision-requests`, observing configured `WAITAOF`
   durability before marking row `PUBLISHED`.
2. Cost Manager consumer validates envelope/inbox hash and runs one transaction:
   record inbox delivery and ensure wallet `(owner_id=user_id, owner_type=PERSONAL,
   currency=USD)` with zero balance.
3. Duplicate stream delivery or relay retry sees existing inbox/event/wallet and
   is an idempotent success. Failure remains retryable through outbox/inbox flow.

### Key contract

| Record / transport | Store | Operation | Owner / purpose |
|---|---|---|---|
| `iam.billing_outbox_records` | PostgreSQL IAM | Claim/retry/mark `PUBLISHED` after transport durability | IAM durable producer evidence |
| `billing:wallet:personal:provision-requests` | Shared Redis Stream | Protobuf event append | IAM → Cost Manager transport |
| Cost inbox record | Cost PostgreSQL | Event/hash dedupe in wallet transaction | At-least-once consumer fence |
| Personal wallet `(owner_id, PERSONAL, USD)` | Cost PostgreSQL | Create-if-absent, balance `0` | Personal wallet SoT |

```mermaid
sequenceDiagram
    participant DB as IAM PostgreSQL
    participant Relay as IAM outbox relay
    participant Stream as Shared Redis Stream
    participant Cost as Cost Manager
    participant CDB as Cost PostgreSQL

    Relay->>DB: Claim committed wallet-provision outbox row
    Relay->>Stream: XADD PersonalWalletProvisionRequestedV1
    Relay->>DB: Mark published after WAITAOF policy
    Stream->>Cost: Consumer-group delivery
    Cost->>CDB: Inbox dedupe + ensure personal USD wallet
    CDB-->>Cost: Commit once
    Cost->>Stream: ACK after durable apply
```

## Security and code map

- Fragment OTT never appears in HTTP query/referrer; raw token and hash are not
  logged, persisted in browser storage or emitted to Billing.
- Validate-before-commit and consume-after-commit preserve retry after database
  failure; active-state reread makes race/response-loss verification idempotent.
- Activation creates no tenant, workspace or Zone. Cost receives only the
  committed personal-wallet event and cannot alter IAM user/role state.

| Responsibility | Source |
|---|---|
| Activation page | `cloud-console/src/app/activate/page.tsx` |
| Verify route/handler | `controlplane/internal/iam/route.go`, `transport/http/handler/auth_handler.go` |
| Verify orchestration | `controlplane/internal/iam/service/auth_service.go` |
| OTT validation/consume | `controlplane/internal/iam/service/one_time_token_service.go` |
| Activation/outbox transaction | `controlplane/internal/iam/repository/auth_repo.go` |
| IAM outbox relay | `controlplane/internal/iam/service/billing_outbox_relay.go` |
| Cost wallet consumer | `cost-manager/api/internal/transport/redis/handler/personal_wallet_provision_handler.go` |
