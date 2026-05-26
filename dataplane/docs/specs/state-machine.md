# Job State Machine V1

## Source of Truth
- `job_state` trong DB controlplane là nguồn sự thật duy nhất.
- Redis stream là transport, không quyết định terminal truth.

## States
- `CREATED`
- `ENQUEUED`
- `PUBLISHED`
- `RUNNING`
- `SUCCEEDED`
- `FAILED`
- `CANCELLED`
- `DLQ`

## Valid Transitions
- `CREATED -> ENQUEUED`
- `ENQUEUED -> PUBLISHED`
- `PUBLISHED -> RUNNING`
- `RUNNING -> SUCCEEDED`
- `RUNNING -> FAILED`
- `RUNNING -> CANCELLED`
- `FAILED -> ENQUEUED` (retry, tăng attempt)
- `FAILED -> DLQ` (hết retry hoặc non-recoverable)

## Transition Guards
- Guard theo (`job_id`,`job_version`,`attempt`,`current_status`).
- Reject transition nếu status hiện tại không thuộc allowed predecessor.
- Reject completion nếu `job_version` stale.

## Retry Policy
- `max_attempt` mặc định: 5
- Backoff: exponential + jitter (20%)
- Retry chỉ cho lỗi retryable
- Non-retryable hoặc vượt `max_attempt` -> `DLQ`

## Completion Decisions
- `APPLIED`: completion hợp lệ, transition thành công.
- `DUPLICATE`: completion đã apply trước đó, trả ack thành công.
- `STALE_VERSION`: completion thuộc version cũ, không apply.
- `CONFLICT`: attempt/version/status guard mismatch.
- `RETRY_LATER`: lock contention hoặc dependency tạm thời.

## Invariants
- Không có 2 terminal states cho cùng (`job_id`,`job_version`,`attempt`).
- Không downgrade từ terminal về non-terminal.
- Transition phải audit-log đầy đủ `trace_id`, `zone`, `node_id`.
