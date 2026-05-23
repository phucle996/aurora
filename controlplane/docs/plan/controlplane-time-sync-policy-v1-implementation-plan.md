# Controlplane Time Sync Policy V1 - Implementation Plan

## 1) Mục tiêu triển khai
Thêm app-level drift observability production-safe (probe + metrics + health signal), không can thiệp chỉnh clock. Infra remediation giao Ops qua runbook.

## 2) Current state vs target state
- Current: health check DB/Redis, chưa có drift visibility.
- Target: app có drift state/metrics; ops có runbook xử lý NTP.

## 3) Implementation changes (grouped by subsystem)

### Handler
**Files (SỬA)**
- `internal/http/handler/health_handler.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `Readiness` | SỬA | Chỉ DB/Redis status | Thêm drift state additive field | Operator thấy drift ngay ở health |

### Service
**Files (THÊM)**
- `internal/app/timesync_probe.go`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `StartTimeDriftProbe` | THÊM | Chưa có probe | Poll 30s, parse drift, update state | Có signal drift runtime |
| `CurrentTimeDriftState` | THÊM | Không có state chuẩn | Trả drift seconds + state | Dùng cho health/metrics |

### Repo
**Files (KHÔNG ĐỔI)**
- Không thay đổi.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | N/A | N/A | Drift không dùng repo |

### Middleware
**Files (KHÔNG ĐỔI)**
- Không thay đổi.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | N/A | N/A | Không đưa drift vào auth middleware V1 |

### Cache
**Files (KHÔNG ĐỔI)**
- Không thay đổi.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | N/A | N/A | Probe state in-memory |

### Route
**Files (KHÔNG ĐỔI)**
- Không endpoint mới.

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| N/A | KHÔNG ĐỔI | N/A | N/A | Health route giữ nguyên |

### Docs
**Files (SỬA)**
- `docs/idea/controlplane-time-sync-and-drift-full-idea.md`
- `docs/spec/controlplane-time-sync-policy-v1-spec.md`
- `docs/plan/controlplane-time-sync-policy-v1-implementation-plan.md`
- `docs/runbook/controlplane-time-drift-incident.md`

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Time-sync docs | SỬA | Mixed responsibility | Chốt app observe / ops remediate | Rõ ownership |

### Tests
**Files (THÊM/SỬA)**
- `internal/app/timesync_probe_test.go` (THÊM)
- `internal/http/handler/health_handler_test.go` (SỬA)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| Probe tests | THÊM | Chưa có | Parse/state mapping/unknown path | Khóa correctness |
| Health tests | SỬA | Chưa có drift field | Assert additive drift field | Khóa compatibility |

## 4) Contract changes
No public contract change. Readiness drift field chỉ additive.

## 5) Test plan + acceptance
- Happy: drift parse success -> state ok.
- Error: parse fail -> unknown.
- Threshold: warning/critical mapping đúng.
- Acceptance:
  - [ ] có 2 metrics drift
  - [ ] readiness additive drift state
  - [ ] state transition log
  - [ ] runbook infra hoàn chỉnh

## 6) Rollout & operations
- Enable: start probe sau bootstrap app.
- Fallback: parse fail -> unknown.
- Ops xử lý NTP theo runbook, không can thiệp app clock.

## 7) Risk & mitigation
- Parse output drift không ổn định giữa distro -> fallback unknown + parser tests.
- Alert noise -> dùng for-window threshold.
- Ownership mơ hồ -> docs chốt app/ops boundary.
