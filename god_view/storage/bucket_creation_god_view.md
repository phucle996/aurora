# Storage Bucket Creation — God View

> [!IMPORTANT]
> Đây là Source of Truth cho create bucket asynchronous lifecycle. PostgreSQL/outbox là business boundary,
> Central Kafka là durable job/result transport, Dataplane đúng Zone mới gọi MinIO.
> Ownership/Billing event nằm tại
> [`resource_ownership_god_view.md`](../billing/resource_ownership_god_view.md).

## 0. Contract

| Thuộc tính | Contract |
|---|---|
| Public mutation | Personal và Tenant route/service/repository riêng |
| Business SoT | CP PostgreSQL storage aggregate + `storage_outbox_records` |
| Command | `JobCommandV1` trên `aurora.jobs.commands.zone.<zone_id>.v1` |
| Result | `JobExecutionResultProto` trên `aurora.jobs.results.v1` |
| Executor | Dataplane Storage trong đúng Zone |
| Delivery | At-least-once; manual contiguous Kafka commit |
| UI completion | Shared L2 Redis Stream → Notification → Centrifugo |
| Billing ownership | Durable lifecycle event sau create/delete terminal success |

## 1. Create transaction

```mermaid
sequenceDiagram
    participant UI as Cloud Console
    participant Edge as Envoy + ACR
    participant CP as Storage API
    participant DB as PostgreSQL
    participant JO as JO CDC
    participant K as Kafka Zone command
    participant DP as Dataplane
    participant S3 as MinIO

    UI->>Edge: create bucket
    Edge->>CP: verified identity/routing scope
    CP->>DB: BEGIN
    CP->>DB: authorize workspace + reserve bucket identity
    CP->>DB: INSERT storage_outbox_records
    CP->>DB: COMMIT
    DB-->>JO: logical WAL
    JO->>K: JobCommandV1, key=job_id, acks=all
    JO->>DB: advance LSN after Kafka ACK
    K-->>DP: manual poll exact Zone topic
    DP->>DP: validate schema/Zone + acquire fenced lease
    DP->>S3: idempotent create
```

Invariant:

- Client không tự quyết định `owner_id`, `owner_type`, workspace hoặc Zone.
- CP cross-check identity + workspace ownership trước transaction.
- Outbox `zone_id UUID` là trusted routing snapshot.
- Aggregate mutation/reservation và outbox insert cùng commit.
- Không publish Kafka trước commit.
- Bucket identifier/resource ID ổn định qua retry.

## 2. Command envelope

`JobCommandV1` bắt buộc có:

- 16-byte `job_id`;
- job/version/attempt;
- exact `job_topic`;
- `source_domain=STORAGE`;
- stable `resource_id`;
- versioned Protobuf business payload;
- trace ID;
- `target_zone_id`;
- `transport_schema_version=1`.

JO chọn topic từ trusted outbox Zone. Dataplane vẫn so sánh topic Zone với `target_zone_id` và `ZONE_ID`.
Cross-wire/poison command đi DLQ rồi mới settle.

## 3. Execution, retry và result

```mermaid
sequenceDiagram
    participant K as Kafka command
    participant DP as Dataplane
    participant KV as Zone Coordination KV
    participant S3 as MinIO
    participant KR as Kafka results
    participant JO as JO result consumer
    participant DB as PostgreSQL
    participant R as Shared L2 Redis
    participant NS as Notification Service
    participant UI as Centrifugo/UI

    K-->>DP: JobCommandV1
    DP->>KV: acquire lease.job.sha256(job_id)
    DP->>S3: create bucket with stable operation identity
    alt transient failure and attempts remain
        DP->>K: republish attempt+1, acks=all
        DP->>K: settle original offset
    else terminal
        DP->>KR: terminal result, acks=all
        DP->>K: settle original contiguous offset
        KR-->>JO: manual poll
        JO->>DB: guarded terminal transaction
        JO->>R: customer-safe completion
        JO->>KR: commit result offset
        R-->>NS: consumer-group delivery
        NS-->>UI: operation result
    end
```

Result consumer không commit offset cao hơn nếu record thấp hơn gặp transient DB/Shared Redis failure. Poison result
đi DLQ trước commit. Duplicate terminal result là no-op theo job/topic/attempt/terminal guards.

## 4. Create/delete resource-first semantics

- Create: CP row/outbox tồn tại trước; DP create infrastructure; terminal FAILED xóa candidate/resource row theo
  exact resource ID nếu workflow quy định create chưa thành công.
- Update: versioning/COW chỉ dùng khi resource có mutable projected configuration.
- Delete: CP không hard-delete business row trước. DP xóa physical bucket trước; JO hard-delete CP record chỉ sau
  terminal success.
- Delete FAILED giữ record để người dùng/SRE retry.
- Không dùng `deleted_at` giả cho physical connection lifecycle nếu workflow đã khóa hard-delete.

## 5. Ownership/Billing event

Sau create/delete `SUCCEEDED`, JO transaction:

1. lock exact result/outbox;
2. resolve owner/resource/Zone từ authoritative `storage_outbox_records`;
3. update operation terminal state;
4. commit; chính storage outbox row với `ownership_published_at=NULL` là recovery marker;
5. fast-path `XADD + WAITAOF` vào Shared Redis ownership stream;
6. mark `ownership_published_at`, hoặc để recovery worker retry nếu Redis lỗi;
7. Cost Manager consumer group ghi Billing inbox/projection rồi `XACK + XDEL`.

Không query Controlplane DB trong charging path. Event chỉ được publish sau transaction commit.

## 6. Failure/race matrix

| Case | Guard |
|---|---|
| CP commit, pod chết | WAL/outbox replay |
| JO Kafka ACK, chết trước LSN | Duplicate command, stable job ID |
| Two DP replicas | Kafka group + Zone lease/fencing |
| Lease contention | Republish durable trước settle original |
| DP side effect xong, chết trước result | Kafka replay + idempotent executor |
| Result DB transaction fail | Result offset không commit |
| Notification fail | Best-effort drop + metric; UI query authoritative API |
| Ownership Redis fail | Existing storage outbox row giữ pending; bounded recovery retry |
| Cross-Zone command | topic + target Zone + ACL → DLQ |
| Delete/create same identity race | DB lock/version/resource ID guard |

## 7. Security

- Internal headers từ public client bị strip/reinject ở edge; backend không tin client-supplied owner/Zone.
- Kafka production dùng TLS/mTLS hoặc SASL over TLS và ACL theo exact Zone topic/group.
- Dataplane Zone A không có credential Zone B.
- S3 credential chỉ ở Dataplane Zone secret scope; không nằm trong Kafka payload/log.
- Bucket name/resource ID validate trước outbox và trước executor.

## 8. Code map

- `controlplane/internal/storage/`: handler/service/repository/outbox.
- `job-orchestrator/src/changefeed/worker.rs`: WAL → Kafka command.
- `dataplane/src/job_runtime/intake.rs`: command intake và Zone validation.
- `dataplane/src/executor/storage/bucket.rs`: physical executor.
- `dataplane/src/job_runtime/execution.rs` + `completion.rs`: lease, retry/result/settlement.
- `job-orchestrator/src/results/worker.rs`: result manual consumer.
- `job-orchestrator/src/reverse_provider/storage/`: terminal transaction/lifecycle relay.
