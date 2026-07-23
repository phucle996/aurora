# Dataplane Runtime — Kafka Job Transport, Zone KV và Admission

Dataplane thực thi workload của đúng một Zone. Central Kafka là durable platform transport;
NATS JetStream KV riêng Zone giữ desired runtime projection/health/coordination; shared Cache Redis chỉ giữ
bounded runtime-watch soft state.

God View chính:

- [`kafka_platform_transport_god_view.md`](../god_view/platform/kafka_platform_transport_god_view.md)
- [`zone_metadata_sync_and_state_machine_god_view.md`](../god_view/hierarchy/zone_metadata_sync_and_state_machine_god_view.md)
- [`mail_configuration_projection_god_view.md`](../god_view/mail/mail_configuration_projection_god_view.md)

## 1. Boundary

| Thành phần | Vai trò |
|---|---|
| Central Kafka | Zone/platform command, result, metadata, report, storage snapshot |
| Shared Cache Redis | Runtime watch/report có TTL; không phải job queue |
| Zone NATS `AURORA_ZONE_CONFIG` | Zone metadata và mail immutable projection |
| Zone NATS `AURORA_ZONE_HEALTH` | Rebuildable current health |
| Zone NATS `AURORA_ZONE_COORDINATION` | CAS lease và fencing |
| Pod memory | Worker registry, admission counters, mail L1 và dynamic lag |

Dataplane không kết nối CP/Billing PostgreSQL, Central NATS hoặc Vault. `NATS_ZONE_URL` không được fallback
sang NATS trung tâm. Production Zone KV dùng file storage và replica `3/5`.

## 2. Job lifecycle

```mermaid
sequenceDiagram
    participant K as Kafka Zone/platform topic
    participant JC as JobConsumer
    participant KV as Zone Coordination KV
    participant Q as Bounded MPSC
    participant JR as JobRunner
    participant RK as Kafka results/retry

    JC->>KV: read zone.metadata
    alt missing/error/status != active
        JC->>JC: fail-closed pause
    else active
        JC->>K: manual poll
        JC->>JC: validate Protobuf + Zone binding
        JC->>KV: acquire lease.job.sha256(job_id)
        JC->>Q: enqueue payload + Kafka delivery + lease
        Q->>JR: execute workload
        JR->>RK: durable result or retry, acks=all
        JR->>K: settle contiguous terminal offset
        JR->>KV: owner/fence checked release
    end
```

- Poison/cross-Zone command được DLQ trước settle.
- Retry publish command `attempt+1` trước settle original.
- Assignment epoch tăng khi rebalance; completion cũ không commit owner mới.
- Lease giảm concurrent duplicate nhưng external executor vẫn phải idempotent.
- Watchdog renew lease bounded-concurrent; timeout publish terminal result rồi settle.

## 3. Admission và autoscaling

- Hysteresis mở circuit từ `80%`, đóng dưới `50%`.
- Pacing tăng theo active workers/CPU/RAM.
- Bounded MPSC truyền backpressure.
- Autoscaler dùng Kafka lag từ chính consumer group.
- Lag stale không được dùng làm tín hiệu scale/state tích cực.

## 4. Mail runtime

Mail configuration hydrate từ Zone KV; customer broker connection chỉ được mở tại Dataplane:

- source suites: Kafka, Redis Stream, NATS JetStream, RabbitMQ;
- connection envelope encrypted, chỉ đúng Zone adapter giải mã;
- one consumer binds one immutable template version;
- template lazy-load vào Moka L1 theo byte weight/TTL/singleflight;
- render escaped parameters rồi batch JMAP;
- customer broker settlement giữ native semantics;
- slot ownership dùng Zone KV lease/fencing.

Customer payload mặc định là `{to, parameter}` JSON. Internal verification topic dùng
`MailDispatchEnvelopeV1` Protobuf nhưng vẫn map thành cùng logical render request.

Dynamic consumer lag/state nằm trong app memory. Chỉ khi CP tạo watch lease, pod owner mới đẩy bounded snapshot
vào Cache Redis; không lưu dynamic runtime trong Kafka, PostgreSQL hoặc Zone KV.

## 5. Recovery

| Failure | Behavior |
|---|---|
| Kafka poll/publish outage | Không settle source; replay sau recovery |
| Kafka poison data | Durable DLQ rồi settle |
| Zone KV unavailable | Ingestion fail-closed |
| Pod chết in-flight | Kafka replay sau offset + lease expiry |
| Rebalance | Epoch fence chặn stale completion |
| Result chưa durable | Không commit command |
| Cache Redis unavailable | Runtime watch/reconciler coordination suy giảm; Kafka job không mất |
| Metadata missing | Durable query/snapshot repair qua Kafka |

## 6. Code map

- `src/infra/kafka.rs`: producer, consumer, rebalance fence, contiguous settlement.
- `src/infra/zone_kv.rs`: buckets, CAS metadata và fenced lease.
- `src/job_lifecycle/consumer.rs`: command validation, DLQ, lease, dispatch.
- `src/job_lifecycle/runner.rs`: execution, retry/result durability.
- `src/workerpool/watchdog.rs`: timeout/lease renewal.
- `src/zone_gateway/`: metadata snapshot/query/report.
- `src/executor/mail/runtime/`: customer broker suites.
- `src/executor/mail/processor/`: render/JMAP/batching.
- `src/executor/mail/supervisor/`: consumer runtime reporting and health observation.
