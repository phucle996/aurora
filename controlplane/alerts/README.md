# Controlplane Alert Rules

Thư mục này chứa alert rule YAML để nạp vào Prometheus Operator / Alertmanager stack của Grafana.

## Current rules

- `rules/anti-probing-ratelimit-v1-alerts.yaml`
  - Nhóm alert cho anti-probing rate limiter gồm 2 cụm chính:
    - local deny-cache efficiency/pressure:
      - `ControlplaneRateLimitLocalCacheHitRatioLow`
      - `ControlplaneRateLimitLocalCacheSaturationDropsHigh`
      - `ControlplaneRateLimitLocalCacheEvictionSpike`
    - backend/dependency degradation:
      - `ControlplaneRateLimitBackendUnavailableSurge`
      - `ControlplaneRateLimitBackendErrorToBlockedRatioHigh`

## On-call quick guide

- `critical` alerts:
  - xử lý ngay theo runbook trong `annotations.runbook_url`.
  - ưu tiên kiểm tra Redis/limiter backend health (`latency`, `saturation`, `network`, `error burst`).
- `warning` alerts:
  - xử lý sớm trong giờ trực.
  - đối chiếu thêm metric `security_ratelimit_check_total`, `security_ratelimit_error_total`, `security_ratelimit_local_cache_total` để xác định trend tăng hay nhiễu ngắn hạn.

## Load path suggestion

- Prometheus Operator: mount/apply `PrometheusRule` CR này trong cluster.
- Non-operator Prometheus: convert phần `spec.groups` sang `rule_files` format tương ứng.
