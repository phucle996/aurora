# Job Queue Contract V1 (CP ↔ DP)

## Scope

Contract giao tiếp cho business async jobs:

- CP publish job vào Redis stream theo zone
- DP consume/execute và report completion về CP

## 1) Redis Dispatch Contract

### Stream topology

- Stream chính: `jobs:<zone>`
- Consumer group: `dp:<zone>:workers`
- DLQ: `jobs:<zone>:dlq`

### Required fields

- `job_id` (string)
- `job_version` (int)
- `attempt` (int)
- `zone` (string)
- `job_topic` (string, vd `vps.create`)
- `resource_id` (string)
- `payload_schema_version` (int)
- `payload_json` (json string)
- `trace_id` (string)
- `created_at` (RFC3339)
- `deadline_at` (RFC3339, optional)

### Privacy boundary

- DP **không được biết tenant identity**.
- `tenant_id` không được publish vào Redis dispatch message cho DP.
- Multi-tenant isolation được enforce ở CP (routing/scheduling/policy), không expose tenant dimension xuống DP runtime.
- Nếu cần phân vùng tại DP, dùng `zone`, `job_topic`, `resource_id` đã được pseudonymized.

### Producer rules (CP)

- Publish bằng `XADD` từ outbox worker.
- Retry publish khi timeout với idempotency theo unique outbox key.
- Publish success không đồng nghĩa job terminal success.

### Consumer rules (DP)

- Consume bằng `XREADGROUP` đúng zone stream.
- `XACK` sau khi execution outcome đã được persist và completion đã enqueue/report an toàn.
- Reclaim pending stuck bằng `XPENDING` + `XCLAIM`/`XAUTOCLAIM`.

## 2) Completion RPC Contract (V1)

Method logical: `ReportJobCompletion`

### Request

- `job_id` (string)
- `job_version` (int)
- `attempt` (int)
- `zone` (string)
- `executor_node_id` (string)
- `result_status` (`SUCCEEDED|FAILED|CANCELLED`)
- `result_code` (string)
- `result_message` (sanitized string)
- `finished_at` (RFC3339)
- `metrics_json` (optional json string)
- `trace_id` (string)

### Response

- `ack` (bool)
- `decision` (`APPLIED|DUPLICATE|STALE_VERSION|CONFLICT|RETRY_LATER`)
- `server_time` (RFC3339)

### RPC semantics

- Replay cùng (`job_id`,`job_version`,`attempt`) -> `DUPLICATE`, `ack=true`.
- Completion version cũ hơn current -> `STALE_VERSION`.
- Attempt/version không khớp guard -> `CONFLICT`.

## 3) Idempotency Keys

- Dispatch idempotency key: (`job_id`,`job_version`,`attempt`,`event_type=JOB_DISPATCH`)
- Completion idempotency key: (`job_id`,`job_version`,`attempt`,`completion_seq?`)

## 4) Validation & Compatibility

- Mọi payload phải có `payload_schema_version`.
- CP và DP phải reject payload thiếu required fields.
- Khi đổi schema payload, tăng version và giữ backward compatibility tối thiểu 1 version rollout.

## 5) Error Code Baseline

- Transport-level: `RETRY_LATER`, `UNAVAILABLE`, `TIMEOUT`
- Domain-level: `INVALID_PAYLOAD`, `RESOURCE_CONFLICT`, `QUOTA_EXCEEDED`, `INTERNAL_EXECUTOR_ERROR`
- Completion decision-level: `DUPLICATE`, `STALE_VERSION`, `CONFLICT`, `APPLIED`

## 6) Examples

### 6.1 Redis dispatch message (example)

Stream: `jobs:sgp-1`

```text
XADD jobs:sgp-1 * \
  job_id "job_01J2Y8S6M8N4A2M7Q8K9R1T2U3" \
  job_version "3" \
  attempt "1" \
  zone "sgp-1" \
  job_topic "vps.create" \
  resource_id "vm_req_7a21" \
  payload_schema_version "2" \
  payload_json "{\"plan\":\"c2-standard-4\",\"image\":\"ubuntu-24.04\",\"region\":\"sgp\"}" \
  trace_id "trace-4f6d3a2c9b" \
  created_at "2026-05-26T09:20:00Z" \
  deadline_at "2026-05-26T09:35:00Z"
```

### 6.1.1 `deadline_at` semantics (giải thích chi tiết)

`deadline_at` là mốc thời gian RFC3339 do CP phát hành để ràng buộc **tính hợp lệ thời gian** của job execution.

- **Loại deadline**: hard-deadline ở mức policy (mặc định v1).
- **Nguồn thời gian chuẩn**: CP clock là chuẩn khi tạo job; DP dùng local clock để pre-check nhanh, CP quyết định cuối khi apply completion.

Lifecycle theo `deadline_at`:

- **Trước deadline**: DP xử lý bình thường.
- **Quá deadline trước khi bắt đầu execute**: DP không nên execute; report completion `FAILED/CANCELLED` với `result_code=DEADLINE_EXCEEDED`.
- **Quá deadline khi đang chạy**: executor cố gắng cancel graceful; report `DEADLINE_EXCEEDED`.
- **Completion đến CP sau deadline**:
  - nếu CP policy strict deadline: reject apply terminal success, decision theo policy (`CONFLICT` hoặc map riêng `DEADLINE_EXCEEDED`).
  - nếu CP policy cho phép grace window: CP có thể accept trong cửa sổ grace.

Quy tắc triển khai:

- `deadline_at` luôn là UTC RFC3339 (ví dụ: `2026-05-26T09:35:00Z`).
- Không dùng `deadline_at` để suy ra retry schedule; retry schedule do policy engine CP quyết định.
- Khi retry tạo `attempt` mới, CP có thể giữ nguyên hoặc cấp `deadline_at` mới tùy `job_topic` policy; phải ghi rõ trong state transition/audit.

### 6.2 Completion RPC request/response (APPLIED)

Request:

```json
{
  "job_id": "job_01J2Y8S6M8N4A2M7Q8K9R1T2U3",
  "job_version": 3,
  "attempt": 1,
  "zone": "sgp-1",
  "executor_node_id": "dp-sgp1-node-03",
  "result_status": "SUCCEEDED",
  "result_code": "OK",
  "result_message": "provision completed",
  "finished_at": "2026-05-26T09:24:12Z",
  "metrics_json": "{\"exec_ms\": 18234, \"steps\": 7}",
  "trace_id": "trace-4f6d3a2c9b"
}
```

Response:

```json
{
  "ack": true,
  "decision": "APPLIED",
  "server_time": "2026-05-26T09:24:13Z"
}
```

### 6.3 Duplicate completion (replay-safe)

Scenario: DP retry RPC do network timeout, nhưng CP đã apply trước đó.

Response expected:

```json
{
  "ack": true,
  "decision": "DUPLICATE",
  "server_time": "2026-05-26T09:24:20Z"
}
```

Expected behavior:

- DP coi là success terminal cho tuple (`job_id`,`job_version`,`attempt`).
- CP không tạo side-effect mới.

### 6.4 Stale version completion

Scenario: CP đã nâng job lên `job_version=4`, nhưng DP cũ gửi completion `job_version=3`.

Response expected:

```json
{
  "ack": true,
  "decision": "STALE_VERSION",
  "server_time": "2026-05-26T09:30:41Z"
}
```

Expected behavior:

- CP không overwrite trạng thái version mới.
- DP dừng retry completion cho version cũ.
