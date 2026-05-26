# Controlplane Spec — Job Queue Outbox V1

## 1. Responsibility
Controlplane chịu trách nhiệm:
- nhận intent tạo business async job
- ghi transactional `job_state + job_outbox`
- publish job qua outbox worker sang Redis stream `jobs:<zone>`
- nhận completion signal và apply policy idempotent

## 2. Core Modules
- `JobService`: create/retry/cancel/finalize
- `OutboxService`: pending scan, publish mark, retry schedule
- `PolicyEngine`: timeout/retry/dlq decision
- `CompletionHandler`: áp state transition từ completion contract

## 3. Persistence Requirements
- DB là source of truth
- Unique constraints:
  - (`job_id`,`job_version`)
  - (`job_id`,`job_version`,`attempt`,`event_type`) cho outbox
- Transaction bắt buộc khi create job:
  - insert `job_state`
  - insert `job_outbox(PENDING)`

## 4. Publisher Worker Requirements
- Poll outbox pending theo batch + lease/advisory lock
- Publish `XADD jobs:<zone>`
- Mark `PUBLISHED` idempotent
- Retry publish với exponential backoff + jitter
- Log structured theo `job_id`,`job_version`,`attempt`,`zone`,`trace_id`

## 5. Completion Apply Requirements
- Completion inbound qua RPC v1
- Compare-and-set theo guard (`job_id`,`job_version`,`attempt`,`status`)
- Replay-safe decisions: `APPLIED|DUPLICATE|STALE_VERSION|CONFLICT|RETRY_LATER`
- Không leak internal error detail ra ngoài client-facing message

## 6. Operations & SLO
- Metrics:
  - outbox pending
  - publish success/fail
  - completion apply latency
  - duplicate/stale/conflict counters
  - dlq inflow
- SLO baseline:
  - p95 dispatch latency theo zone
  - completion apply p95

## 7. Security
- RPC completion dùng mTLS + node identity binding
- Không log secret/token/raw sensitive payload
- Redis ACL theo role/service account

## 8. Failure Recovery
- Crash sau DB commit trước publish: outbox recovery publish lại
- Crash sau publish trước mark published: retry idempotent
- Late completion: apply decision `DUPLICATE` hoặc `STALE_VERSION`

## 9. Privacy & Deadline Policy
- `tenant_id` có thể tồn tại trong CP domain/persistence để phục vụ billing/audit/policy, nhưng không được đưa vào DP dispatch payload.
- CP phải enforce tenant isolation trước bước publish (routing/scheduling/authorization).
- `deadline_at` là policy field do CP phát hành; CP quyết định strict reject hay grace window khi completion đến muộn.
