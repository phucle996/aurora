# Dataplane Runtime Storage & Migration V1 (Temp Spec)

## 1) Mục tiêu

Đặc tả storage contract cho dataplane runtime trong controlplane core:
- dữ liệu nào lưu Postgres (durable/source-of-truth)
- dữ liệu nào đi Redis (realtime/event bus)
- từ đó quyết định migration DB cần có gì
- chuẩn hóa việc `dataplane_nodes` bắt buộc tham chiếu đúng 1 zone (`zone_id` NOT NULL)

Đây là spec storage/migration boundary, không phải implementation plan.

---

## 2) Nguyên tắc phân tách

- Postgres (`core` schema):
  - dữ liệu cần bền vững, query snapshot, phục vụ quyết định runtime.
- Redis:
  - luồng realtime ingest/event/heartbeat, transient.

Không dùng Redis làm source-of-truth dài hạn cho dataplane state.

---


## 2.1 Dependency migration order (bắt buộc)

Thứ tự migration cho runtime storage:
1. migrate `zones` trước (xem `zone-migration-v1-temp-spec.md`)
2. sau đó mới migrate `dataplane_nodes` với `zone_id` FK -> `zones.id`

Lý do:
- zone là danh mục nền tảng cho dataplane edge
- tránh tạo dataplane node khi chưa có taxonomy zone chuẩn

## 3) Lưu trong Postgres (bắt buộc)

## 3.1 `dataplane_nodes`

Mục đích: registry + trạng thái hiện tại của mỗi dataplane node.

Columns v1:
- `id` (uuid v7, PK)
- `status` (enum `dataplane_node_status`, not null)
- `zone_id` (uuid v7, FK -> zones.id, not null)  -- mỗi dataplane thuộc đúng 1 zone
- `capabilities` (jsonb, not null default `{}`)
- `created_at` (timestamptz, not null)
- `updated_at` (timestamptz, not null)

## 3.2 `dataplane_node_runtime` (defer)

Theo scope tối giản hiện tại, chưa cần bảng runtime metrics để tránh write update liên tục.

Defer sang phase sau nếu thật sự cần scheduler theo load.

## 3.3 `dataplane_events` (optional v1, recommended v1.1)

Mục đích: timeline event để audit/debug transitions.

Columns đề xuất:
- `id` (uuid v7, PK)
- `node_id` (uuid v7, FK -> dataplane_nodes.id)
- `event_type` (text, not null)
- `payload` (jsonb, not null default `{}`)
- `created_at` (timestamptz, not null)

Nếu chưa làm ở v1 thì defer sang v1.1.

---

## 4) Lưu trong Redis (bắt buộc)

## 4.1 Heartbeat stream

- stream key: đề xuất `dataplane:heartbeat`
- producer: dataplane
- tần suất: mỗi `5s`
- retention: theo ops policy (trim theo thời gian hoặc maxlen)

Payload tối thiểu:
- `node_id`
- `emitted_at`
- `version`
- `active_jobs`
- `queue_depth`
- `cpu_percent`
- `mem_percent`

## 4.2 Runtime event stream (optional)

- stream key đề xuất: `dataplane:runtime:event`
- dùng cho event trạng thái runtime không cần lưu ngay thành durable log

---

## 5) Dữ liệu không lưu DB ở v1

- heartbeat raw từng nhịp 5s (raw stream message)
- retry/backoff transient state của dataplane connection
- ephemeral stream consumer offsets ngoài cơ chế Redis group

---

## 6) Enum đề xuất cho DB

`dataplane_node_status`:
- `registered`
- `ready`
- `degraded`
- `draining`
- `stale`
- `failed`
- `maintenance`

---

## 7) Migration scope v1

## 7.1 Enum migration

- thêm enum `dataplane_node_status` trong `000001_core_enums.up.sql`

## 7.2 Table migration

- migrate `zones` trước (theo zone migration spec)
- sau đó thêm `dataplane_nodes` với `zone_id` FK
- chưa thêm `dataplane_node_runtime` ở v1 tối giản
- (`dataplane_events` optional: nếu chưa làm v1 thì tách migration sau)

## 7.3 Index migration

Bắt buộc:
- index `dataplane_nodes(status)`

---

## 8) Update model từ Redis -> DB

- Controlplane consumer đọc heartbeat stream.
- Upsert vào `dataplane_nodes` theo `id` (PK).
- Cập nhật chủ yếu `status` và `zone` khi có event điều phối tương ứng.

Idempotency:
- update phải an toàn nếu message trùng/replay.
- không tạo duplicate node theo `id` (PK).

---

## 9) Runtime decision read model

Scheduler/rebalance/failover chỉ đọc từ DB snapshot:
- `dataplane_nodes.status`
- `zone_id` (join zones khi cần đọc code/name)

Không đọc trực tiếp từ Redis stream để ra quyết định cuối cùng.

---

## 10) Acceptance criteria

- Có enum + tables + indexes đúng scope v1.
- DB chứa đủ snapshot tối giản để biết node thuộc `zone` nào và đang ở `status` gì.
- Redis chỉ giữ luồng realtime heartbeat/event, không thay vai source-of-truth.
- Upsert theo `id` (PK) idempotent.
