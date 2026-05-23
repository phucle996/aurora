# Controlplane Graceful Shutdown V1 - Specification

## 1) Purpose + Scope
### Purpose
Định nghĩa behavior mục tiêu cho graceful full shutdown của controlplane process.

### In-scope
- Signal handling và stop entrypoint.
- Shutdown ordering và timeout semantics.
- Error handling best-effort theo từng phase.
- Ops runbook behavior.

### Out-of-scope
- Cluster-wide distributed drain policy.
- K8s hook chi tiết (preStop/terminationGracePeriod) ở bản này.

## 2) Terminology / Actors
- Main process
- App runtime
- Modules
- Infra resources (HTTP/gRPC/OTel/DB/Redis)

## 3) API Contract
- Không tạo public endpoint mới.
- Có thể mở rộng health/readiness additive nếu cần cho shutdown visibility.

## 4) Flow Behavior
### Target main flow
1. Nhận signal shutdown.
2. Mark not-ready.
3. HTTP drain với timeout cấu hình.
4. gRPC graceful stop + fallback force stop khi timeout.
5. Module stop hooks.
6. Telemetry shutdown.
7. Root context cancel.
8. Close infra clients.

### Failure branches
- Nếu phase lỗi, MUST log system error và tiếp tục phase tiếp theo.
- Stop MUST idempotent và nil-safe.

## 5) Data & Boundary Rules
- Shutdown flow không được phụ thuộc business state mutation.
- Cleanup responsibility tách theo layer app/module/infra.

## 6) Security Rules
- Không log secret/token trong shutdown logs.
- Không bypass readiness transition khi stop.

## 7) Failure Semantics
- Best-effort shutdown.
- Timeout mỗi phase được kiểm soát.
- Không deadlock vô hạn ở gRPC/HTTP stop.

## 8) Non-functional Baseline
- HTTP drain timeout: 20s (target baseline).
- OTel shutdown timeout: 10s (target baseline).
- gRPC graceful timeout fallback: 5s (target baseline).

## 9) Acceptance Criteria
- [ ] Có shutdown order deterministic.
- [ ] Có timeout + fallback semantics cho HTTP/gRPC/OTel.
- [ ] Stop idempotent và nil-safe.
- [ ] Có runbook ops bám đúng target flow.
