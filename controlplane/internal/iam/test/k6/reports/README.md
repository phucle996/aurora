# k6 Reports (Admin Auth)

Lưu evidence chạy k6 thực tế cho admin auth flow.

## Naming convention

- `admin-login-smoke-<env>-<yyyymmdd-hhmm>.json`
- `admin-critical-burst-<env>-<yyyymmdd-hhmm>.json`
- `summary-<env>-<yyyymmdd-hhmm>.md`

## Required metadata trong summary

- Commit SHA
- Environment (`local|staging|preprod`)
- Base URL
- Script name
- Threshold pass/fail
- p95 latency
- Error rate

## Example run

```bash
k6 run internal/iam/test/k6/admin_login_smoke.js \
  --summary-export internal/iam/test/k6/reports/admin-login-smoke-staging-20260514-1930.json

k6 run internal/iam/test/k6/admin_critical_burst.js \
  --summary-export internal/iam/test/k6/reports/admin-critical-burst-staging-20260514-1935.json
```
