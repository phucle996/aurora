# Controlplane Time Sync & Drift (Full Idea)

## Mục tiêu
Đảm bảo controlplane production-safe với các luồng nhạy cảm thời gian (JWT, signature skew, TTL scheduler) bằng mô hình:
- app-level: chỉ quan sát drift + phát tín hiệu,
- infra-level: ops quản trị đồng bộ giờ hệ điều hành.

## Quyết định kiến trúc
- **Không** can thiệp chỉnh clock trong app.
- App chỉ làm:
  - drift probe read-only,
  - metrics,
  - health additive signal,
  - state-transition log.
- Hạ tầng NTP (`chrony`/`timesyncd`, upstream, firewall, host bootstrap) thuộc Ops runbook.

## Vì sao
- Đồng hồ hệ thống là concern của OS/node, không phải business service.
- Nhét cơ chế time correction vào app làm tăng rủi ro và coupling.
- Production cần tách ranh giới rõ: app detect, ops remediate.

## Kết quả kỳ vọng
- Drift được phát hiện sớm qua metrics/alerts.
- Auth/signature issues do clock lệch giảm mạnh.
- Incident xử lý nhất quán qua runbook.
