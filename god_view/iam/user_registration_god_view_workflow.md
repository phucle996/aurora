# User Registration — God View (Master SoT)

Workflow này tạo self identity mới, phát verification mail và kích hoạt account.
Mục tiêu là một user chỉ trở thành `active` khi activation, default platform role
và personal-wallet provisioning intent đã commit cùng durable boundary. Không có
tenant, workspace hoặc Zone nào được tạo/chọn trong workflow này.

| Phase | Owner | Input | Output |
|---|---|---|---|
| 1. Register | Client → Envoy/ACR → Controlplane IAM | Public REST registration payload | `201` sau identity commit, hoặc `400`/`409`/`500` trước commit |
| 2. Verify dispatch | IAM → Security Redis → Kafka → Mail Dataplane | Committed pending user | One-time mail envelope; pending login là resend recovery |
| 3. Activate | Client → IAM → PostgreSQL → Billing outbox | Mail fragment rồi REST OTT proof | `active` user + platform role + durable wallet-provision intent |

## Phase 1 — Create pending self identity

Client gửi credential mới tới public endpoint. ACR chỉ áp dụng CORS/pre-auth
rate limit và forward route; không nhận Kafka credential, không sinh verification
token và không chọn mail consumer/template/Zone.

### REST input

| Part | Contract |
|---|---|
| Method/path | `POST /api/v1/auth/register` |
| Headers used | `Content-Type: application/json`; `X-Forwarded-For` chỉ để ACR rate limit và handler GeoIP lookup |
| Body | JSON object; password là opaque secret, không trim |

#### JSON payload

```json
{
  "username": "alice_01",
  "email": "alice@example.com",
  "password": "opaque-user-secret",
  "fullname": "Alice",
  "phone": "+84901234567",
  "location": "VN",
  "timezone": "Asia/Ho_Chi_Minh"
}
```

| Field | Contract |
|---|---|
| `username` | trim/lowercase; `^[a-z0-9][a-z0-9_-]{5,63}$`; không chứa `@` |
| `email` | trim/lowercase; HTTP email validation |
| `password` | >=8, có lower/upper/digit/special; không log, không trim |
| `fullname` | Required, trim |
| `phone` | Optional E.164 |
| `location` | Optional; GeoIP country thay thế khi lookup có kết quả |
| `timezone` | Optional, trim |

### Controlplane processing and REST output

1. Handler giới hạn request context 5 giây, canonicalize input và reject syntax
   trước service.
2. IAM hash password, sinh UUIDv7 user ID, đặt status `pending-active`, rồi
   gọi repository transaction.
3. Repository atomically insert `iam.users` và `iam.user_profiles`. PostgreSQL
   unique index canonical username/email là conflict authority.
4. Chỉ sau commit Phase 2 mới được attempt. Không có session, device, refresh
   token hay role được tạo ở đây.

#### Response headers

| Result | Headers |
|---|---|
| `201`/`400`/`409`/`500` | `Content-Type: application/json` |

#### Response payload

| Result | Meaning |
|---|---|
| `201` | `{"message":"account created"}`; pending identity đã commit, kể cả khi dispatch mail sau commit unavailable |
| `400` | Payload/canonicalization/password policy không hợp lệ |
| `409` | Username hoặc email canonical đã tồn tại |
| `500` | Password hash, UUID hoặc PostgreSQL transaction lỗi trước durable commit |

### Key contract

| Key / durable record | Store | Operation / TTL | Owner / purpose |
|---|---|---|---|
| `iam.users` | PostgreSQL | insert status `pending-active` | IAM identity SoT |
| `iam.user_profiles` | PostgreSQL | insert cùng transaction user | IAM self profile SoT |
| `users_email_lower_uidx`, `users_username_lower_uidx` | PostgreSQL | unique constraint | Canonical conflict fence |

```mermaid
sequenceDiagram
    participant UI as Cloud Console
    participant E as Envoy + ACR
    participant H as IAM HTTP handler
    participant S as AuthService
    participant DB as PostgreSQL

    UI->>E: POST /api/v1/auth/register
    E->>H: Public route after CORS/rate limit
    H->>H: Canonicalize and validate JSON
    H->>S: RegisterAccount
    S->>S: Hash password, UUIDv7, pending-active
    S->>DB: BEGIN; INSERT users + user_profiles; COMMIT
    alt validation, hash, UUID or transaction failure
        H-->>UI: 400/409/500; no durable pending identity on 500
    else identity committed
        S->>S: Start verification dispatch (Phase 2)
        H-->>UI: 201 account created
    end
```

## Phase 2 — Issue verification intent and deliver mail

Phase này chạy sau identity commit. Mail delivery không phải authority cho
registration: OTT/Kafka failure không rollback user và không đổi `201`. User
đăng nhập lại với đúng password để trigger resend theo cooldown nếu mail không
đến.

### IAM processing and transport output

1. `publishAccountVerification` sinh UUIDv7 `event_id` và plaintext OTT 43
   characters.
2. One-Time Token service chỉ ghi SHA-256 token vào Security Redis bằng một
   dedicated connection: `SET EX`, sau đó `WAIT` replica ACK theo config. TTL
   hoặc replication gate lỗi làm dispatch fail, không ảnh hưởng pending user.
3. IAM tạo `AccountVerificationDispatch` với `username`, `user_id`, `event_id`,
   `verify_token`; adapter marshal `MailDispatchEnvelopeV1` protobuf.
4. Kafka adapter chọn trusted topic
   `aurora.iam.account-verification.v1`; key là raw event UUID. IAM không gửi
   Zone, template, sender profile hay consumer ID.
5. Mail Dataplane terminal-reject envelope hết `not_after_unix_ms`; envelope
   còn hạn được root-owned Kafka consumer/template render và deliver.

### Key contract

| Key / transport | Store | Operation / TTL | Owner / purpose |
|---|---|---|---|
| `iam:ott:account_verify:{user_id}:{event_id}` | Security Redis | SHA-256 token; `SET EX` + optional `WAIT` | IAM; event-scoped activation proof |
| `iam:account_verify:resend_cooldown:{user_id}` | Security Redis | `SET NX EX 60s` | Pending-login resend serialization |
| `aurora.iam.account-verification.v1` | Kafka | Protobuf `MailDispatchEnvelopeV1`, key `event_id`, `acks=all` | IAM outbound adapter → root mail consumer |

```mermaid
sequenceDiagram
    participant S as IAM AuthService
    participant R as Security Redis
    participant K as Kafka
    participant M as Mail Dataplane
    participant U as User mailbox

    S->>R: SET SHA-256 OTT EX TTL; WAIT replica ACK
    alt OTT issue or Kafka publish fails
        S->>S: Emit sanitized error; retain pending identity
        Note over S: HTTP registration still returns 201
    else OTT issued
        S->>K: MailDispatchEnvelopeV1(event_id key)
        K->>M: Root consumer receives envelope
        alt envelope expired
            M->>K: Terminal reject and commit
        else envelope valid
            M->>U: Verification mail with fragment link
        end
    end
```

### Pending-login recovery

Password verification of a `pending-active` account never creates a device or
session. The service attempts `SET NX` on the resend cooldown; the winner issues
a fresh event-scoped OTT and publishes mail. A publish failure deletes cooldown
best-effort and returns authentication dependency unavailable. Existing unexpired
mail links remain valid because every `event_id` has its own OTT key.

## Phase 3 — Confirm verification and activate account

Mail link uses a fragment so plaintext OTT never enters an HTTP request, proxy
log or referrer. The activation page clears fragment before the user explicitly
confirms the REST request.

### REST input

| Part | Contract |
|---|---|
| Landing | `GET /activate#user_id=<uuid>&event_id=<uuid>&token=<ott>`; fragment is browser-local |
| Method/path | `POST /api/v1/auth/verify` |
| Headers used | `Content-Type: application/json`; no session, tenant or Zone header |

#### JSON payload

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
| `token` | Trimmed bearer proof; 32–256 characters |

### Controlplane processing and REST output

1. IAM first reads durable active state. An already-active user is an idempotent
   success path.
2. Pending user validates hash in Redis but consumes only after database commit.
   If a concurrent request activated first, IAM rereads durable state and returns
   success instead of exposing OTT race.
3. Repository transaction locks user and atomically sets `active`, ensures
   `platform_user`, and inserts deterministic personal wallet provisioning event
   into `iam.billing_outbox_records`.
4. Billing relay is notified only after commit. OTT compare-and-delete is cleanup;
   Redis failure after commit never turns durable activation into `500`.

#### Response headers

| Result | Headers |
|---|---|
| `200`/`400`/dependency error | `Content-Type: application/json` |

#### Response payload

| Result | Meaning |
|---|---|
| `200` | `{"message":"account activated successfully"}`; safe after response loss/retry |
| `400` | Invalid UUID/body or missing/wrong/expired OTT when user remains pending |
| dependency error | Redis unavailable before activation or durable activation transaction failed; retry is safe |

### Key contract

| Key / durable record | Store | Operation | Owner / purpose |
|---|---|---|---|
| `iam:ott:account_verify:{user_id}:{event_id}` | Security Redis | validate, then Lua compare-and-delete after DB commit | Activation replay fence |
| `iam.users.status` | PostgreSQL | `pending-active → active` in transaction | Account authority |
| `iam.user_role` / platform role mapping | PostgreSQL | ensure `platform_user` in same transaction | Post-activation access baseline |
| `iam.billing_outbox_records` | PostgreSQL | deterministic UUID-SHA1 event insert in same transaction | Durable handoff for personal wallet provisioning |
| `billing:wallet:personal:provision-requests` | Shared Redis Stream | outbox relay `XADD` after commit | Cost Manager wallet consumer transport |

```mermaid
sequenceDiagram
    participant UI as Activation page
    participant H as IAM handler
    participant S as AuthService
    participant R as Security Redis
    participant DB as PostgreSQL
    participant O as Billing outbox relay
    participant C as Cost Manager

    UI->>UI: Read fragment and clear browser URL
    UI->>H: POST /api/v1/auth/verify
    H->>S: VerifyAccount(user_id, event_id, token)
    S->>DB: Read active state
    alt user still pending
        S->>R: Validate OTT hash
        S->>DB: Lock user; activate + role + billing outbox; COMMIT
        S->>R: Compare-and-delete OTT best effort
    else already active
        S->>DB: Ensure role and deterministic outbox idempotently
    end
    S->>O: Notify after commit
    O->>C: Publish durable wallet provision request
    S-->>UI: 200 account activated
```

## Security and failure invariants

- Password, plaintext OTT, mail envelope, full email and token hash never enter
  application logs, browser query, analytics or persistent client storage.
- Registration client cannot select Kafka topic, root consumer, sender, template,
  Zone, wallet owner, role or Billing event ID.
- PostgreSQL identity commit is the first durable boundary. Pre-commit failures
  return `500`; post-commit verification delivery failures return `201` and rely
  on pending-login resend.
- Pending/suspended/disabled accounts never receive a device, access session,
  refresh token or cookie.
- Verification uses event-scoped tokens, one-time consumption and durable-state
  recheck; concurrent verify and response loss are idempotent.
- Wallet side effect is never published before the activation transaction commits.

## Code map

| Responsibility | Source |
|---|---|
| Public register/verify handlers | `controlplane/internal/iam/transport/http/handler/auth_handler.go` |
| Registration, resend and activation service | `controlplane/internal/iam/service/auth_service.go` |
| User/profile and activation transaction | `controlplane/internal/iam/repository/auth_repo.go` |
| OTT hash/replication/consume | `controlplane/internal/iam/service/one_time_token_service.go` |
| Verification Kafka adapter | `controlplane/internal/iam/transport/pubsub/account_verification_publisher.go` |
| Mail envelope and consumer | `proto/mail_dispatch.proto`, `dataplane/src/executor/mail/processor/stream.rs` |
| Billing outbox relay | `controlplane/internal/iam/service/billing_outbox_relay.go` |
