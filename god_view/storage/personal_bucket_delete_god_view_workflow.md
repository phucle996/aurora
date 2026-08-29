# Personal Bucket Delete — God View

Bucket deletion requests irreversible Zone work. Controlplane retains the bucket
and credentials until Dataplane reports physical bucket deletion and authority
cleanup success.

## API-scope contract

Browser calls `DELETE /api/v1/critical/storage/buckets/{bucket_id}` with no body.
ACR chooses personal only for a verified platform session, rewrites to the
internal `/api/v1/personal/critical/storage/buckets/{bucket_id}` route, consumes
the exact session proof, and injects trusted identity/workspace/Zone context.
`Authorize` requires `storage:bucket:delete` at current workspace or wildcard.
Controlplane runs `RequireSessionProof` before `Authorize`. The browser selects
only the UUID; repository resolves the physical name from the durable row and
rechecks exact user/workspace/Zone scope before transitioning it.

## REST input and output

### Request headers used

| Header | Use |
|---|---|
| `Cookie` | ACR resolves Trinity user, platform branch, Zone and workspace. |
| `Origin` | CORS. |
| `X-Requested-With` or `Sec-Fetch-Site` | CSRF requirement for `DELETE`. |
| `x-session-proof-challenge-id`, `x-session-proof-timestamp`, `x-session-proof-signature` | Bound to exact `DELETE`, query-free critical path and empty-body hash; ACR consumes/removes raw proof and overwrites the verified marker/challenge upstream. |
| `traceparent` | Copied to outbox if valid. |

### Path payload

| Field | Contract |
|---|---|
| `bucket_id` | UUID. Handler rejects invalid value with `400`. |

### Response headers

| Result | Headers |
|---|---|
| All JSON responses | `Content-Type: application/json` |

### Response payload

| Status | Payload |
|---|---|
| `200` | `data: null` and message `bucket deletion initiated` after durable outbox insert |
| `400` | Invalid UUID |
| `403` | ACR, context or permission failure |
| `404` | Bucket id absent or not owned by user |
| `500` | Payload protection/database failure |

## Key and transport contract

| Store / transport | Operation | Invariant |
|---|---|---|
| Auth-State Redis session | ACR verification | Browser identity and workspace header are never upstream authority. |
| `storage.personal_credentials` | `READY -> DELETING` | Child resources remain until Zone success; failure restores them to `READY`. |
| `storage.personal_buckets` | `READY -> DELETING`, then hard-delete | Must remain until successful physical deletion; DB rejects hard-delete from another state. |
| `storage.storage_outbox_records` | Insert `storage.bucket.delete` | Holds resource id, durable physical `resource_name`, Zone, owner and encrypted payload; no browser-supplied name enters the command. |
| `storage.resource_admission_projection` | Hard-delete after successful Zone result | Central admission topology is retained while deletion can still retry. |
| `AURORA_ZONE_CONFIG/storage.bucket.head.{bucket_id}` | CAS tombstone | Disables runtime reads while preserving exact identity and deletion event. |
| `AURORA_ZONE_ADMISSION/{bucket_id}` | Revision-fenced delete | Removed only if stored ID/name identity matches the durable target. |
| `AURORA_ZONE_ADMISSION/name/{physical_name}` | Revision-fenced delete | Releases the SDK name index for safe later name reuse. |
| Zone command/result topics | Kafka | JO and Dataplane execute at least once. |
| Ownership stream | Shared Redis stream | Derived `RESOURCE_DELETED` is published after terminal success from durable outbox metadata. |

## Phase 1 — Client → Envoy → ACR

Envoy sends exact method/path, headers and empty body to ACR. ACR checks CORS,
rate limits, session, session proof, CSRF, Zone and tenant. It strips caller
identity/workspace headers, overwrites trusted context headers and rewrites the
neutral critical path to Personal. There is no name query or request payload.

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Central Envoy
    participant A as ACR ExtAuthz
    participant AR as Auth-State Redis

    B->>E: DELETE /api/v1/critical/storage/buckets/bucket_id with proof
    E->>A: CheckRequest exact method path headers and empty body
    A->>AR: Verify Trinity session and consume bound proof
    A->>A: CORS rate limits CSRF Zone tenant workspace
    alt edge verification fails
        A-->>E: Local 401, 403 or 429
        E-->>B: No upstream call
    else verified platform context
        A-->>E: Overwrite headers and critical Personal rewrite
        E->>E: Forward no-query request to Controlplane
    end
```

## Phase 2 — Controlplane deletion command transaction

After proof and permission middleware, handler parses only the bucket UUID.
Service calls the delete-specific repository projection with
`(bucket_id, user_id, workspace_id, zone_id)` and receives durable
`(id, name, workspace_id, zone_id)`. It reads access keys through the owner
fence and builds `BucketDeleteSync` exclusively from those durable facts.

Repository HPKE-seals the command, then its mutation CTE locks the exact `READY`
bucket and rechecks user ownership, workspace and both bucket/workspace Zone
facts. It rejects the command if any child is not `READY`, transitions every
credential and the bucket to `DELETING`, and inserts the outbox row with the
same durable name. It does not delete business or admission rows here.

```mermaid
sequenceDiagram
    participant M as ContextInjector and Authorize
    participant H as PersonalBucketHandler
    participant S as PersonalBucketService
    participant R as PersonalBucketRepository
    participant V as Vault payload protector
    participant PG as PostgreSQL

    M->>M: Require storage bucket delete grant
    M->>H: Trusted user workspace Zone context
    H->>H: Parse bucket id only
    H->>S: DeleteBucket with trusted user workspace Zone
    S->>R: GetDeleteTarget with four-dimensional scope
    R->>PG: SELECT durable id name workspace zone through owner fence
    S->>R: List access keys by owned bucket id
    R->>PG: SELECT credentials through owner fence
    S->>R: Insert delete outbox
    R->>V: Seal BucketDeleteSync with durable target
    R->>PG: Recheck exact scope, bucket/credentials -> DELETING, insert outbox
    alt ownership row exists
        PG-->>H: Command durable
        H-->>M: 200 deletion initiated
    else no owned bucket
        PG-->>H: ErrNotFound
        H-->>M: 404
    end
```

## Phase 3 — Outbox CDC Dispatch & Dataplane Execution

Dataplane deletes the durable named physical bucket first, deletes each MinIO
user and policy, disables/tombstones `storage.bucket.head.{bucket_id}`, then
revision-deletes both admission indexes. A MinIO or KV cleanup failure keeps the
command retryable; MinIO `NoSuchBucket` and already-absent exact KV keys are
idempotent replay results.

```mermaid
sequenceDiagram
    autonumber
    participant PG as PostgreSQL (Outbox)
    participant JO as Job Orchestrator CDC
    participant KC as Zone command Kafka
    participant DP as Zone Dataplane (BucketDeleteExecutor)
    participant M as MinIO Cluster
    participant KV as Zone config KV

    PG-->>JO: Read committed outbox record (topic: storage.bucket.delete)
    JO->>KC: Publish JobCommandV1 (sealed BucketDeleteSync, target zone)
    KC-->>DP: Consume job command
    DP->>DP: Decrypt payload & decode BucketDeleteSync
    DP->>M: S3 DeleteBucket (non-empty/error remains retryable)
    DP->>M: DeleteServiceAccount & DeletePolicy
    DP->>KV: CAS disabled runtime tombstone
    DP->>KV: CAS-delete admission name/physical_name then bucket_id
    M-->>DP: Physical deletion finished
```

The runtime-head CAS preserves
`AURORA_ZONE_CONFIG/storage.bucket.head.{bucket_id}` fields
`schema_version`, module/type, resource/name, Personal owner, workspace and
Zone; sets `runtime_read_enabled=false`, `tombstoned=true`, replaces
`event_id` with the delete job and increments `version`.

Admission cleanup reads and decodes each key, requires exact durable
`resource_id/resource_name` equality and uses revision-conditional delete.
Identity mismatch or corrupt JSON fails closed; CAS contention is retryable.
Deleting the name index before the ID index prevents a successful command from
leaving an SDK lookup bound to a deleted resource.

Before publishing the terminal job result, Dataplane CAS-creates
`AURORA_ZONE_JOB_COMPLETION/job.completion.{job_id}.{delivery_epoch}` as protobuf
`JobCompletionReceiptV1` with `schema_version=2`, `command_sha256`, `attempt`,
`message`, `result_payload`, `result_payload_schema_version`, `result_status`
and optional `error_code`. This is replay evidence only; it carries no bucket,
owner or deletion authority. Matching command bytes reuse the result, while a
conflicting or corrupt receipt fails closed.

---

## Phase 4 — Job Settlement & Resource Ownership Handover (Billing Registry)

Ngay sau khi nhận kết quả từ Dataplane, Job Orchestrator Result Worker thực thi CTE xóa personal bucket, credentials và `storage.resource_admission_projection` của exact `(resource_id, zone_id)`, chuyển outbox sang `SUCCEEDED`, và **ngay lập tức kích hoạt luồng Fast-Path đẩy sự kiện sở hữu (`RESOURCE_DELETED`) sang Cost Manager** để đóng sổ tính cước. Central admission row chỉ bị xóa sau khi Zone đã cleanup thành công.

```mermaid
sequenceDiagram
    autonumber
    participant DP as Zone Dataplane
    participant KR as Result Kafka Topic
    participant JO as Job Orchestrator (Result Worker)
    participant PG as PostgreSQL (Controlplane)
    participant Redis as Shared Redis Stream (stream:{billing}:resource_ownership)
    participant CostConsumer as Cost Manager (resource_ownership_handler.go)
    participant Inbox as billing.ownership_event_inbox
    participant Head as billing.resource_ownership_head
    participant Proj as billing.resource_ownership_projection

    DP->>KR: Publish JobResult (job_id, status: SUCCEEDED)
    KR-->>JO: Consume JobResult
    JO->>PG: 1. Delete bucket/credentials/resource admission and settle SUCCEEDED

    rect rgb(255, 245, 240)
    Note over JO,Proj: Luồng chuyển giao sở hữu sang Billing (Fast-Path)
    JO->>Redis: 2. XADD ResourceOwnershipChangedV1 (RESOURCE_DELETED, owner_type: PERSONAL, version: 2)
    Redis-->>CostConsumer: 3. XReadGroup / XAutoClaim (Group: cost-resource-ownership-v1)
    CostConsumer->>Inbox: 4. INSERT INTO ownership_event_inbox (Idempotency Guard)
    CostConsumer->>CostConsumer: 5. SELECT pg_try_advisory_xact_lock(resource_id)
    CostConsumer->>Head: 6. Kiểm tra source_version = lastVersion + 1 (Gap Fence)
    CostConsumer->>Proj: 7. UPDATE projection SET effective_to = completed_at WHERE resource_id = $1 AND effective_to IS NULL
    CostConsumer->>Head: 8. UPSERT head state = 'DELETED', version = 2
    CostConsumer->>Inbox: 9. UPDATE ownership_event_inbox SET status = 'APPLIED'
    CostConsumer->>Redis: 10. TxPipelined(XAck + XDel)
    end
```

### Hop-by-Hop Contract — Phase 4

#### Hop 4.1: Zone Dataplane → Kafka Result Topic
- **Input Contract (`JobResult` Protobuf)**:
  - `job_id`: UUID (`event_id` của outbox record)
  - `job_topic`: `"storage.bucket.delete"`
  - `status`: `JOB_STATUS_SUCCEEDED` / `JOB_STATUS_FAILED`
- **Topic**: `aurora.central.job_results`

#### Hop 4.2: Kafka Result → Job Orchestrator Result Worker
- **Input**: Consumed `JobResult` Protobuf.
- **Authority & Idempotency Fence**:
  - `owner_type`: `"PERSONAL"`
  - `status`: `IN ('PENDING', 'PROCESSING')` (chống replay kết quả cũ)
  - `zone_id`: `<> 00000000-0000-0000-0000-000000000000`
- **State Transition / Durable Effects**:
  - Khi `SUCCEEDED`:
    - Physical deletion: Hard-delete only a `DELETING` bucket; the DB trigger enforces this and cascade removes already-`DELETING` credentials.
    - Outbox settlement: `storage.storage_outbox_records` chuyển trạng thái `status = 'SUCCEEDED'`, cập nhật `completed_at = NOW()`.
  - Khi `FAILED`:
    - Khôi phục resource: Bucket `DELETING -> READY` và child credentials `DELETING -> READY` trước khi settle failure.
    - Outbox settlement: `storage.storage_outbox_records` chuyển trạng thái `status = 'FAILED'`, cập nhật `completed_at = NOW()`.
- **Output Schema (`SettledOutboxRecord`)**:
  - `resource_id`: UUID (`bucket_id`)
  - `resource_name`: string (`bucket_name`)
  - `owner_id`: UUID (`user_id`)
  - `owner_type`: `"PERSONAL"`
  - `zone_id`: UUID
  - `actor_user_id`: UUID
  - `trace_id`: UUID

#### Hop 4.3: Job Orchestrator → Shared Redis Stream
- **Method / Transport**: Lua Script Atomic Check `XLEN < stream_capacity` + `XADD` + `WAITAOF(1, replica_acks, timeout)`.
- **Stream**: `stream:{billing}:resource_ownership`
- **Protobuf**: `ResourceOwnershipChangedV1`
  - `event_id`: Deterministic UUIDv5 (`ownership_event_id`).
  - `event_type`: `"RESOURCE_DELETED"`
  - `resource_type`: `"STORAGE_BUCKET"`
  - `resource_id`: `bucket_id`
  - `resource_name`: `bucket_name`
  - `owner_id`: `user_id`
  - `owner_type`: `"PERSONAL"`
  - `zone_id`: `zone_id`
  - `source_version`: `2`
  - `effective_at`: RFC3339 timestamp (`completed_at`).
  - `source_job_id`: `job_id`
- **Durability Guarantee**: `WAITAOF` đảm bảo Redis đã ghi AOF và sync replica trước khi JO cập nhật `ownership_published_at = NOW()` trong PostgreSQL.
- **Failover / Recovery**: `OwnershipRelay` chạy ngầm định kỳ quét các dòng outbox `status = 'SUCCEEDED' AND ownership_published_at IS NULL` với `FOR UPDATE SKIP LOCKED` để phát bù nếu Redis gặp sự cố.

#### Hop 4.4: Cost Manager Consumer → Billing Database
- **Consumer**: `ResourceOwnershipConsumer` (`cost-resource-ownership-v1`) đọc tin qua `XReadGroup` và `XAutoClaim` (reclaim pending sau 30s).
- **Contract Boundary Validation**: Kiểm tra kích thước payload `<= 64KB`, schema_version == 1, UUID hợp lệ, owner_type `PERSONAL`, W3C traceparent.
- **Single SQL Transaction**:
  1. `INSERT INTO billing.ownership_event_inbox` bảo đảm Idempotency.
  2. `SELECT pg_try_advisory_xact_lock(hashtextextended(resource_id, 0))` chống race condition giữa các pod.
  3. Kiểm tra `source_version == last_source_version + 1` trên `billing.resource_ownership_head`.
  4. Đóng cửa sổ cước: `UPDATE billing.resource_ownership_projection SET effective_to = $1 WHERE resource_id = $2 AND effective_to IS NULL`.
  5. Cập nhật `billing.resource_ownership_head` sang trạng thái `DELETED`.
  6. Cập nhật inbox `status = 'APPLIED'`.
- **Post-Commit Clean**: Thực thi Redis Pipeline `XACK` + `XDEL` để giải phóng RAM.

---

## Phase 5 — Timeline Projection & Realtime Client Notification

Sau khi hoàn tất chuyển giao sở hữu, Job Orchestrator đẩy sự kiện thông báo vào Redis Notification Stream để Notification Service lưu ScyllaDB và phát realtime qua Centrifugo WebSocket tới người dùng cá nhân.

```mermaid
sequenceDiagram
    autonumber
    participant JO as Job Orchestrator (notify.rs)
    participant RedisNoti as Redis Stream (stream:{job_notifications})
    participant NotiSvc as Notification Service (job_notifications.rs)
    participant Scylla as ScyllaDB (Activity Timeline)
    participant Centrifugo as Centrifugo WebSocket
    actor Browser as Cloud Console (Personal)

    JO->>RedisNoti: 1. XADD JobNotificationEvent { job_id, user_id, status: SUCCEEDED, job_topic }
    RedisNoti-->>NotiSvc: 2. XReadGroup / Consume notification
    NotiSvc->>Scylla: 3. Insert ActivityEvent (PERSONAL_BUCKET_DELETED)
    NotiSvc->>Centrifugo: 4. Publish to channel "user:notification:{user_id}"
    Centrifugo-->>Browser: 5. WebSocket Push: { type: "BUCKET_DELETED", id }
    Browser->>Browser: 6. Invalidate query cache & remove bucket row
```

### Hop-by-Hop Contract — Phase 5

#### Hop 5.1: Job Orchestrator → Redis Notification Stream
- **Input**: Struct `NotificationIntent`
- **Stream**: `stream:{job_notifications}`
- **Payload**: JSON / MsgPack `JobNotificationEvent`
  - `job_id`: UUID
  - `user_id`: UUID (`actor_user_id`)
  - `status`: `"SUCCEEDED"`
  - `job_topic`: `"storage.bucket.delete"`
  - `resource_id`: `bucket_id`

#### Hop 5.2: Notification Service → ScyllaDB & Centrifugo
- **Processing**:
  - Ghi bản ghi audit `ActivityEvent` vào ScyllaDB Timeline.
  - Gọi HTTP API của Centrifugo phát tin nhắn sang kênh cá nhân `user:notification:{user_id}`.

#### Hop 5.3: Centrifugo → Browser Client (UI Wakeup)
- **Transport**: WebSocket Push.
- **Client Processing**: Hook `useStorageAccessSession` / TanStack Query client nhận tin, tự động làm mới danh sách và xóa row bucket đã xóa trên giao diện trong 0ms.

---

## Phase 6 — Cost Engine Final Rating & Settlement Termination (Rating Window Closed)

Sau khi Phase 4 cập nhật `billing.resource_ownership_projection` với `effective_to = completed_at`, vòng đời tính cước của bucket cá nhân chính thức chấm dứt.

```mermaid
sequenceDiagram
    autonumber
    participant DP as Zone Dataplane Metering
    participant Redis as Shared Redis Stream (aurora:storage:usage:reports)
    participant Engine as Cost Engine (usage_report_settlement.rs)
    participant Proj as billing.resource_ownership_projection
    participant Wallet as billing.wallets (Zero Mutation)

    DP->>Redis: StorageUsageReportV1 (chu kỳ sau khi xóa: window_start >= effective_to)
    Redis-->>Engine: XReadGroup (cost-engine-storage-metering-v1)

    rect rgb(255, 245, 240)
    Note over Engine,Wallet: Chặn trừ tiền cước sau thời điểm xóa (Termination Guard)
    Engine->>Proj: 1. Resolve Owner: Match resource_id trong cửa sổ [window_start, window_end)
    Proj-->>Engine: 0 Rows Matched (effective_to <= window_start)
    Engine->>Engine: 2. Đánh dấu bản ghi UNOWNED / DELETED
    Engine-->>Wallet: 3. KHÔNG phát sinh usage_charges và KHÔNG trừ tiền ví
    Engine->>Redis: 4. XACK hoàn tất
    end
```

### Hop-by-Hop Contract — Phase 6

#### Hop 6.1: Prorated Final Window Settlement (Kỳ cước cuối cùng)
- Đối với chu kỳ giờ chứa mốc `completed_at` (`window_start < effective_to < window_end`):
  - Cost Engine đối soát tìm thấy bản ghi ownership có `effective_to`.
  - Cước dung lượng `storage.capacity.gb_hour` được tính theo tỉ lệ thời gian thực tế bucket tồn tại trước khi bị xóa (`effective_to - window_start`).
  - Hóa đơn cước cuối cùng được chốt và trừ vào ví cá nhân.

#### Hop 6.2: Post-Deletion Fencing & Zero Zombie Billing (Chặn cước sau khi xóa)
- Đối với tất cả các chu kỳ giờ sau thời điểm xóa (`window_start >= effective_to`):
  - **Resolution Guard**:
    ```sql
    SELECT owner_id, owner_type, resource_type, zone_id
    FROM billing.resource_ownership_projection
    WHERE resource_id = $1 AND resource_type = 'STORAGE_BUCKET'
      AND effective_from <= $3 AND (effective_to IS NULL OR effective_to > $2)
    LIMIT 1;
    ```
  - Truy vấn trả về `0 rows` vì `effective_to` đã đóng trước đó.
  - **Durable Guarantee**: Cost Engine ghi nhận trạng thái `UNOWNED` và bỏ qua, **tuyệt đối không trừ tiền ví của người dùng** cho bất kỳ metric dư thừa nào từ Dataplane.

---

## Security discrepancy and failure semantics

| Condition | Actual behavior |
|---|---|
| `name` does not belong to `bucket_id` | Owner CTE validates only id/user. Client name reaches Dataplane and can target another physical bucket. This is a critical command-binding gap. Do not treat endpoint as safe for production deletion until repository derives command name from its locked row. |
| Current selected Zone differs from target bucket Zone | Handler places current ACR Zone in outbox, while repository validates only bucket id/user. It does not bind the locked bucket Zone to command Zone, so a cross-Zone delete command can be routed to wrong Zone. |
| Bucket not empty | MinIO delete fails. JO retains Central bucket/credentials and marks operation failed. |
| Bucket delete succeeds but user/policy cleanup fails | Dataplane still returns success, so Central rows are deleted while MinIO residual users/policies may remain. |
| Result replay | Outbox status guard makes terminal settlement no-op. |
| Bucket or any credential is not `READY` | Normal user delete is rejected without mutation; a separate reconciliation workflow must resolve the incomplete resource. |
| Ownership stream unavailable | Completed outbox keeps `ownership_published_at` pending for recovery relay. It does not undo physical deletion. |

---

## Code map

### Phase 1 — Client → Envoy → ACR
- **ACR ExtAuthz Filter & Session Validation**: `acr/src/auth/`
- **ACR Storage Route & Rewrite**: `acr/src/storage/route.rs`, `acr/src/storage/control_assertion.rs`

### Phase 2 — Wallet Admission & Controlplane Mutation
- **Route Registration**: `controlplane/internal/storage/route.go`
- **HTTP Handler**: `controlplane/internal/storage/transport/http/handler/personal_bucket_handler.go` (`Delete`)
- **Domain Service**: `controlplane/internal/storage/service/personal_bucket_service.go` (`DeleteBucket`)
- **SQL Repository**: `controlplane/internal/storage/repository/personal_bucket_repo.go` (`Delete`)

### Phase 3 — Outbox CDC Dispatch & Dataplane Execution
- **JO Outbox Changefeed Dispatch**: `job-orchestrator/src/changefeed/dispatch.rs`
- **Dataplane Command Executor**: `dataplane/src/executor/storage/delete.rs`

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

### Phase 6 — Cost Engine Final Rating & Settlement Termination
- **Cost Engine Settlement Worker**: `cost-manager/engine/src/service/storage/usage_report_settlement.rs`
- **Cost Engine Pricing Runtime & Lease**: `cost-manager/engine/src/engine/` (`pricing_runtime.rs`, `settlement.rs`, `lease.rs`)
- **Billing PostgreSQL Tables**: `billing.resource_ownership_projection`, `billing.usage_charges`, `billing.usage_charge_lines`, `billing.wallets`

## Resource completion revision — 2026-08-27

Bucket deletion succeeds only after the bucket and every listed owned credential
and policy have been removed. S3 NoSuchBucket is idempotent; cleanup errors are not
ignored. An unknown infrastructure outcome retries the same command/attempt,
instead of exhausting into FAILED and falsely restoring READY after partial deletion.
The generic success receipt prevents repeating a completed deletion on replay.
