# Runbook: Controlplane Time Drift Incident

## Scope
Runbook này dành cho Ops xử lý đồng bộ giờ hệ điều hành. App chỉ cung cấp metrics/state.

## Signals
- `system_time_drift_seconds`
- `system_time_sync_state` (ok/warning/critical/unknown)

## Trigger
- Warning: drift > 0.5s trong 5 phút.
- Critical: drift > 2s trong 1 phút.

## xử lý nhanh
1. Kiểm tra NTP service trên node (`chronyd`/`systemd-timesyncd`) active.
2. Kiểm tra upstream source reachable.
3. Kiểm tra đồng nhất drift toàn bộ controlplane nodes.
4. Node drift nặng: cân nhắc rút traffic tạm thời.
5. Khôi phục sync, theo dõi metric về `ok` trước khi đóng incident.

## nguyên tắc
- Không chỉnh clock từ app/service business.
- Mọi remediation ở OS/infra layer.
