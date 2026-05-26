# Cloud-Native Job Queue + Outbox V1 Spec

## 1. Scope

Spec này định nghĩa kiến trúc và contract cho luồng:

- Controlplane (CP) tạo và dispatch business async job
- Dataplane (DP, Rust) consume và execute theo zone
- Completion signal quay lại CP để finalize trạng thái

Ngoài scope:

- Chi tiết auth/session runtime-critical RPC flows
- UI/operator dashboard implementation

---

## 2. Goals & Non-Goals

### Goals

- Không mất job khi CP crash/restart.
- At-least-once delivery với idempotent handling end-to-end.
- Version-safe completion (stale completion không làm bẩn trạng thái).
- Zone-isolated queue để scale cloud-native.

### Non-Goals

- Exactly-once tuyệt đối trên distributed boundary.
- Multi-region active-active replication trong v1.

---

## 3. Source of Truth

- **DB `job_state` table là source of truth duy nhất**.
- Redis Stream chỉ là transport + replay log tạm thời.
- Outbox record là cầu nối transactional giữa DB và publish event.

---

## 4. Architecture Components

## 4.1 Controlplane Core (`controlplane/internal/core`)

- `JobService`: tạo job, validate policy, finalize completion.
- `OutboxService`: ghi outbox record, update publish state.
- `PolicyEngine`: retry/backoff/timeout/dlq decision.

## 4.2 Controlplane Workers

- `OutboxPublisherWorker`:
  - poll outbox `PENDING`
  - publish Redis stream `jobs:<zone>`
  - mark outbox `PUBLISHED` (idempotent)

- `ReconcilerWorker`:
  - detect outbox stuck
  - requeue/retry theo policy

## 4.3 Dataplane (Rust)

- `WorkerOrchestrator`: cấp phát, quản lý vòng đời, autoscale worker pools
- `ZoneConsumerWorker`: consume `jobs:<zone>` trong worker pool
- `JobExecutor`: route theo `job_topic`
- `CompletionReporter`: gửi completion về CP (RPC trong v1)

---

## 5. Data Model (Logical)

## 5.1 `job_state`

- `job_id` (PK logical)
- `job_version` (int)
- `zone`
- `job_topic`
- `resource_id`
- `status` (`CREATED|ENQUEUED|PUBLISHED|RUNNING|SUCCEEDED|FAILED|CANCELLED|DLQ`)
- `attempt`
- `max_attempt`
- `last_error_code`
- `last_error_message` (sanitized)
- `payload_json`
- `payload_schema_version`
- `created_at`, `updated_at`, `deadline_at`

Privacy boundary:

- `tenant_id` là CP-internal field (nếu cần cho billing/policy/audit) và không được expose sang DP dispatch payload.

Unique guard gợi ý:

- unique (`job_id`, `job_version`)

## 5.2 `job_outbox`

- `outbox_id` (PK)
- `job_id`, `job_version`, `attempt`
- `zone`, `stream_key` (`jobs:<zone>`)
- `event_type` (`JOB_DISPATCH`)
- `publish_state` (`PENDING|PUBLISHED|FAILED`)
- `publish_attempt`
- `next_retry_at`
- `last_publish_error`
- `created_at`, `updated_at`, `published_at`

Unique guard gợi ý:

- unique (`job_id`, `job_version`, `attempt`, `event_type`)

## 5.3 `job_completion_event` (optional nhưng khuyến nghị)

- log replay-safe cho completion inbound
- unique (`job_id`, `job_version`, `attempt`, `completion_seq`)

---

## 6. Redis Stream Contract

## 6.1 Stream topology

- Primary stream: `jobs:<zone>`
- Consumer group per DP cluster zone: `dp:<zone>:workers`
- Optional DLQ stream: `jobs:<zone>:dlq`

## 6.2 Message fields (required)

- `job_id` (string)
- `job_version` (int)
- `attempt` (int)
- `job_topic` (string)
- `resource_id` (string)
- `payload_schema_version` (int)
- `payload_json` (string json)
- `trace_id` (string)
- `created_at` (RFC3339)
- `deadline_at` (RFC3339, optional)

`deadline_at` semantics:

- Hard deadline mặc định cho v1.
- DP pre-check trước execute; nếu quá deadline thì không chạy và report `DEADLINE_EXCEEDED`.
- Nếu quá deadline trong lúc đang chạy, DP cố gắng cancel graceful rồi report `DEADLINE_EXCEEDED`.
- CP là nơi quyết định cuối cùng khi apply completion (strict deadline hoặc grace window theo policy).
- `deadline_at` luôn UTC RFC3339; không dùng để tính retry schedule.

## 6.3 Producer semantics

- CP publish bằng `XADD`.
- Nếu publish timeout/unknown result: retry idempotent dựa trên outbox unique key.
- Không update `job_state` terminal chỉ vì publish success.

## 6.4 Consumer semantics

- `ZoneConsumerWorker` dùng `XREADGROUP`.
- Chỉ `XACK` sau khi đã persist execution outcome local và completion đã được gửi thành công (hoặc persisted local retry queue cho reporter).
- Có pending reclaimer dùng `XPENDING` + `XCLAIM`/`XAUTOCLAIM`.

---

## 7. Completion Signal Contract (RPC v1)

Endpoint logical: `ReportJobCompletion`

Request fields:

- `job_id`
- `job_version`
- `attempt`
- `zone`
- `executor_node_id`
- `result_status` (`SUCCEEDED|FAILED|CANCELLED`)
- `result_code` (domain code)
- `result_message` (sanitized)
- `finished_at`
- `metrics_json` (optional)
- `trace_id`

Response fields:

- `ack` (bool)
- `decision` (`APPLIED|DUPLICATE|STALE_VERSION|CONFLICT|RETRY_LATER`)
- `server_time`

Rules:

- Same tuple (`job_id`,`job_version`,`attempt`) replay -> return `DUPLICATE` + `ack=true`.
- Lower version than current -> `STALE_VERSION`.
- Attempt mismatch -> `CONFLICT` (policy decides).

---

## 8. State Machine

Valid transitions:

- `CREATED -> ENQUEUED`
- `ENQUEUED -> PUBLISHED`
- `PUBLISHED -> RUNNING`
- `RUNNING -> SUCCEEDED|FAILED|CANCELLED`
- `FAILED -> ENQUEUED` (retry path, tăng `attempt`)
- `FAILED -> DLQ` (quá retry hoặc non-recoverable)

Invalid transitions bị reject và audit log.

Transition guard:

- optimistic check theo (`job_id`,`job_version`,`status`,`attempt`).

---

## 9. Idempotency & Versioning Rules

- **Dispatch idempotency**: outbox unique key chặn duplicate create/publish cùng logical dispatch.
- **Execution idempotency**: DP executor phải hỗ trợ detect side-effect đã applied (theo `job_id` + domain resource key).
- **Completion idempotency**: CP apply completion theo compare-and-set state + attempt/version guard.
- **Versioning**:
  - tăng `job_version` khi semantic payload thay đổi cần thay thế job cũ.
  - completion của version cũ không được overwrite version mới.

---

## 10. Retry, Backoff, Timeout, DLQ Policy

Defaults gợi ý v1:

- `max_attempt = 5`
- backoff: exponential (`2^attempt * base_ms`) + jitter 20%
- hard timeout theo `job_topic` profile (vd `vps.create` lớn hơn `vps.reboot`)
- `FAILED` với error retryable -> quay lại `ENQUEUED`
- `FAILED` non-retryable hoặc vượt `max_attempt` -> `DLQ`

Policy phải config-driven theo topic.

---

## 11. Concurrency & Scaling

- Nhiều CP publisher worker chạy song song, dùng lease/advisory lock theo shard outbox.
- Nhiều `ZoneConsumerWorker` trong cùng zone group, do `WorkerOrchestrator` điều phối.
- Partition key logic theo `zone` + optional `resource_hash`/`topic-shard` để giảm hot-key.
- Backpressure:
  - CP rate-limit publish theo lag/pending của zone
  - DP concurrency limit theo `job_topic` và workload class (`business`, `mail`, `hypervisor`)

Autoscaling baseline (DP WorkerOrchestrator):

- Scale out khi stream lag/pending age/queue depth/handler latency vượt ngưỡng.
- Scale in khi load thấp ổn định qua cool-down window để tránh thrashing.
- Min/Max worker per zone và per workload class phải cấu hình được.
- Scale-in phải drain in-flight worker trước khi shutdown.

---

## 12. Security

- CP<->DP RPC dùng mTLS.
- Completion request phải carry node identity và trace.
- Không log secrets/payload nhạy cảm raw.
- Redis ACL giới hạn key pattern theo service role.

---

## 13. Observability & SLO

Metrics bắt buộc:

- outbox pending count
- publish success/fail rate
- stream lag theo zone
- consumer pending age p95/p99
- completion apply latency
- duplicate/stale/conflict completion counters
- dlq inflow rate
- active workers / desired workers theo workload class
- autoscale decisions count (`scale_out`, `scale_in`)

Tracing:

- `trace_id` xuyên CP create -> outbox publish -> DP execute -> CP completion

Logging:

- structured log with `job_id`, `job_version`, `attempt`, `zone`, `topic`

---

## 14. Failure Modes & Recovery

- CP crash sau DB commit trước publish: outbox worker sẽ publish lại.
- CP crash sau publish trước mark published: publish retry phải idempotent.
- DP crash sau execute trước completion: local retry reporter + pending reclaim.
- Network partition: eventual completion qua retry pipeline.
- Late completion: apply decision `STALE_VERSION` hoặc `DUPLICATE`.

---

## 15. Rollout Plan (High-level)

- Phase A: schema + core state machine + outbox publisher (no DP cutover)
- Phase B: DP Rust consumer cho 1 topic pilot (`vps.create`)
- Phase C: completion RPC hardening + policy tuning
- Phase D: mở rộng topic + DLQ ops runbook

---

## 16. Open Decisions

- Completion stream async có bật ở v1 hay để v2.
- SLA timeout chuẩn theo từng `job_topic`.
- Chuẩn error-code taxonomy dùng chung CP/DP.
- Cơ chế dedupe side-effect tại domain executor (resource-level lock vs idempotency table).

---

## 17. Acceptance Criteria

- Không mất job trong test crash CP tại 3 điểm: pre-publish, mid-publish, post-publish.
- Duplicate completion không làm sai terminal state.
- Stale version completion bị reject đúng policy.
- Zone isolation hoạt động: consumer zone A không xử lý job zone B.
- Metrics/trace đủ để truy vết 1 job end-to-end dưới 5 phút điều tra.
