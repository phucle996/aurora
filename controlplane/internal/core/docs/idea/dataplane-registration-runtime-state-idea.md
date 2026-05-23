# Dataplane Registration & Runtime State (Core Idea)

## Mục tiêu

Tài liệu này mô tả ý tưởng cho phần `core` của controlplane về:
- đăng ký dataplane
- lưu runtime state của dataplane trong DB
- dùng state đó để hỗ trợ quyết định runtime (rebalance, failover, draining)

Đây là **idea doc**:
- không phải migration spec
- không phải implementation plan
- không chốt chi tiết API/proto

---

## 1) Vì sao cần DB state cho dataplane

Heartbeat stream (Redis) là luồng realtime tốt, nhưng không đủ làm source-of-truth dài hạn.

Controlplane cần DB state để:
- biết hệ thống đang có những dataplane nào
- biết trạng thái hiện tại của từng node
- truy vấn nhanh snapshot phục vụ scheduler/runtime decisions
- làm mốc nhất quán khi controlplane restart
- có lịch sử để phân tích sự cố/failover

---

## 2) Mô hình 2 lớp (đề xuất)

### Lớp 1: Realtime ingress

- Dataplane đẩy heartbeat mỗi 5s vào Redis stream.
- Controlplane consumer đọc stream để cập nhật runtime state.

### Lớp 2: Durable state

- Controlplane persist snapshot vào Postgres (`core` schema).
- Scheduler/rebalancer/failover chỉ đọc quyết định từ DB snapshot.

Tóm tắt:
- Redis = ingest/event bus
- Postgres = source-of-truth runtime state

---

## 3) Dataplane lifecycle state (ý tưởng)

Các trạng thái đề xuất:
- `registered`
- `ready`
- `degraded`
- `draining`
- `stale`
- `failed`
- `maintenance`

Semantics ngắn:
- `ready`: nhận job bình thường
- `draining`: không nhận job mới, chỉ xử lý job đang giữ
- `stale`: quá ngưỡng timeout heartbeat
- `failed`: xác nhận lỗi/rời cụm

---

## 4) Core data model (ý tưởng)

### 4.1 `dataplane_nodes`

Lưu snapshot trạng thái node hiện tại.

Fields ý tưởng:
- `id` (uuid v7)
- `node_code` (unique)
- `status`
- `region`, `zone`
- `version`
- `capabilities` (jsonb)
- `labels` (jsonb)
- `last_heartbeat_at`
- `last_confirmed_job_at`
- `created_at`, `updated_at`

### 4.2 `dataplane_node_runtime`

Lưu chỉ số runtime gần nhất cho scheduling.

Fields ý tưởng:
- `node_id`
- `active_jobs`
- `queue_depth`
- `cpu_percent`
- `mem_percent`
- `updated_at`

### 4.3 `dataplane_events` (optional phase sau)

Timeline event để debug/audit runtime transitions.

---

## 5) Runtime decision hooks

### Rebalance

- chọn node `ready`
- filter theo `capabilities`
- ưu tiên node load thấp hơn (`active_jobs`, `queue_depth`, CPU/MEM)

### Failover

- nếu node `stale`/`failed`:
  - fence node khỏi assignment mới
  - requeue/re-dispatch jobs chưa confirm
  - dựa trên idempotency key/job_id để tránh duplicate side effects

### Draining

- node `draining` không nhận job mới
- chỉ hoàn tất inflight jobs
- về `ready` khi admin/runtime chuyển trạng thái lại

---

## 6) Update model (ý tưởng)

Nguồn cập nhật state:
1. heartbeat stream consumer
2. job completion confirmation RPC
3. admin runtime actions (drain/maintenance/fail)

Nguyên tắc:
- write path cần idempotent theo `node_code` + event identity
- transition state cần guard logic (không nhảy trạng thái bừa)

---

## 7) Timeout / stale policy (ý tưởng)

Ví dụ phase đầu:
- heartbeat expected mỗi 5s
- stale threshold 15s
- failed threshold 60s (hoặc qua explicit failure event)

Lưu ý: giá trị cụ thể sẽ chốt ở spec/config phase.

---

## 8) Ranh giới với mail job pipeline

- Dataplane runtime state là concern của scheduler/control runtime.
- Mail job pipeline là concern riêng (notification channel).
- Không dùng mail stream để làm source runtime state cho dataplane.

---

## 9) Kết luận idea

Hướng hợp lý cho core:
- lưu dataplane registry + runtime snapshot vào DB
- dùng Redis stream cho ingest heartbeat/event
- dùng DB snapshot để ra quyết định rebalance/failover/draining
- thiết kế transition và failover path theo idempotency ngay từ đầu
