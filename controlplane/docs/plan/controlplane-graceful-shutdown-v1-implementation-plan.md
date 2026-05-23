# Controlplane Graceful Shutdown V1 - Implementation Plan

## 1) Mục tiêu triển khai
Triển khai graceful full shutdown theo spec target, nhằm chuẩn hóa shutdown order, timeout semantics, và stop idempotency trước khi xem là prod-ready.

## 2) Current state vs target state
- Current: shutdown behavior chưa được xác nhận đủ theo spec target.
- Target: shutdown lifecycle đạt đủ acceptance criteria trong spec.

## 3) Implementation changes (grouped by subsystem)

### Handler
**Files (SỬA - nếu cần)**
- `internal/http/handler/health_handler.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Readiness shutdown signal | SỬA | Chưa đảm bảo visibility theo shutdown target | Additive signal rõ khi shutdown lifecycle bắt đầu | Dễ quan sát drain behavior |

### Service
**Files (KHÔNG ĐỔI)**
- Không thay business services.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | N/A | N/A | Shutdown orchestration ở app/module layer |

### Repo
**Files (KHÔNG ĐỔI)**
- Không thay repo.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | N/A | N/A | Không thuộc scope |

### Middleware
**Files (KHÔNG ĐỔI)**
- Không thay middleware.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | N/A | N/A | Không thuộc scope |

### Cache
**Files (KHÔNG ĐỔI)**
- Không thay cache.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | N/A | N/A | Không thuộc scope |

### Route
**Files (KHÔNG ĐỔI)**
- Không endpoint mới.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | N/A | N/A | Không thuộc scope |

### Docs
**Files (SỬA/THÊM)**
- `docs/idea/controlplane-graceful-shutdown-full-idea.md`
- `docs/spec/controlplane-graceful-shutdown-v1-spec.md`
- `docs/plan/controlplane-graceful-shutdown-v1-implementation-plan.md`
- `docs/runbook/controlplane-graceful-shutdown-ops.md`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Docs set | SỬA/THÊM | Chưa chuẩn pre-implementation framing | Chuẩn hóa rõ current-vs-target và rollout path | Dễ quyết định start code |

### Tests
**Files (THÊM/SỬA)**
- `internal/app/*_test.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `App.Stop()` lifecycle tests | THÊM/SỬA | Coverage shutdown chưa chứng minh đủ target | Thêm test order/idempotent/timeout fallback | Khóa production behavior |

## 4) Contract changes
No public contract change.

## 5) Test plan + acceptance
- Signal -> shutdown sequence đúng order.
- HTTP/gRPC/OTel timeout fallback đúng spec.
- `Stop()` gọi nhiều lần không panic.

## 6) Rollout & operations
- Áp dụng theo phase: test -> staging drill -> prod rollout.
- Theo dõi logs theo phase shutdown.

## 7) Risk & mitigation
- Risk: sequence lệch giữa docs và code.
  - Mitigation: bắt buộc test lifecycle + docs sync cùng PR.
- Risk: timeout không đủ trong workload thật.
  - Mitigation: staging drill trước prod.
