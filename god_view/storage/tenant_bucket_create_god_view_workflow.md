# Tenant Bucket Create — God View

Tenant Bucket creation is an asynchronous provisioning mutation for enterprise workspaces.
Controlplane executes commercial admission verification for the tenant, generates physical
bucket identifiers, initial bootstrap credentials, and writes a sealed Zone outbox command
within an atomic CTE transaction. A `201 Created` returns the durable bucket entity and the
one-time plaintext bootstrap credentials; physical MinIO provisioning occurs asynchronously
via Dataplane at the edge zone.

---

## API-scope contract

Browser calls neutral `POST /api/v1/critical/storage/buckets` with a session proof
bound to the exact method, public path and body. ACR validates the proof and Trinity tenant membership,
resolves workspace and zone context, rewrites the path to `/api/v1/tenant/critical/storage/buckets`,
and injects trusted identity headers (`x-user-id`, `x-workspace-id`, `x-zone-id`, `x-tenant-id`).
Controlplane runs `RequireSessionProof` before requiring `storage:bucket:write`. Repository enforces 3-table join
verification (`tenant_workspaces`, `tenant_memberships`, `tenant_buckets`) before committing.

---

## REST input and output

### Request headers used

| Header | Use |
|---|---|
| `Cookie` | ACR derives Trinity session, tenant membership, Zone, and workspace context. |
| `Origin` | CORS enforcement at Envoy / ACR. |
| `X-Requested-With` or `Sec-Fetch-Site` | Required by ACR CSRF check for `POST`. |
| `x-session-proof-challenge-id`, `x-session-proof-timestamp`, `x-session-proof-signature` | Exact-method/path/body proof. ACR consumes/removes cryptographic fields and overwrites upstream with verified marker/challenge only. |
| `traceparent` | Injected into outbox record for distributed OpenTelemetry tracing. |

### JSON payload

```json
{
  "name": "company-assets",
  "quota_bytes": 107374182400
}
```

| Field | Type | Contract |
|---|---|---|
| `name` | `string` | Required, trimmed, 1–51 byte, lowercase ASCII/digit/hyphen, with lowercase ASCII or digit at both ends. Converted to physical name `tn-{tenant_id[:8]}-{name}` so the physical name remains at most 63 bytes. |
| `quota_bytes` | `integer` | Accepted as `int64`; Dataplane applies a hard quota only when positive. |

### Response payload

| Status | Payload | Reason |
|---|---|---|
| `201` | `data` contains flat `bucket_id`, `bucket_name`, `credential_id`, `access_key`, one-time `secret_key` and `policy` | Tenant bucket, bootstrap credential, resource admission and protected command committed. The neutral Console contract is identical to Personal create. |
| `400` | Error envelope | Invalid JSON or logical bucket-name contract. |
| `401` | `{"status": "error", "code": "UNAUTHORIZED", "message": "unauthorized"}` | Missing or invalid Trinity session cookie. |
| `403` | `{"status": "error", "code": "FORBIDDEN", "message": "permission denied"}` | Missing `storage:bucket:write` permission grant or inactive tenant membership. |
| `409` | `{"status": "error", "code": "CONFLICT", "message": "bucket name already exists"}` | Physical bucket name collision. |
| `503` | `{"error": "STORAGE_COMMERCIAL_ADMISSION_UNAVAILABLE", "message": "Service Unavailable"}` | Tenant commercial admission gate denied. |
| `500` | `{"status": "error", "code": "INTERNAL_ERROR", "message": "internal_error"}` | Database error, payload sealing failure, or outbox insert error. |

---

## Key and transport contract

| Key / Transport | Store | Operation | Invariant |
|---|---|---|---|
| Auth-State session | Redis (Central) | Read / verify | Edge must verify tenant membership and inject authentic `x-tenant-id`. |
| `hierarchy.tenant_workspaces` | PostgreSQL | Key share check | Source of truth for `zone_id` and `tenant_id`. |
| `hierarchy.tenant_memberships` | PostgreSQL | Read under CTE | User must be in `active` status within the tenant. |
| `storage.tenant_buckets` | PostgreSQL | Insert | Physical bucket record owned by the workspace. |
| `storage.tenant_credentials` | PostgreSQL | Insert `state=CREATING` | One-time bootstrap credential promise; secret remains response/payload-only. |
| `storage.storage_outbox_records` | PostgreSQL | Insert | Topic `storage.bucket.create`, payload `BucketCreateSync`. |
| `storage.resource_admission_projection` | PostgreSQL | Insert in the same create transaction | Exact bucket receives the locked current Tenant `ALLOW` snapshot. |
| `storage.bucket.create` | Kafka (Zone Command) | At-least-once publish | Sealed payload carries bucket, Tenant owner, workspace and Zone registration fields. |
| `storage.bucket.head.{bucket_id}` | Zone config KV | CAS after MinIO success | Runtime head contains exact Tenant/workspace/Zone and trusted physical name. |
| `AURORA_ZONE_ADMISSION/{bucket_id}` | Zone admission KV | CAS from create snapshot | Bucket-ID commercial lookup. |
| `AURORA_ZONE_ADMISSION/name/{physical_name}` | Zone admission KV | CAS the same value | SDK physical-name commercial lookup. |

---

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    autonumber
    actor Browser as Cloud Console (Tenant)
    participant Envoy as Central Envoy Gateway
    participant ACR as ACR (ExtAuthz)
    participant Redis as Auth-State Redis
    participant CP as Controlplane

    Browser->>Envoy: POST /api/v1/critical/storage/buckets with JSON and proof
    Envoy->>ACR: CheckRequest (headers, cookie, path, body)
    ACR->>Redis: Validate Trinity tenant session & resolve workspace/zone
    ACR->>ACR: Check CSRF & enforce rate limits
    alt Invalid Session, Inactive Member, or Forbidden
        ACR-->>Envoy: Deny (401 / 403 / 429)
        Envoy-->>Browser: HTTP Error
    else Verified Tenant Session
        ACR->>ACR: Strip untrusted headers & inject x-user-id, x-tenant-id, x-workspace-id, x-zone-id
        ACR->>ACR: Rewrite path to /api/v1/tenant/critical/storage/buckets
        ACR-->>Envoy: Ok (with mutation headers & rewritten path)
        Envoy->>CP: Forward POST /api/v1/tenant/critical/storage/buckets
    end
```

### Hop-by-Hop Contract — Phase 1

#### Hop 1.1: Browser → Central Envoy Gateway
- **Input**:
  - Method: `POST`
  - URL: `/api/v1/critical/storage/buckets`
  - Headers: `Cookie: trinity_session=...; workspace_id=...`, `Origin: https://console.aurora.local`, `Content-Type: application/json`, `X-Requested-With: XMLHttpRequest`
  - Body: `{"name": "company-assets", "quota_bytes": 107374182400, "...": "advanced options"}` plus session-proof headers minted for the exact request.
- **Output**: Forwarded verbatim as gRPC `CheckRequest` to ACR ExtAuthz filter.

#### Hop 1.2: Envoy → ACR (ExtAuthz gRPC CheckRequest)
- **Input**:
  - `envoy.service.auth.v3.CheckRequest` with HTTP attributes.
- **Processing**:
  1. Validates CORS origin and rate limits.
  2. Queries Auth-State Redis with `trinity_session` key to extract `user_id`, `tenant_id`, and `zone_id`.
  3. Verifies workspace cookie corresponds to an authorized workspace within the tenant.
- **Output**:
  - On success: `OkHttpResponse` containing:
    - `headers_to_remove`: cookie/authorization, caller identity/context and raw session-proof signature/timestamp/challenge copies.
    - `headers_to_add` with overwrite semantics: verified `x-user-id`, `x-user-name`, `x-user-level`, `x-tenant-id`, `x-workspace-id`, `x-zone-id`, `x-client-device-id`, `x-session-proof-verified=true`, verified challenge id and `x-original-path`.
    - `path_mutation`: rewrite `:path` to `/api/v1/tenant/critical/storage/buckets`.

#### Hop 1.3: Envoy → Controlplane Upstream
- **Input**:
  - `POST /api/v1/tenant/critical/storage/buckets` with trusted headers and request body.
- **Output**: HTTP stream to Controlplane Go HTTP router.

---

## Phase 2 — Controlplane Desired-State Transaction

```mermaid
sequenceDiagram
    autonumber
    participant Envoy as Central Envoy Gateway
    participant AuthMW as Authorize Middleware
    participant Handler as TenantBucketHandler
    participant Service as TenantBucketService
    participant Repo as TenantBucketRepository
    participant Protector as Payload Protector
    participant PG as PostgreSQL

    Envoy->>AuthMW: Request with trusted headers
    AuthMW->>AuthMW: Verify tenant:storage:bucket:write grant
    AuthMW->>Handler: Dispatch Create(c)
    Handler->>Service: CreateBucketForTenant(param)
    Service->>Service: Generate UUIDv7 IDs, Access/Secret Keys, Policy
    Service->>Service: Build BucketCreateSync schema 2 skeleton
    Service->>Repo: Create(bucket, credential, outboxRecord)
    Repo->>PG: Lock current Tenant ALLOW admission FOR KEY SHARE
    Repo->>Repo: Copy locked snapshot into BucketCreateSync
    Repo->>Protector: Seal payload bytes inside transaction
    Repo->>PG: Execute ownership/admission CTE and four inserts
    PG-->>Repo: Commit bucket, credential, outbox and resource admission
    Repo-->>Service: Success
    Service-->>Handler: CreatedBucketResult (Bucket + Plaintext Secret)
    Handler-->>Envoy: 201 Created JSON
    Envoy-->>Browser: 201 Created JSON
```

### Hop-by-Hop Contract — Phase 2

#### Hop 2.1: ContextInjector & Authorize Middleware
- **Input**: Headers `x-user-id`, `x-tenant-id`, `x-workspace-id`, `x-zone-id`.
- **Processing**: Evaluates L1 Permission Registry for key `{tenant_id}:{workspace_id}:storage:bucket:write` (or wildcard).
- **Output**: Validated request context.

#### Hop 2.2: TenantBucketHandler → TenantBucketService
- **Input**: `CreateTenantBucketRequest` DTO containing `name` and `capacity_quota_bytes`.
- **Processing**:
  1. Validates the 1–51 byte logical bucket-name contract.
  2. Copies only ACR-verified Tenant/workspace/Zone/user scope into the command.
  3. Formats physical bucket name: `tn-{tenant_id[:8]}-{name}`.
  4. Generates cryptographically secure access key and secret key.
  5. Builds standard AWS S3 policy scoped to `arn:aws:s3:::{physicalName}/*`.
  6. Constructs the schema-2 `BucketCreateSync` skeleton; the repository supplies its locked admission snapshot.
- **Output**: Call to `repo.Create(ctx, bucket, credential, outboxRecord)`.

#### Hop 2.3: TenantBucketRepository → PostgreSQL Atomic Transaction

Repository first locks the current non-expired Tenant `ALLOW` owner row with
`FOR KEY SHARE`. It injects exactly that version, validity and source event into
the schema-2 create protobuf and seals the payload while the lock is held.
This serializes create against the projection consumer's owner-row update.

Its CTE then rechecks the same admission and the four-dimensional durable scope
`(workspace_id, tenant_id, actor_user_id active membership, zone_id)`. From the
authorized row it inserts one `PROVISIONING` bucket, one bootstrap credential,
one pending protected outbox command and one
`storage.resource_admission_projection` row. Any missing scope, admission
change, seal error, SQL error or conflict rolls the complete transaction back.

#### Hop 2.4: Controlplane → Browser
- **Input**: `CreatedBucketResult` entity.
- **Output**: HTTP `201 Created` JSON:
  ```json
  {
    "status": "success",
    "data": {
      "bucket_id": "01916fe8-444a-714d-91b5-555e5fbdd98b",
      "bucket_name": "tn-b78f9a2c-company-assets",
      "credential_id": "01916fe8-444a-714d-91b5-555e5fbdd98c",
      "access_key": "AKIA...",
      "secret_key": "wJalrXUtnFEMI...",
      "policy": "..."
    },
    "message": "tenant bucket created successfully"
  }
  ```

---

## Phase 3 — Outbox CDC Dispatch & Dataplane Execution

```mermaid
sequenceDiagram
    autonumber
    participant PG as PostgreSQL (Outbox)
    participant JO as Job Orchestrator CDC
    participant KafkaCmd as Zone Command Kafka
    participant DP as Zone Dataplane (BucketCreateExecutor)
    participant MinIO as MinIO Cluster
    participant KV as Zone config KV

    PG-->>JO: Read committed outbox record (topic: storage.bucket.create)
    JO->>KafkaCmd: Publish JobCommandV1 (sealed BucketCreateSync, target zone)
    KafkaCmd-->>DP: Consume job command
    DP->>DP: Decrypt payload & decode BucketCreateSync
    DP->>MinIO: S3 CreateBucket
    DP->>MinIO: MinIO Admin SetBucketQuota (Hard Quota)
    DP->>MinIO: MinIO Admin CreateServiceAccount (Access Key, Secret Key, Policy)
    MinIO-->>DP: Provisioning succeeded
    DP->>KV: CAS storage.bucket.head.bucket_id with Tenant scope and physical name
    DP->>KV: CAS admission bucket_id and name/physical_name
```

`BucketCreateSync` schema 2 carries the locked Central `ALLOW` snapshot in
addition to exact bucket/Tenant/workspace/Zone registration. The runtime key is
`AURORA_ZONE_CONFIG/storage.bucket.head.{bucket_id}`; its JSON schema is
`schema_version=1`, `runtime_read_enabled=true`, `module="storage"`,
`resource_type="bucket"`, exact `resource_id`, server-owned `resource_name`,
`version=1`, create `event_id`, `tombstoned=false`, Tenant `owner_id`,
`owner_type="TENANT"`, `workspace_id` and `zone_id`.

Dataplane writes the same admission JSON to
`AURORA_ZONE_ADMISSION/{bucket_id}` and
`AURORA_ZONE_ADMISSION/name/{physical_name}` with `resource_id`,
`resource_name`, `policy_version`, `decision='ALLOW'`, null reason,
effective/optional expiry Unix seconds and `source_event_id`. Equal-version
replay succeeds only for exact record and resource identity equality. A runtime
or admission KV failure is retryable, and JO does not mark the bucket `READY`
until every required Zone projection succeeds.

After the executor returns a terminal result and before publishing that result,
Dataplane CAS-creates
`AURORA_ZONE_JOB_COMPLETION/job.completion.{job_id}.{delivery_epoch}` as protobuf
`JobCompletionReceiptV1`. Its phase-local schema is `schema_version=2`,
`command_sha256`, `attempt`, `message`, `result_payload`,
`result_payload_schema_version`, `result_status` and optional `error_code`.
It is execution replay evidence only, never Tenant or bucket authority: a
matching command hash reuses the terminal result; conflict or corruption fails
closed.

### Hop-by-Hop Contract — Phase 3

#### Hop 3.1: PostgreSQL WAL → Job Orchestrator CDC Engine
- **Input**: Database CDC changefeed record from `storage.storage_outbox_records` where `job_topic = 'storage.bucket.create'` and `owner_type = 'TENANT'`.
- **Output**: `JobCommandV1` struct partitioned by `zone_id`.

#### Hop 3.2: Job Orchestrator → Zone Command Kafka Topic
- **Input**: `JobCommandV1` published to topic `aurora.zone.{zone_id}.storage.bucket.create`.
- **Output**: Message committed to Kafka broker partition.

#### Hop 3.3: Kafka → Zone Dataplane (`BucketCreateExecutor`)
- **Input**: Consumed Kafka record delivered to Dataplane runtime.
- **Processing**:
  1. Decrypts payload and validates the schema-2 registration plus locked `ALLOW` admission snapshot.
  2. Calls S3 `CreateBucket` with physical bucket name.
  3. Calls MinIO Admin API to apply hard storage quota.
  4. Calls MinIO Admin API to create user / service account and attach scoped policy.
  5. CAS-creates the runtime head and both admission indexes.
- **Output**: MinIO and all required Zone projections are provisioned.

---

---

## Phase 4 — Job Settlement & Resource Ownership Handover (Billing Registry)

JO settles resource first: bucket `PROVISIONING -> READY` and bootstrap
credential `CREATING -> READY`; only then may the outbox become `SUCCEEDED` and
the ownership fast path open billing.

```mermaid
sequenceDiagram
    autonumber
    participant DP as Zone Dataplane
    participant KafkaRes as Kafka Result Topic
    participant JO as Job Orchestrator (Result Worker)
    participant PG as PostgreSQL (Controlplane)
    participant Redis as Shared Redis Stream (stream:{billing}:resource_ownership)
    participant CostConsumer as Cost Manager (resource_ownership_handler.go)
    participant Inbox as billing.ownership_event_inbox
    participant Head as billing.resource_ownership_head
    participant Proj as billing.resource_ownership_projection

    DP->>KafkaRes: Publish JobResult (job_id, status: SUCCEEDED)
    KafkaRes-->>JO: Consume JobResult
    JO->>PG: 1. Bucket READY + credential READY, then outbox SUCCEEDED
    
    rect rgb(255, 245, 240)
    Note over JO,Proj: Luồng chuyển giao sở hữu sang Billing (Fast-Path)
    JO->>Redis: 2. XADD ResourceOwnershipChangedV1 (RESOURCE_CREATED, owner_type: TENANT, version: 1)
    Redis-->>CostConsumer: 3. XReadGroup / XAutoClaim (Group: cost-resource-ownership-v1)
    CostConsumer->>Inbox: 4. INSERT INTO ownership_event_inbox (Idempotency Guard)
    CostConsumer->>CostConsumer: 5. SELECT pg_try_advisory_xact_lock(resource_id)
    CostConsumer->>Head: 6. Kiểm tra source_version = 1 (Initial Version Fence)
    CostConsumer->>Proj: 7. INSERT INTO resource_ownership_projection (effective_from = completed_at, effective_to = NULL)
    CostConsumer->>Head: 8. UPSERT head state = 'ACTIVE', version = 1
    CostConsumer->>Inbox: 9. UPDATE ownership_event_inbox SET status = 'APPLIED'
    CostConsumer->>Redis: 10. TxPipelined(XAck + XDel)
    end
```

### Hop-by-Hop Contract — Phase 4

#### Hop 4.1: Zone Dataplane → Kafka Result Topic
- **Input Contract (`JobResult` Protobuf)**:
  - `job_id`: UUID (`event_id` của outbox record)
  - `job_topic`: `"storage.bucket.create"`
  - `status`: `JOB_STATUS_SUCCEEDED` / `JOB_STATUS_FAILED`
  - `error_code`: string (khi thất bại)
  - `error_message`: string (khi thất bại)
- **Topic**: `aurora.central.job_results`

#### Hop 4.2: Kafka Result → Job Orchestrator Result Worker
- **Input**: Consumed `JobResult` Protobuf.
- **Authority & Idempotency Fence**:
  - `owner_type`: `"TENANT"`
  - `status`: `IN ('PENDING', 'PROCESSING')` (chống replay kết quả cũ)
- **State Transition / Durable Effects**:
  - Khi `SUCCEEDED`:
    - Resource status: Cập nhật `storage.tenant_buckets.status = 'READY'`.
    - Bootstrap credential: Cập nhật `storage.tenant_credentials.state = 'READY'`.
    - Outbox settlement: `storage.storage_outbox_records` chuyển trạng thái `status = 'SUCCEEDED'`, cập nhật `completed_at = NOW()`.
  - Khi `FAILED`:
    - Resource evidence: Cập nhật bucket `PROVISIONING -> FAILED` và bootstrap credential `CREATING -> ERROR`; không xóa dấu vết resource từ một failure result.
    - Outbox settlement: `storage.storage_outbox_records` chuyển trạng thái `status = 'FAILED'`, cập nhật `completed_at = NOW()`.
- **Output Schema (`SettledOutboxRecord`)**:
  - `resource_id`: UUID (`bucket_id`)
  - `resource_name`: string (`bucket_name`)
  - `owner_id`: UUID (`tenant_id`)
  - `owner_type`: `"TENANT"`
  - `zone_id`: UUID
  - `actor_user_id`: UUID
  - `trace_id`: UUID

#### Hop 4.3: Job Orchestrator → Shared Redis Stream
- **Method / Transport**: Lua Script Atomic Check `XLEN < stream_capacity` + `XADD` + `WAITAOF(1, replica_acks, timeout)`.
- **Stream**: `stream:{billing}:resource_ownership`
- **Protobuf**: `ResourceOwnershipChangedV1`
  - `event_id`: Deterministic UUIDv5 (`ownership_event_id`).
  - `event_type`: `"RESOURCE_CREATED"`
  - `resource_type`: `"STORAGE_BUCKET"`
  - `resource_id`: `bucket_id`
  - `resource_name`: `bucket_name`
  - `owner_id`: `tenant_id`
  - `owner_type`: `"TENANT"`
  - `zone_id`: `zone_id`
  - `source_version`: `1`
  - `effective_at`: RFC3339 timestamp (`completed_at`).
  - `source_job_id`: `job_id`
- **Durability Guarantee**: `WAITAOF` đảm bảo Redis đã ghi AOF và sync replica trước khi JO cập nhật `ownership_published_at = NOW()` trong PostgreSQL.
- **Failover / Recovery**: `OwnershipRelay` chạy ngầm định kỳ quét các dòng outbox `status = 'SUCCEEDED' AND ownership_published_at IS NULL` với `FOR UPDATE SKIP LOCKED` để phát bù nếu Redis gặp sự cố.

#### Hop 4.4: Cost Manager Consumer → Billing Database
- **Consumer**: `ResourceOwnershipConsumer` (`cost-resource-ownership-v1`) đọc tin qua `XReadGroup` và `XAutoClaim` (reclaim pending sau 30s).
- **Contract Boundary Validation**: Kiểm tra kích thước payload `<= 64KB`, schema_version == 1, UUID hợp lệ, owner_type `TENANT`, W3C traceparent.
- **Single SQL Transaction**:
  1. `INSERT INTO billing.ownership_event_inbox` bảo đảm Idempotency.
  2. `SELECT pg_try_advisory_xact_lock(hashtextextended(resource_id, 0))` chống race condition giữa các pod.
  3. Kiểm tra `source_version == 1` trên `billing.resource_ownership_head`.
  4. Mở cửa sổ cước: `INSERT INTO billing.resource_ownership_projection (id, resource_type, resource_id, resource_name, owner_id, owner_type, zone_id, ownership_version, effective_from, source_updated_at) VALUES ($1, 'STORAGE_BUCKET', $2, $3, $4, 'TENANT', $5, 1, $6, NOW())`.
  5. Cập nhật `billing.resource_ownership_head` sang trạng thái `ACTIVE`.
  6. Cập nhật inbox `status = 'APPLIED'`.
- **Post-Commit Clean**: Thực thi Redis Pipeline `XACK` + `XDEL` để giải phóng RAM.

---

## Phase 5 — Timeline Projection & Realtime Client Notification

Sau khi hoàn tất hạch toán sở hữu, Job Orchestrator phát sự kiện thông báo kết quả vào Redis Notification Stream để Notification Service lưu audit log và phát realtime qua Centrifugo WebSocket tới Cloud Console.

```mermaid
sequenceDiagram
    autonumber
    participant JO as Job Orchestrator (notify.rs)
    participant RedisNoti as Redis Stream (stream:{job_notifications})
    participant NotiSvc as Notification Service (job_notifications.rs)
    participant Scylla as ScyllaDB (Tenant Timeline)
    participant Centrifugo as Centrifugo WebSocket
    actor Browser as Cloud Console (Tenant)

    JO->>RedisNoti: 1. XADD JobNotificationEvent { job_id, user_id, status: SUCCEEDED, job_topic }
    RedisNoti-->>NotiSvc: 2. XReadGroup / Consume notification
    NotiSvc->>Scylla: 3. Insert ActivityEvent (TENANT_BUCKET_CREATED)
    NotiSvc->>Centrifugo: 4. Publish to channel "tenant:storage:{tenant_id}:{workspace_id}"
    Centrifugo-->>Browser: 5. WebSocket Push: { type: "BUCKET_CREATED", id, name }
    Browser->>Browser: 6. Invalidate query cache & update bucket list
```

### Hop-by-Hop Contract — Phase 5

#### Hop 5.1: Job Orchestrator → Redis Notification Stream
- **Input**: Struct `NotificationIntent`
- **Stream**: `stream:{job_notifications}`
- **Payload**: JSON / MsgPack `JobNotificationEvent`
  - `job_id`: UUID
  - `user_id`: UUID (`actor_user_id`)
  - `status`: `"SUCCEEDED"`
  - `job_topic`: `"storage.bucket.create"`
  - `resource_id`: `bucket_id`

#### Hop 5.2: Notification Service → Timeline & Centrifugo
- **Processing**:
  - Ghi bản ghi audit `ActivityEvent` vào ScyllaDB Timeline.
  - Gọi HTTP API của Centrifugo phát tin nhắn sang channel `tenant:storage:{tenant_id}:{workspace_id}`.

#### Hop 5.3: Centrifugo → Browser Client (UI Wakeup)
- **Transport**: WebSocket Push.
- **Client Processing**: TanStack Query client nhận tin, tự động vô hiệu hóa query `["tenant", "storage", "buckets", workspace_id]` và làm mới danh sách bucket trên giao diện trong 0ms.

---

## Phase 6 — Cost Engine Hourly Settlement & Rating Initiation (Background Accounting)

Sau khi Phase 4 mở cửa sổ sở hữu (`effective_from = completed_at, effective_to = NULL`), tenant bucket chính thức bước vào chu kỳ tính cước định kỳ hàng giờ (Hourly Rating Engine). Dataplane định kỳ quét MinIO, gửi báo cáo đo kiểm `StorageUsageReportV1` qua Kafka sang Shared Redis Stream `aurora:storage:usage:reports`. Cost Engine background consumer (`cost-engine-storage-metering-v1`) sẽ đối soát và trừ ví tiền của Tenant.

```mermaid
sequenceDiagram
    autonumber
    participant DP as Zone Dataplane Metering
    participant JO as Job Orchestrator (Storage Metering Relay)
    participant Redis as Shared Redis Stream (aurora:storage:usage:reports)
    participant Engine as Cost Engine (usage_report_settlement.rs)
    participant Pricing as Pricing Runtime & Zone Adjustments
    participant Proj as billing.resource_ownership_projection
    participant Wallet as billing.wallets & billing.usage_charges

    DP->>JO: StorageUsageReportV1 qua Kafka
    JO->>Redis: XADD aurora:storage:usage:reports
    Redis-->>Engine: XReadGroup (cost-engine-storage-metering-v1)
    
    rect rgb(240, 248, 255)
    Note over Engine,Wallet: Giao dịch hạch toán & trừ tiền (Hourly Settlement)
    Engine->>Engine: 1. Acquire Redis Lease Lock (storage:report:settlement:lock)
    Engine->>Proj: 2. Resolve Owner: Match resource_id trong cửa sổ [window_start, window_end)
    Proj-->>Engine: owner_id = tenant_id, owner_type = 'TENANT' (Active)
    Engine->>Pricing: 3. Lấy Base Price (capacity/network) + Multiplier Zone
    Engine->>Engine: 4. Tính toán: Total Charge = Usage * Unit Price * Multiplier
    Engine->>Wallet: 5. ACID Transaction: Ghi usage_charges, usage_charge_lines & Trừ số dư ví Tenant
    Engine->>Redis: 6. XACK + Release Lease
    end
```

### Hop-by-Hop Contract — Phase 6

#### Hop 6.1: Dataplane → Shared Redis Stream (via JO)
- **Transport**: Kafka `{prefix}.storage.usage.reports.v1` → JO Relay → Redis Stream `aurora:storage:usage:reports`.
- **Payload**: Protobuf `StorageUsageReportV1`
  - `report_id`: UUID
  - `zone_id`: UUID
  - `window_start`, `window_end`: RFC3339 timestamps (chu kỳ 1 giờ)
  - `aggregates`: Danh sách bản ghi đo lường (`storage.capacity.gb_hour`, `storage.network_in.byte`, `storage.network_out.byte`).

#### Hop 6.2: Cost Engine Ingestion & Ownership Resolution
- **Consumer**: `usage_report_settlement.rs` (`cost-engine-storage-metering-v1`).
- **Resolution SQL Fence**:
  ```sql
  SELECT owner_id, owner_type, resource_type, zone_id
  FROM billing.resource_ownership_projection
  WHERE resource_id = $1 AND resource_type = 'STORAGE_BUCKET'
    AND effective_from <= $3 AND (effective_to IS NULL OR effective_to > $2)
  LIMIT 1;
  ```
- **Invariant**: Do Phase 4 đã ghi `effective_from`, Cost Engine xác định chính xác `owner_id = tenant_id` và `owner_type = TENANT`.

#### Hop 6.3: Rating & Wallet Settlement Transaction
- **Đơn giá & Hệ số**:
  - `Base Price`: Lấy từ bảng giá gốc `billing.pricing_schedules` (theo đơn vị `GB_HOUR` hoặc `BYTE`).
  - `Zone Multiplier`: Lấy từ `billing.storage_zone_price_adjustment_versions` theo `zone_id`.
  - **Công thức**: $\text{Amount} = \text{Usage Quantity} \times \text{Base Price} \times \frac{\text{Numerator}}{\text{Denominator}}$.
- **PostgreSQL ACID Settlement**:
  1. `INSERT INTO billing.report_inbox` / `billing.line_inbox` (Idempotency).
  2. `INSERT INTO billing.usage_charges` & `billing.usage_charge_lines` (Bản ghi hóa đơn tài chính bất biến).
  3. `UPDATE billing.wallets SET balance = balance - amount, updated_at = NOW() WHERE owner_id = $1 AND owner_type = 'TENANT'`.

---

## Failure and security rules

| Condition | Failure Semantics | Recovery / Settlement |
|---|---|---|
| User is inactive in tenant | HTTP `403 Forbidden` | CTE Block 1 returns 0 rows; zero mutations occur. |
| Workspace does not match context zone | HTTP `403 Forbidden` / `404 Not Found` | CTE Block 1 returns 0 rows; prevents cross-zone split brain. |
| Physical bucket name collision | HTTP `409 Conflict` | Unique constraint violation trapped; rolled back. |
| Zone provisioning fails | Dataplane reports `FAILED` | JO preserves bucket/credential failure evidence; provisioning failure is not deletion authority. |
| Ownership Stream unavailable | Backpressure / Network Timeout | Outbox record retains `ownership_published_at = NULL`; `OwnershipRelay` recovers asynchronously. |

---

## Code map

### Phase 1 — Client → Envoy → ACR
- **ACR ExtAuthz Filter & Session Validation**: `acr/src/auth/`
- **ACR Storage Route & Tenant Rewrite**: `acr/src/storage/route.rs`, `acr/src/storage/control_assertion.rs`

### Phase 2 — Wallet Admission & Controlplane Mutation
- **Route Registration**: `controlplane/internal/storage/route.go`
- **HTTP Handler**: `controlplane/internal/storage/transport/http/handler/tenant_bucket_handler.go` (`Create`)
- **Domain Service**: `controlplane/internal/storage/service/tenant_bucket_service.go` (`CreateBucketForTenant`)
- **Commercial Admission Repository**: `controlplane/internal/storage/repository/commercial_admission_repo.go`
- **SQL Repository**: `controlplane/internal/storage/repository/tenant_bucket_repo.go` (`Create`)

### Phase 3 — Outbox CDC Dispatch & Dataplane Execution
- **JO Outbox Changefeed Dispatch**: `job-orchestrator/src/changefeed/dispatch.rs`
- **Dataplane Command Executor**: `dataplane/src/executor/storage/bucket.rs`

### Phase 4 — Job Settlement & Resource Ownership Handover (Billing Registry)
- **JO Result Worker (DB Settlement)**: `job-orchestrator/src/results/storage/bucket.rs`, `job-orchestrator/src/results/apply.rs`
- **JO Ownership Fast-Path & Relay**: `job-orchestrator/src/outbox/ownership.rs`, `job-orchestrator/src/outbox/redis.rs`
- **Cost Manager Ownership Consumer**: `cost-manager/api/internal/transport/redis/handler/resource_ownership_handler.go`
- **Cost Manager Ownership Service**: `cost-manager/api/internal/service/resource_ownership_service.go`
- **Cost Manager Ownership Repository**: `cost-manager/api/internal/repository/resource_ownership_repo.go`

### Phase 5 — Timeline Projection & Realtime Client Notification
- **JO Realtime Notifier**: `job-orchestrator/src/results/notify.rs`
- **Notification Service Consumer**: `notification-service/src/application/job_notifications.rs`
- **Centrifugo WebSocket Push**: `notification-service/src/infrastructure/centrifugo.rs`
- **Cloud Console UI**: `cloud-console/src/`

### Phase 6 — Cost Engine Hourly Settlement & Rating Initiation
- **Cost Engine Settlement Worker**: `cost-manager/engine/src/service/storage/usage_report_settlement.rs`
- **Cost Engine Pricing Runtime & Lease**: `cost-manager/engine/src/engine/` (`pricing_runtime.rs`, `settlement.rs`, `lease.rs`)
- **Billing PostgreSQL Tables**: `billing.resource_ownership_projection`, `billing.usage_charges`, `billing.usage_charge_lines`, `billing.wallets`
