# Controlplane Time Sync Policy V1 - Specification

## 1) Purpose + Scope
### Purpose
Định nghĩa behavior global cho việc quan sát clock drift trong app và xử lý đồng bộ giờ ở tầng hạ tầng.

### In-scope
- Drift probe read-only trong app runtime.
- Drift metrics + health additive signal.
- Alert thresholds và state semantics.
- Runbook infra xử lý NTP/time-sync.

### Out-of-scope
- App tự sửa system clock.
- Thay đổi business auth/token contracts.

## 2) Terminology / Actors
- **Drift probe**: app checker đọc lệch giờ từ nguồn hệ điều hành.
- **Ops**: đội vận hành chịu trách nhiệm NTP/host time sync.
- **State**: `ok|warning|critical|unknown`.

## 3) API Contract
- Không thêm public endpoint mới.
- Readiness payload có thể thêm drift field theo additive policy.

## 4) Flow Behavior
- Probe chạy định kỳ 30s.
- Parse drift từ nguồn hệ điều hành (ưu tiên `chronyc tracking`).
- Emit metrics + state.
- Log chỉ khi state thay đổi.
- Không chỉnh clock trong app.

## 5) Data & Boundary Rules
- Source-of-truth thời gian: OS clock đã sync bởi NTP daemon.
- App không là time authority.

## 6) Security Rules
- Drift telemetry không chứa secret/token data.
- Drift xử lý qua ops channel, không leak vào client contract.

## 7) Failure Semantics
- Probe parse fail => state `unknown`.
- App vẫn chạy; alert/operator xử lý.

## 8) Non-functional Baseline
- Probe interval: 30s.
- Threshold:
  - warning: drift > 0.5s trong 5m
  - critical: drift > 2s trong 1m
- Overhead thấp, không nằm request hot path.

## 9) Acceptance Criteria
- [ ] Có drift probe read-only trong app.
- [ ] Có `system_time_drift_seconds` + `system_time_sync_state`.
- [ ] Có readiness additive drift state.
- [ ] Có state-transition system log.
- [ ] Có runbook infra xử lý NTP/time-sync.
