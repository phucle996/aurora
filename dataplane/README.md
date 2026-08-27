# Dataplane Runtime — Kafka, Zone KV và Admission

Dataplane thực thi workload của đúng một Zone. Kafka là durable Central↔Zone
transport; NATS JetStream KV riêng Zone giữ desired runtime projection, health
và coordination. Dataplane không có credential Redis trung tâm và không mở
reverse runtime stream về Central.

Tài liệu kiến trúc chính:

- [Dataplane architecture](ARCHITECTURE.md)
- Node runtime sampling và resource-aware autoscaling được mô tả trong
  [Dataplane architecture](ARCHITECTURE.md#node-sampling-and-worker-scaling).
- [Telemetry architecture](TELEMETRY.md)
- [`CENTRAL_ZONE_TRANSPORT.md`](../architecture/CENTRAL_ZONE_TRANSPORT.md)
- [`zone_metadata_propagation_god_view_workflow.md`](../god_view/zone_runtime/zone_metadata_propagation_god_view_workflow.md)
- [`personal_mail_consumer_runtime_read_god_view_workflow.md`](../god_view/mail/personal_mail_consumer_runtime_read_god_view_workflow.md)
- [`tenant_mail_consumer_runtime_read_god_view_workflow.md`](../god_view/mail/tenant_mail_consumer_runtime_read_god_view_workflow.md)

## 1. Boundary

| Thành phần | Vai trò |
|---|---|
| Kafka transport | Per-Zone command, result, metadata, report, storage snapshot |
| Zone NATS `AURORA_ZONE_CONFIG` | Zone metadata và mail immutable projection |
| Zone NATS `AURORA_ZONE_JOB_COMPLETION` | Immutable terminal receipts; 512 MiB discard-new quota, không TTL |
| Zone NATS `AURORA_ZONE_HEALTH` | Rebuildable current health |
| Zone NATS `AURORA_ZONE_COORDINATION` | CAS lease và fencing |
| Zone NATS `AURORA_ZONE_RUNTIME_REPLAY` | Dataplane bootstrap provision file KV history 1, TTL 30 giây; Zone Public Authorizer CAS `jti` |
| Pod memory | Worker registry, admission counters, mail L1 và dynamic lag |

Dataplane không kết nối CP/Billing PostgreSQL, Shared/Auth Redis hoặc Vault.
Completion KV yêu cầu quyền bootstrap/read/write tương ứng trên NATS scoped
credential. Receipt cũ trong config KV vẫn được đọc để replay không chạy lại
mutation. Không bật GC trước khi có terminal-settlement acknowledgement và
transport-enforced replay retirement; đầy quota phải fail-closed, không evict.
Receipt schema 2 giữ cả SUCCEEDED/FAILED, schema 1 cũ chỉ có success vẫn đọc được.
Cần quiesce DP binary cũ trước khi đổi writer sang bucket mới; read fallback
không bảo đảm an toàn cho mixed-version writers.

`NATS_ZONE_URL` chỉ là Zone-local JetStream. Zone KV luôn mở qua trust root
`NATS_ZONE_TLS_CA`, scoped credential file `NATS_ZONE_CREDS`, và client certificate/key
`NATS_ZONE_TLS_CERT`/`NATS_ZONE_TLS_KEY` khi listener dùng mTLS. Dev Compose bật mTLS thật với CA dùng
chung cho Zone nhưng leaf certificate riêng cho từng client; thiếu bất kỳ material nào thì bootstrap
fail-closed. Production Zone KV dùng file storage và replica `3/5`.

Dataplane chỉ subscribe `aurora.jobs.commands.zone.<zone_uuid>.v1`; không có fallback hoặc shared
platform command topic. Envelope thiếu `target_zone_id` hoặc mang Zone khác phải fail-closed vào DLQ.

Mọi `JobCommandV1.payload` từ Controlplane là `ProtectedPayloadV1`, không phải domain Protobuf
plaintext. Dataplane mở toàn bộ byte payload tại `src/security/jobpayload.rs`, sau đó executor mới
decode Storage/Mail/Hypervisor/Managed Service contract. JO chỉ kiểm tra public metadata rồi relay
đúng ciphertext đã commit; JO không có private key.

`JOB_PAYLOAD_PRIVATE_KEYS_FILE` là path bắt buộc tới read-only Zone-local JSON:

```json
{"keys":[{"key_id":"00000000-0000-0000-0000-000000000001","private_key":"<standard-padded-base64-of-32-raw-bytes>"}]}
```

File giới hạn 64 KiB và 64 key để rotation có thể giữ cả `active` lẫn `decrypt_only`. Bootstrap
fail-fast nếu file thiếu/sai. Producer chỉ dùng key active sau khi Zone report chứng minh mọi replica
fresh đều đã nạp cùng `key_id + SHA-256(public_key)`; nhờ vậy rolling update không giao ciphertext
cho pod cũ. Raw private key không được đưa vào env, log, Kafka, PostgreSQL hoặc Zone KV.

Compose development mount `dataplane/.secrets/` read-only tại `/run/secrets/aurora`; directory này bị
Git ignore. Operator phải provision `job-payload-keys.json`, đăng ký public X25519 counterpart qua
critical Hierarchy API, chờ tất cả replica report fresh rồi mới activate key. Không commit key mẫu hay
fallback private material để làm local stack tự chạy.

Docker development chạy Dataplane trong `dev/zone/compose.yml`.
Dataplane export OTLP vào `zone-otel-collector:4317`; nó không fallback sang
Central Collector. Zone Collector/Victoria dùng volume và network riêng, còn
Kafka đi qua `aurora-dev-transport`. Xem
`dev/README.md` cho start/stop order và network boundary.

## 2. Job runtime

```mermaid
sequenceDiagram
    participant K as Kafka Zone command topic
    participant I as ZoneJobIntake
    participant KV as Zone Coordination KV
    participant Q as Bounded QueuedJob channel
    participant E as JobExecutionRuntime
    participant C as Completion coordinator
    participant RK as Kafka results/retry

    I->>KV: cached read zone.metadata
    alt missing/error/status != active
        I->>I: fail-closed pause
    else active
        I->>K: manual poll
        I->>I: size/schema/trace/Zone validation + open protected payload
        I->>Q: enqueue ValidatedJob + KafkaDelivery
        Q->>E: worker owns one execution slot
        E->>KV: acquire lease.job.sha256(job_id)
        E->>E: execute with watchdog cancellation
        E->>C: terminal/retry decision
        C->>RK: durable result, retry or DLQ (acks=all)
        C->>K: settle contiguous terminal offset
        E->>KV: owner/fence checked release
    end
```

- Poison/cross-Zone command được DLQ trước settle.
- Command tối đa 1 MiB, domain payload tối đa 1,000,000 bytes và execution timeout tối đa 1 giờ;
  DLQ không copy raw command mà chỉ giữ byte length + SHA-256 fingerprint để không biến quarantine
  topic thành secret store.
- `job_version > 0` và route `mail→MAIL`/`storage→STORAGE` được validate trước side effect.
  `vps` fail-closed vào DLQ cho tới khi JO có durable IAM/VPS result owner.
- Retry publish command `attempt+1` trước settle original nhưng tái sử dụng đúng ciphertext; attempt,
  trace context và Kafka offset không nằm trong AAD.
- Assignment epoch tăng khi rebalance; completion cũ không commit owner mới.
- Settlement serialize theo partition, hỗ trợ sparse offset và giới hạn cửa sổ fetched-but-not-terminal
  ở `4 × Ready workers`; record cao không làm commit vượt record thấp.
- Lease giảm concurrent duplicate nhưng external executor vẫn phải idempotent.
- Watchdog renew lease 30 giây theo chu kỳ 10 giây; execution deadline độc lập với lease TTL.
  Deadline chỉ hủy future cục bộ, không chứng minh resource mutation thất bại. Watchdog đưa cùng
  job ID/version/attempt/delivery epoch và ciphertext vào retry scheduler hiện có sau 30–32 giây
  (lease TTL + jitter), không tạo terminal result. Recovery không tiêu thụ business retry budget.
  Source chỉ được settle sau Kafka retry publish ACK; restart trước đó replay source cũ.
  Retry publish thất bại khiến critical scheduler thoát để supervisor restart/replay;
  không chỉ log rồi để source kẹt trong assignment hiện tại. ACK của assignment cũ
  vẫn bị fence, không được commit offset của owner mới.
- Queue recovery có giới hạn và không block gia hạn lease khác. Overflow khiến critical watchdog
  thoát để supervisor restart/replay, không bỏ quên source trong assignment hiện tại.
  Gauge `dataplane_watchdog_recovery_queue_depth` theo dõi pending recovery; không còn timeout
  completion reporter hoặc gauge `dataplane_watchdog_completion_queue_depth`.
- Critical intake/retry/watchdog exit hoặc panic làm process fail-safe shutdown, fence
  execution đang active và trả lỗi để container supervisor restart.

Watchdog regression checks: `cargo test job_runtime` runs deadline/unit checks.
With a disposable Kafka broker, run
`AURORA_TEST_KAFKA=127.0.0.1:19092 cargo test job_runtime -- --ignored` for
protected-command/last-attempt replay, stale registration, completion race and
recovery queue backpressure checks. These tests do not mutate provider resources.

## 3. Admission và autoscaling

- Zone Control assigns probe, metadata, report, inventory and scale work units;
  Dataplane has no Zone-wide leader lease.
- Hysteresis mở circuit từ `90%`, đóng dưới `60%`.
- Admission budget theo số worker `Ready`, không theo `MAX_WORKERS`; pacing dùng admitted jobs/CPU/RAM.
- Bounded MPSC truyền backpressure.
- Mỗi node xuất cached lag của partition local; assigned Zone Control scale
  worker aggregates rồi phát directive có `assignment_epoch` TTL 15 giây.
- Worker chỉ apply directive đúng Zone/chưa hết hạn; lag stale giữ target trước.

## 4. Mail runtime

Mail configuration hydrate từ Zone KV; customer broker connection chỉ được mở tại Dataplane:

- source suites: Kafka, Redis Stream, NATS JetStream, RabbitMQ;
- connection envelope encrypted, chỉ đúng Zone adapter giải mã;
- one consumer binds one immutable template version;
- template lazy-load vào Moka L1 theo byte weight/TTL/singleflight;
- render escaped parameters rồi batch JMAP;
- customer broker settlement giữ native semantics;
- slot ownership dùng Zone KV lease/fencing.

Ghi chú chốt ngày 2026-08-27: chưa triển khai per-message inbox. Phần này được
hoãn để thiết kế cùng hướng thay persistence KV của Mail tại Zone bằng PostgreSQL
sau này; không chuyển storage backend hoặc thay thời điểm ACK broker trong commit
hiện tại. Pod chết sau khi nhận việc vẫn có thể khiến Drain kẹt; Delete tiếp tục
bị chặn để không báo hoàn tất sai. Xem giới hạn và quyết định đã chấp nhận trong
[Personal Drain](../god_view/mail/personal_mail_consumer_drain_god_view_workflow.md#deferred-decision--2026-08-27)
và [Tenant Drain](../god_view/mail/tenant_mail_consumer_drain_god_view_workflow.md#deferred-decision--2026-08-27).

Customer payload mặc định là `{to, parameter}` JSON. Internal verification topic dùng
`MailDispatchEnvelopeV1` Protobuf nhưng vẫn map thành cùng logical render request.

Mỗi active consumer slot phát bounded OTel health/lag metrics và structured state
events với registration scope `consumer + owner + workspace + Zone`. Zone OTel
Collector đưa chúng vào Victoria. Browser đọc qua ACR-signed Zone Public Edge và
`zone-runtime-stream`; Controlplane/JO/Shared Redis không nằm trong read path.
Dynamic telemetry không được ghi thành business state trong PostgreSQL, Kafka
hoặc Zone KV.

## 5. Recovery

| Failure | Behavior |
|---|---|
| Kafka poll/publish outage | Không settle source; replay sau recovery |
| Kafka poison data | Durable DLQ rồi settle |
| Zone KV unavailable | Ingestion fail-closed |
| Local payload key chưa được mount | Không settle offset; fail-safe shutdown để supervisor restart sau rollout fix |
| Protected payload sai auth/AAD/schema | Durable sanitized DLQ rồi settle; không lộ raw ciphertext |
| Pod chết in-flight | Kafka replay sau offset + lease expiry |
| Rebalance | Epoch fence chặn stale completion |
| Result chưa durable | Không commit command |
| Zone Collector/Victoria unavailable | Runtime UI có thể stale; broker settlement và Kafka job không đổi |
| Metadata missing | Durable query/snapshot repair qua Kafka |

## 6. Code map

- `src/infra/kafka.rs`: producer, consumer, rebalance fence, contiguous settlement.
- `src/infra/zone_kv.rs`: buckets, CAS metadata và fenced lease.
- `src/job_runtime/model.rs`: validated command và phase types `QueuedJob`/`LeasedJob`.
- `src/job_runtime/intake.rs`: Zone gate, Kafka validation, admission và bounded enqueue.
- `src/security/jobpayload.rs`: bounded keyring, HPKE open và AAD route fence.
- `src/job_runtime/execution.rs`: execution routing và watchdog cancellation boundary.
- `src/job_runtime/completion.rs`: result/retry/DLQ durability rồi mới settle Kafka source.
- `src/job_runtime/coordination/`: fenced lease acquisition/renewal và execution watchdog.
- `src/workerpool/runtime.rs`: immutable job wiring và bounded multi-consumer queue.
- `src/workerpool/pool.rs`: execution-aware worker slots, drain và shutdown barrier.
- `../zone-control/src/orchestrator.rs`: weighted work-unit assignment and rebalance.
- `../zone-control/src/zone_health.rs`, `zone_metadata.rs`, `zone_storage.rs`, and
  `zone_scaling.rs`: Zone-wide control duties; Dataplane only applies their directives.
- `src/workerpool/scale_follower.rs`: apply fenced worker target.
- `src/executor/mail/runtime/`: customer broker suites.
- `src/executor/mail/processor/`: render/JMAP/batching.
- `src/executor/mail/supervisor/`: pod-local OTel runtime telemetry; không mở reverse command/report path.
- `src/executor/managed_service/`: Zone-local managed-service admission, typed YAML
  AST renderer, deterministic namespace/ownership checks, Kubernetes SSA/readiness/
  delete executor và versioned terminal result producer. Client chỉ dùng mounted
  Kubernetes service-account token/CA; không đọc Controlplane DB, Shared Redis,
  Vault hoặc Zone KV.

## 7. Structured log controls

| Environment | Default | Boundary |
|---|---:|---|
| `APP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `APP_LOG_BUFFERED_LINES` | `16384` | Clamped `1024..262144`; queue đầy drop có metric, không block Tokio |
| `APP_LOG_MAX_FIELD_BYTES` | `16384` | Clamped `1024..262144`; áp dụng trước JSON serialization |
| `APP_LOG_RATE_LIMIT_MS` | `5000` | Clamped `100..60000`; chỉ suppress warning/error trùng |
| `OTEL_TRACE_SAMPLE_RATIO` | `1.0` | Clamped `0..1`; ParentBased ratio chỉ áp dụng cho root trace mới |
| `OTEL_BSP_MAX_QUEUE_SIZE` | SDK default | Bounded in-process span queue; đầy thì drop trace, không block job |
| `OTEL_BSP_MAX_EXPORT_BATCH_SIZE` | SDK default | Phải nhỏ hơn hoặc bằng queue size |
| `OTEL_BSP_SCHEDULE_DELAY` | SDK default | Milliseconds giữa các lần batch export |
| `OTEL_BSP_EXPORT_TIMEOUT` | SDK default | Milliseconds timeout cho một batch export |

Dataplane emit một NDJSON record trên stdout cho mỗi event. Không cấu hình thêm direct OTLP Logs
song song với filelog collector vì cùng record sẽ bị ingest hai lần.
