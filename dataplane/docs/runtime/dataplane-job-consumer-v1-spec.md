# Dataplane Spec — Rust Job Consumer V1

## 1. Responsibility

Dataplane (Rust) chịu trách nhiệm:

- consume business jobs từ stream zone tương ứng
- execute theo `job_topic`
- report completion về CP (RPC v1)
- đảm bảo execution path idempotent ở mức worker runtime

## 2. Runtime Components

- `WorkerOrchestrator`: cấp phát, quản lý vòng đời, và autoscale worker pool
- `ZoneConsumerWorker`: Redis stream reader/group consumer chạy bên trong worker
- `TopicRouter`: map `job_topic -> executor`
- `Executor`: thực thi task domain (`vps.create`, `vps.resize`...)
- `CompletionReporter`: gửi completion RPC + retry gửi khi transient lỗi

### Worker Orchestrator responsibilities

- Worker lifecycle: create/start/health-check/drain/stop/restart
- Routing worker pool theo workload class (`business_job`, `mail_job`, `hypervisor_job`)
- Autoscaling worker theo policy (lag, pending age, queue depth, handler latency)
- Enforce per-topic concurrency và backpressure
- Graceful rollout: scale-up/down không mất in-flight guarantees

## 3. Consume Semantics

- Chỉ consume stream `jobs:<zone>` được cấu hình
- Consume được thực hiện bởi `ZoneConsumerWorker` do `WorkerOrchestrator` quản lý
- Parse + validate required fields trước execute
- Nếu payload invalid: mark fail theo policy reporter (không panic worker)
- `XACK` sau khi outcome đã được persist/report-safe

Privacy boundary:

- DP không được nhận hoặc xử lý `tenant_id` trong dispatch payload.
- Mọi tenant-level policy/isolation do CP xử lý trước khi publish.

## 4. Idempotent Execution

- Dedupe key: (`job_id`,`job_version`,`attempt`)
- Nếu task side-effect đã apply trước đó, trả kết quả idempotent success path
- Không execute lại blind khi detect duplicate in-flight

Deadline handling:

- Nếu `deadline_at` đã quá hạn trước execute: skip execute, report `DEADLINE_EXCEEDED`.
- Nếu quá hạn khi đang execute: ưu tiên graceful cancel, sau đó report `DEADLINE_EXCEEDED`.

## 5. Completion RPC Semantics

- Gửi đầy đủ tuple (`job_id`,`job_version`,`attempt`) + `trace_id`
- Nếu nhận `DUPLICATE`: coi như success terminal cho attempt đó
- Nếu `STALE_VERSION`: stop retry completion cho attempt cũ
- Nếu `RETRY_LATER`/transport error: retry reporter với backoff

## 6. Reliability

- Pending reclaim support (`XPENDING` + claim)
- Worker concurrency limit theo `job_topic` do Orchestrator cấp phát
- Autoscale guardrails:
  - scale out khi stream lag/pending age vượt ngưỡng
  - scale in khi idle ổn định qua cool-down window
  - min/max worker bounds theo workload class
- Graceful shutdown:
  - stop nhận message mới
  - drain in-flight bounded theo timeout

## 7. Observability

- Metrics:
  - consume rate
  - handler duration by topic
  - completion retry count
  - pending reclaim count
- Logs structured:
  - `job_id`,`job_version`,`attempt`,`zone`,`topic`,`trace_id`

## 8. Security

- Redis auth/ACL chỉ cho keys stream zone được phép
- RPC mTLS tới CP
- Không log raw sensitive fields trong payload
