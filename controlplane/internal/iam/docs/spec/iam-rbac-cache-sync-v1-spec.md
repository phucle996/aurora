# IAM RBAC Cache Sync V1 - Specification

## 1) Purpose + Scope

### Purpose
Định nghĩa behavior source-of-truth cho đồng bộ cache RBAC giữa nhiều replica IAM trong môi trường HA.

### In-scope
- Invalidation bus semantics.
- Epoch/version sync semantics.
- Retry/backoff và no-op behavior khi sync conflict.

### Out-of-scope
- Chi tiết provider cụ thể (Redis/PubSub implementation-level code).

---

## 2) Behavioral Model

### Primary path
- Sau mutation RBAC thành công ở DB:
  - publish invalidation event (`role` hoặc `all`).
- Replica nhận event:
  - invalidate local cache theo event kind.

### Secondary self-heal path
- Mỗi replica chạy sync loop định kỳ (`tick` baseline 30s).
- Loop kiểm tra version/epoch shared state.
- Nếu local epoch < shared epoch:
  - invalidate theo policy an toàn,
  - cập nhật local epoch.

---

## 3) Conflict + Failure Rules

- Event duplicate -> idempotent no-op.
- Event đến trễ -> dựa epoch để tránh rollback state mới.
- Bus unavailable tạm thời -> rely on periodic self-heal.
- Sync tick lỗi -> log + metric + retry tick sau, không crash process.

---

## 4) Performance + Stability Baseline

- Tick interval mặc định: `30s`.
- Jitter nhỏ khuyến nghị để giảm herd effect.
- Invalidate granular ưu tiên hơn invalidate all khi đủ thông tin.
- Không block request path chỉ vì bus consumer chậm.

---

## 5) Observability

- `iam_rbac_invalidation_total{kind,result}`
- `iam_rbac_epoch_drift_total`
- `iam_rbac_sync_total{result}`
- `iam_rbac_sync_duration_seconds`

Structured logs:
- `run_id`, `epoch_local`, `epoch_shared`, `event_kind`, `result`, `reason`.

---

## 6) Acceptance Criteria

- [ ] Mutation publish invalidate event theo contract.
- [ ] Replica nhận event invalidate đúng scope.
- [ ] Miss event vẫn tự-heal qua epoch sync.
- [ ] Sync failure không làm chết process.
