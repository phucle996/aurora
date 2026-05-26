# Cloud-Native Job Queue + Outbox (Controlplane ↔ Dataplane) — Idea

## 1) Mục tiêu

Xây mô hình điều phối job cloud-native cho hệ Aurora:

- `controlplane` phát hành và quản lý lifecycle job
- `dataplane` (edge theo zone) consume + execute job
- chống mất job, chống duplicate, và giữ khả năng scale write-heavy

Mục tiêu cốt lõi:

- **không mất job** khi crash/restart
- **idempotent** ở cả publish và completion
- **zone isolation** rõ ràng để scale và giảm blast radius

---

## 2) Quyết định kiến trúc (đã chốt)

1. **Outbox nằm trong DB của controlplane** (core module).
2. **Business async job** đi qua Redis Stream.
3. **Runtime/control critical flow** (auth/session/bootstrap...) ưu tiên RPC.
4. Redis stream tổ chức theo **1 stream/zone**: `jobs:<zone>`.
5. `job_state` trong DB là **source of truth**; Redis là transport/event log.
6. Dataplane định hướng runtime bằng **Rust** để tối ưu worker path.
7. Consumer không chạy độc lập; consumer chạy bên trong worker do **Worker Orchestrator** quản lý.
8. Worker Orchestrator là nền tảng dùng lại cho nhiều workload: job business, mail, hypervisor.

---

## 3) Boundary trong `core`

`controlplane/internal/core` chịu trách nhiệm:

- Job orchestration (create/dispatch/reconcile/finalize)
- Transactional write: `job_state` + `outbox_record`
- Policy engine cho retry/backoff/dead-letter/timeout
- Completion apply (từ RPC hoặc completion-event)

Không để transport leak vào business rule:

- Redis adapter chỉ publish/consume event
- RPC handler chỉ nhận signal và gọi core service

---

## 4) Luồng chính (business async job)

1. CP nhận intent tạo job.
2. CP mở DB transaction:
   - insert/update `job_state`
   - insert `outbox_record` trạng thái `PENDING`
3. Transaction commit thành công.
4. Outbox Publisher worker đọc `PENDING` và publish vào `jobs:<zone>`.
5. Publish thành công -> update outbox `PUBLISHED`.
6. `WorkerOrchestrator` cấp phát `ZoneConsumerWorker` cho zone/workload tương ứng.
7. DP (Rust worker) consume từ `jobs:<zone>`, execute job.
8. DP gửi completion signal (phase đầu ưu tiên RPC để đơn giản vận hành).
9. CP nhận signal, apply policy idempotent, update `job_state/outbox` tương ứng.

---

## 5) Idempotency + Versioning

Mỗi job cần bộ định danh:

- `job_id` (stable unique id)
- `job_version` (optimistic version)
- `attempt` (retry attempt)

Quy tắc:

- Publish duplicate cùng `(job_id, job_version, attempt)` không tạo side effect mới.
- Completion duplicate cho phiên bản đã finalized trả về ack thành công (replay-safe).
- Completion mismatch version bị reject theo policy (`stale`/`conflict`).

---

## 6) Stream model theo zone

### Stream naming

- `jobs:<zone>`
- (tuỳ phase sau) `jobs:<zone>:dlq` cho dead-letter

### Payload tối thiểu

- `job_id`
- `job_version`
- `attempt`
- `job_topic` (vd: `vps.create`, `vps.resize`)
- `tenant_id`
- `resource_id`
- `trace_id`
- `created_at`

### Consumer behavior

- DP chỉ consume zone của nó
- `XACK` sau khi persist execution result nội bộ
- Có reaper cho pending stuck (XPENDING/XCLAIM hoặc cơ chế tương đương)

---

## 7) Completion signal: RPC vs Redis

### Phase 1 (khuyến nghị)

- DP -> CP qua RPC cho completion:
  - dễ authn/authz
  - dễ tracing/timeout/retry semantics
  - thuận lợi debug khi rollout ban đầu

### Phase 2 (nâng cấp)

- Bổ sung completion stream async cho throughput lớn
- CP vẫn apply policy trên cùng core state machine (không đổi domain)

---

## 8) Policy state machine (mức ý tưởng)

Trạng thái gợi ý:

- `CREATED` -> `ENQUEUED` -> `PUBLISHED` -> `RUNNING` -> (`SUCCEEDED` | `FAILED` | `CANCELLED`)

Policy chính:

- retry bounded theo `max_attempt`
- exponential backoff + jitter
- timeout per job type
- DLQ khi vượt retry hoặc lỗi không recoverable

---

## 9) Cloud-native checklist

- Horizontal scaling:
  - nhiều outbox publisher instance + lease/advisory lock
  - nhiều dataplane workers per zone do Worker Orchestrator autoscale
- Observability:
  - lag/pending/retry/dlq metrics theo zone và topic
  - trace_id xuyên CP -> Redis -> DP -> completion
- Resilience:
  - crash-safe nhờ transactional outbox
  - replay-safe nhờ idempotency key + version guard
- Isolation:
  - zone stream tách biệt, tránh noisy neighbor xuyên vùng

---

## 10) Rủi ro cần để ý sớm

- Hot zone gây backlog cục bộ
- Completion đến trễ sau khi job đã timeout/replace version
- Publisher duplicate do failover race
- Schema drift giữa CP payload và DP decoder (Rust)

Giảm thiểu:

- contract versioning rõ (`payload_schema_version`)
- strict validation trước publish/execute
- reconciliation job định kỳ ở CP

---

## 11) Kết luận

Hướng `DB outbox + Redis stream theo zone + DP Rust consumer` là **ổn cho cloud-native** và phù hợp workload write-heavy.

Điểm quan trọng nhất là giữ:

- **DB state machine là truth**
- **Redis là transport**
- **idempotent/versioned ở mọi điểm giao tiếp**

Tài liệu này là idea-level để chốt hướng; bước tiếp theo là viết spec chi tiết contract/state/policy và plan triển khai.
