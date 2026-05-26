# Controlplane RateLimit Runtime Policy v1 Spec

## 1) Mục tiêu
Policy hóa tham số vận hành của:
- `internal/http/middleware/ratelimiter.go`
- `internal/security/ratelimit/decision_engine.go`

để tune runtime nhanh, giảm sửa code trực tiếp khi vận hành.

## 2) Phạm vi
### Trong phạm vi
- Contract policy keys cho rate admission, escalation, bypass, sampling, local guard.
- Typed parse schema trong policyengine runtime config.
- Wiring runtime snapshot từ policyengine sang middleware/ratelimit.

### Ngoài phạm vi
- Risk engine/scoring độc lập.
- Thay đổi business logic IAM.
- Thay đổi public error envelope.

## 3) Runtime policy contract

## 3.1 YAML shape đề xuất
```yaml
version: v1
policies:
  rate_limit:
    preauth:
      global_instant:
        max_inflight: 2000
        queue_limit: 0
        retry_after_seconds: 1
      ip:
        capacity: 30
        refill: 15
        period_seconds: 1
    postauth:
      ip_device:
        capacity: 40
        refill: 20
        period_seconds: 1
    decision_engine:
      throttle_ttl_seconds: 2
      isolation_ttl_seconds: 60
      block_ttl_seconds: 900
      escalation_window_seconds: 600
      block_threshold: 3
      max_keys: 4096
      evict_scan_limit: 128
    bypass:
      route_patterns:
        - /api/v1/health/liveness
        - /api/v1/health/readiness
        - /api/v1/health/startup
        - /metrics
    observability:
      sampling_percent:
        throttle: 10
        temporary_isolation: 50
        block: 100
        error: 100
    behavior:
      retry_after_fallback_seconds: 2
      fail_open: false
```

## 3.2 Field contract (must)
- `capacity`, `refill`: int64 > 0.
- `period_seconds`: int > 0.
- `preauth.global_instant.max_inflight`: int > 0.
- `preauth.global_instant.queue_limit`: int >= 0 (0 = không queue, reject ngay khi vượt ngưỡng).
- `preauth.global_instant.retry_after_seconds`: int [1..10].
- `throttle_ttl_seconds`: int [1..30].
- `isolation_ttl_seconds`: int [10..600].
- `block_ttl_seconds`: int [60..3600].
- `escalation_window_seconds`: int [60..3600].
- `block_threshold`: int [2..20].
- `max_keys`: int [1024..200000].
- `evict_scan_limit`: int [16..2048].
- `sampling_percent.*`: int [0..100].
- `retry_after_fallback_seconds`: int [1..30].
- `fail_open`: bool.

## 4) Mapping contract code ↔ policy
- `RateLimitPreAuth(...)` admission baseline lấy từ `rate_limit.preauth.ip`.
- `RateLimitPreAuth(...)` global instant gate lấy từ `rate_limit.preauth.global_instant`.
- HTTP status cho global instant reject là contract cố định ở middleware (429), không policy hóa ở v1.
- `RateLimitPostAuth(...)` admission baseline lấy từ `rate_limit.postauth.ip_device`.
- `DecisionEngine` state params lấy từ `rate_limit.decision_engine.*`.
- `shouldBypassRateLimit(...)` đọc từ `rate_limit.bypass.route_patterns`.
- `samplingPercentByDecision(...)` đọc từ `rate_limit.observability.sampling_percent.*`.
- `retryAfterFromRate(...)` fallback dùng `rate_limit.behavior.retry_after_fallback_seconds`.
- `Bucket.SetFailOpen(...)` bind với `rate_limit.behavior.fail_open`.

## 5) Default/fallback behavior
- Nếu key policy thiếu/invalid:
  - log warning nội bộ,
  - fallback về default hardcoded hiện tại (safe defaults),
  - không crash process.
- Nếu toàn bộ block `rate_limit` không có:
  - dùng full default hiện tại.

## 6) Validation rules
- Validation chạy lúc load policy snapshot.
- Invalid critical fields:
  - không apply snapshot mới,
  - giữ last-known-good snapshot.
- Validation errors phải có reason rõ trong internal log.

## 7) Observability contract
### Metrics (tối thiểu)
- `security_ratelimit_policy_reload_total{result}`
- `security_ratelimit_policy_active_version{}` (gauge=1 với label version/checksum)
- `security_ratelimit_policy_fallback_total{field}`

### Logs (tối thiểu)
- `policy_type=rate_limit`
- `policy_version`
- `policy_checksum`
- `reload_result`
- `fallback_fields`

## 8) Rollout plan
### Phase 1
- Policy hóa preauth global instant gate + admission + bypass + retry_after_fallback.
- Giữ escalation defaults hiện tại.

### Phase 2
- Policy hóa decision_engine TTL/threshold/window.

### Phase 3
- Policy hóa sampling + fail_open behavior per class (nếu cần).

## 9) Acceptance criteria
- Có thể tune admission/escalation qua YAML không sửa code.
- Có thể tune giới hạn tổng số request tức thời (preauth global instant) qua YAML.
- Fallback/default hoạt động đúng khi policy thiếu/sai.
- Không đổi contract response client.
- Build + tests pass cho policy parsing và middleware behavior chính.

## 10) Open questions
1. `fail_open` có cho phép phân tách theo route class (`public_read`, `auth_sensitive`) không?
2. `bypass.route_patterns` dùng exact match hay pattern syntax (glob/regex)?
3. Có cần chia sampling theo route class ngoài decision-level không?
