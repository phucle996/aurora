# IAM Admin Auth Load Tests (k6)

Thư mục này chứa các kịch bản k6 để load test luồng admin auth/critical actions.

## Prerequisites

- cài `k6`
- controlplane đang chạy và có endpoint admin auth
- có sẵn dữ liệu test hợp lệ (admin key, MFA, device signing material)

## Env variables

- `BASE_URL` (default: `http://localhost:8080`)
- `ADMIN_API_KEY`
- `MFA_METHOD` (`totp` hoặc `recovery_code`)
- `MFA_CODE`
- `DEVICE_PUBLIC_KEY`
- `CRITICAL_PATH` (default: `/admin/critical/ping`)
- `ADMIN_SIGNATURE`
- `ADMIN_TIMESTAMP`
- `ADMIN_NONCE`
- `STEPUP_METHOD`
- `STEPUP_CODE`

## Run examples

```bash
k6 run internal/iam/test/k6/admin_login_smoke.js
k6 run internal/iam/test/k6/admin_critical_burst.js

# export summary evidence
k6 run internal/iam/test/k6/admin_login_smoke.js \
  --summary-export internal/iam/test/k6/reports/admin-login-smoke-staging-YYYYMMDD-HHMM.json

k6 run internal/iam/test/k6/admin_critical_burst.js \
  --summary-export internal/iam/test/k6/reports/admin-critical-burst-staging-YYYYMMDD-HHMM.json
```

## Baseline SLO (tạm cho admin flow)

- `http_req_duration`: `p(95) < 800ms`
- `http_req_failed`: `rate < 1%`

Đây là baseline tạm cho admin auth/critical routes, không đại diện SLO toàn IAM.

## Ghi chú

- Đây là skeleton test để team vận hành/QA tùy biến theo route thật.
- Khi test critical actions thực tế, cần pipeline tạo chữ ký động theo canonical contract.
- Cache trong admin flow hiện dùng mô hình `TTL-only by design` (replica-local RAM cache + DB fallback).

## User device runtime smoke

`user_device_runtime_smoke.js` xác minh nhanh contract user device fragment + presence:

- login → có cookies `access_token`, `refresh_token`, `device_id`, `device_secret`.
- list `/me/devices` cần đủ 4 cookie + jti hợp lệ → 200.
- refresh → rotate đủ 3 mảnh `device_id`, `device_secret`, `access_token`.
- logout → 204, server clear runtime + cookie.

```bash
k6 run internal/iam/test/k6/user_device_runtime_smoke.js \
  -e BASE_URL=http://localhost:28000 \
  -e IAM_USERNAME=demo@example.com \
  -e IAM_PASSWORD=secret123
```
