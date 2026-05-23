# Mail Consumer - Phase 1 Idea (Controlplane ↔ Dataplane HA)

## 1) Mục tiêu phase 1

Phase 1 tập trung vào **Consumer lifecycle orchestration**:
- Controlplane cung cấp REST APIs để khai báo và điều phối consumer.
- Dataplane thực thi runtime consumer (connect source, pull/push message, ack).
- Controlplane và dataplane giao tiếp điều khiển bằng gRPC.
- Hỗ trợ HA assignment, rebalance, scale up/down, failover.

**Chưa làm ở phase 1**:
- Full delivery pipeline optimization.
- Advanced policy engine.
- Multi-region active-active phức tạp.

---

## 2) Vai trò Controlplane vs Dataplane

## Controlplane
- Source of truth cho cấu hình consumer.
- API CRUD + operational actions (pause/resume/scale/test).
- Scheduler/orchestrator để assign consumer shard lên dataplane nodes.
- Theo dõi heartbeat + health state + desired/actual state.

## Dataplane
- Runtime execution:
  - khởi tạo connector,
  - subscribe/poll source,
  - checkpoint/offset,
  - emit normalized message.
- Gửi heartbeat định kỳ về controlplane.
- Báo cáo runtime metrics + errors.

---

## 3) Resource model (ý tưởng)

## 3.1 Consumer
- `consumer_id`
- `name`
- `source_type` (`kafka|redis_stream|rabbitmq|nats`)
- `source_config_ref` (secret/config reference)
- `status` (`enabled|paused|error|draining`)
- `parallelism` (desired workers/shards)
- `rebalance_policy` (`cooperative|eager`)
- `failover_policy` (`hot_standby|cold_restart`)
- `created_at`, `updated_at`, `version`

## 3.2 Consumer Assignment
- `assignment_id`
- `consumer_id`
- `shard_id` / `partition_key`
- `dataplane_node_id`
- `state` (`assigned|starting|running|draining|failed`)
- `lease_epoch`
- `lease_expires_at`

## 3.3 Dataplane Node
- `node_id`
- `zone/region`
- `capacity` (max workers)
- `labels` (connector capability)
- `last_heartbeat_at`
- `health` (`healthy|degraded|offline`)

---

## 4) REST API surface (phase 1)

## 4.1 Consumer CRUD
- `POST /api/v1/mail/consumers`
  - tạo consumer mới.
- `GET /api/v1/mail/consumers`
  - list + filter theo status/source_type.
- `GET /api/v1/mail/consumers/:id`
  - xem chi tiết + assignment state.
- `PATCH /api/v1/mail/consumers/:id`
  - cập nhật mutable fields (`name`, `parallelism`, `status`, policy).
- `DELETE /api/v1/mail/consumers/:id`
  - hard delete (xóa hẳn resource).

## 4.2 Operational APIs
- `PATCH /api/v1/mail/consumers/:id/status`
  - body ví dụ: `{ "status": "enabled|paused" }`
- `POST /api/v1/mail/consumers/:id/test-connect`
  - kiểm tra connectivity config trước khi enable.

## 4.3 CP ↔ DP gRPC contracts (internal)
- `ReportHeartbeat` (DP -> CP)
  - node gửi heartbeat + load + active assignments.
- `ReportActionResult` (DP -> CP)
  - DP báo kết quả triển khai action job (`success|failed`) theo `job_id`.
- `GetControlplaneActions` hoặc stream subscription model
  - DP nhận action intent đã được CP phát hành qua job stream.

---


## 4.4 Redis Stream action-job model

- Controlplane không push action trực tiếp vào DP bằng REST.
- Controlplane phát hành action dưới dạng job vào Redis Stream (mỗi job có `job_id`).
- Dataplane consumer đọc stream và tự triển khai action tương ứng theo `job_id`.
- Sau khi triển khai, DP gọi gRPC `ReportActionResult` về CP.
- CP xác nhận kết quả và chỉ khi CP confirm thì job mới được ack/xóa khỏi stream.

Ghi chú:
- Trạng thái job phải có vòng đời rõ: `pending -> claimed -> executing -> reported -> confirmed -> acked`.
- `job_id` là idempotency key chính cho action lifecycle.

## 5) Assignment trong môi trường HA

## 5.1 Nguyên tắc
- 1 shard active tại 1 thời điểm (single active owner per shard).
- Lease-based ownership + epoch fencing.
- Controlplane quyết định owner, dataplane chỉ execute assignment hợp lệ theo epoch.

## 5.2 Thuật toán assign (phase 1 practical)
- Lọc node `healthy` và đủ capability (`source_type` support).
- Score node theo:
  - available capacity,
  - zone balance,
  - current shard load.
- Chọn node score cao nhất cho từng shard.
- Ghi assignment với `lease_epoch` tăng dần.

## 5.3 Fencing
- Mỗi assignment có `lease_epoch`.
- Dataplane phải reject assignment cũ epoch thấp hơn.
- Khi controlplane reassign, epoch tăng để tránh split-brain consume.

---

## 6) Rebalance

## 6.1 Trigger rebalance
- Node join/leave.
- Capacity pressure.
- Consumer parallelism thay đổi.

## 6.2 Rebalance strategy
- Ưu tiên `cooperative rebalance`:
  - move dần từng shard để giảm downtime spike.
- Fallback `eager rebalance` khi state không nhất quán hoặc force recovery.

## 6.3 Rebalance flow
1. Controlplane mark shard `draining` trên old node.
2. Old node commit checkpoint cuối + stop.
3. Controlplane assign shard sang new node (epoch+1).
4. New node start from checkpoint.
5. Mark shard `running`.

---

## 7) Scale (định hướng)

- Không có API scale thủ công ở phase này.
- Scale sẽ là auto-scale (làm sau ở dataplane worker pool).
- Controlplane chỉ lưu policy/intent; dataplane worker pool tự quyết định nhịp scale runtime theo load/heartbeat.

---

## 8) Failover

## 8.1 Failure detection
- Node coi là suspect khi miss `N` heartbeat windows.
- Node coi offline khi vượt `T_failover`.

## 8.2 Failover flow
1. Controlplane mark node `offline`.
2. Revoke leases của assignments đang thuộc node đó.
3. Reassign shard sang healthy nodes (epoch+1).
4. Dataplane mới start từ checkpoint gần nhất.
5. Emit event/audit failover và publish action-job reassign nếu cần.

## 8.3 Duplicate prevention khi failover
- Lease epoch fencing (owner cũ không được continue).
- Idempotency key ở downstream pipeline.
- Checkpoint commit theo interval + graceful drain khi còn heartbeat.

---

## 9) Checkpoint/Offset strategy

- Kafka: partition offset commit.
- Redis Stream: consumer group ID tracking.
- RabbitMQ: ack/nack semantics.
- NATS: durable consumer sequence.

Nguyên tắc chung:
- At-least-once consume ở phase 1.
- Bù duplicate bằng idempotency downstream.

---

## 10) Observability bắt buộc

## 10.1 Metrics
- `mail_consumer_assignments_total{state}`
- `mail_consumer_rebalance_total{result}`
- `mail_consumer_failover_total{result}`
- `mail_consumer_heartbeat_stale_total`
- `mail_consumer_lag{consumer_id,shard}`
- `mail_consumer_processing_latency_seconds`

## 10.2 Tracing
- span chain:
  - `consumer.assign`
  - `consumer.rebalance`
  - `consumer.failover`
  - `consumer.start`
  - `consumer.stop`

## 10.3 Logs
- Structured fields:
  - `consumer_id`, `shard_id`, `node_id`, `lease_epoch`, `event`.
- Không log secret config.

---

## 11) API request examples

## 11.1 Create consumer
```json
{
  "name": "verify-account-consumer",
  "source_type": "redis_stream",
  "source_config_ref": "secret://mail/redis-stream/main",
  "parallelism": 3,
  "rebalance_policy": "cooperative",
  "failover_policy": "cold_restart",
  "status": "enabled"
}
```

## 11.2 Scale consumer
```json
{
  "parallelism": 6
}
```

## 11.3 Heartbeat payload (dataplane -> controlplane)
```json
{
  "node_id": "dp-node-a1",
  "timestamp": "2026-05-13T10:30:00Z",
  "capacity": {
    "max_workers": 32,
    "used_workers": 21
  },
  "assignments": [
    {
      "consumer_id": "cons_01",
      "shard_id": "0",
      "lease_epoch": 14,
      "state": "running",
      "lag": 128
    }
  ]
}
```

---

## 12) Rollout strategy phase 1

1. Build controlplane CRUD + heartbeat ingestion.
2. Build assignment scheduler + lease epoch fencing.
3. Enable manual rebalance/scale APIs.
4. Add automated failover on heartbeat timeout.
5. Add observability dashboards + alerts.

---

## 13) Kết luận

Phase 1 cho consumer nên ưu tiên:
- assignment correctness,
- failover safety,
- observability đầy đủ,
- scale/rebalance có kiểm soát.

Sau khi ổn định phase 1, mới mở rộng policy automation sâu hơn.
