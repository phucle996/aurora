# Controlplane Reuse Registry (Quick Lookup)

Mục tiêu: cho AI tra cứu nhanh **dùng cái gì ở đâu**, tránh tạo mới logic/symbol trùng lặp.

## 1) File/Folder Quick Map

| Need | Canonical path | Scope | Notes |
|---|---|---|---|
| App global wiring | `controlplane/internal/app/` | bootstrap/composition | đọc `app.go`, `route.go`, `module.go` trước |
| Shared middleware | `controlplane/internal/http/middleware/` | transport/security gate | không nhúng business logic |
| IAM module | `controlplane/internal/iam/` | auth/rbac/device/session | theo chain handler->service->repo |
| Core module | `controlplane/internal/core/` | zone/secret/runtime core | theo chain handler->service->repo |
| Security ratelimit core | `controlplane/internal/security/ratelimit/` | key/rule/evaluator | middleware chỉ gọi, không clone logic |
| Config | `controlplane/internal/config/` | runtime config | không hardcode env rải rác |
| Observability bootstrap | `controlplane/internal/observability/` | prometheus/otel hooks | register metrics tập trung |
| Constants | `controlplane/pkg/constant/` | header/context/cookie keys | ưu tiên reuse constants |
| Logger | `controlplane/pkg/logger/` | structured logging | không tự tạo logger mới |
| API response | `controlplane/pkg/apires/` | response envelope | dùng helper chuẩn |
| App error | `controlplane/pkg/apperr/` | canonical app errors | map internal->public rõ ràng |

## 2) Symbol Registry (Do/Don't)

| Symbol / Utility | Path | Use-case | Do | Don't |
|---|---|---|---|---|
| Logger package | `controlplane/pkg/logger/logger.go` | structured app/security logs | dùng logger hiện có, redact field nhạy cảm | không tạo logger framework mới trong module |
| Header constants | `controlplane/pkg/constant/http_header.go` | set/get HTTP headers | reuse constant key | không hardcode string header nếu đã có constant |
| Context key constants | `controlplane/pkg/constant/context_key.go` | pass context qua middleware/handler | reuse key sẵn có | không tự đặt key string trùng nghĩa |
| Cookie constants | `controlplane/pkg/constant/cookie.go` | cookie name/options | dùng constant chuẩn | không hardcode cookie name nhiều chỗ |
| Success response | `controlplane/pkg/apires/success_response.go` | trả response thành công | dùng envelope/helper chuẩn | không trả format custom lệch contract |
| Error response | `controlplane/pkg/apires/error_response.go` | trả lỗi public | giữ generic message khi security-sensitive | không leak internal reason ra client |
| App error | `controlplane/pkg/apperr/app_error.go` | phân loại/mapping lỗi | map lỗi theo contract thống nhất | không trả raw error nội bộ trực tiếp |
| Ratelimit bucket | `controlplane/internal/security/ratelimit/bucket.go` | token bucket Redis/Lua | gọi qua package ratelimit | không duplicate logic bucket ở middleware |
| Ratelimit stacked | `controlplane/internal/security/ratelimit/stacked.go` | multi-rule eval | build rules rồi gọi stacked | không tự viết evaluator song song trong transport |
| Ratelimit keys | `controlplane/internal/security/ratelimit/keys.go` | key building theo scope | dùng key builder chuẩn | không concat key ad-hoc thiếu chuẩn hóa |
| Ratelimit helpers | `controlplane/internal/security/ratelimit/helpers.go` | retry/header parse helpers | reuse helper hiện có | không parse header/retry theo logic riêng |

## 3) Lookup by Task (AI shortcut)

| Task | Read first | Then |
|---|---|---|
| Đổi middleware order/global chain | `controlplane/internal/app/app.go` | `controlplane/internal/http/middleware/*.go` |
| Gắn middleware theo route module | `controlplane/internal/iam/route.go` hoặc `controlplane/internal/core/route.go` | handler module tương ứng |
| Thêm/đổi auth headers | `controlplane/pkg/constant/http_header.go` | middleware/handler sử dụng |
| Thêm context metadata qua chain | `controlplane/pkg/constant/context_key.go` | middleware producer + observability consumer |
| Bổ sung security logging | `controlplane/pkg/logger/logger.go` | `internal/http/middleware/*` |
| Thay đổi rate-limit behavior | `internal/security/ratelimit/*` | `internal/http/middleware/ratelimiter.go` |
| Bổ sung metrics HTTP/security | `internal/observability/prometheus.go` | middleware emit point |
| Đổi API error contract | `pkg/apperr/app_error.go` + `pkg/apires/error_response.go` | handler mapping sites |

## 4) Reuse Check Template (bắt buộc cho plan/spec/idea)

- Reused symbol/constants:
  - `<symbol>` từ `<path>`
- New symbol added (nếu có):
  - `<symbol>` tại `<path>`
  - Lý do không reuse được: `<reason>`
- Boundary check:
  - Handler->Service->Repo->DB có bị leak không: `YES/NO`
- SoT check:
  - Nguồn dữ liệu/quyết định authoritative: `<source>`

## 5) Priority Rule

Khi có mâu thuẫn giữa đề xuất mới và registry này:

1. Ưu tiên reuse symbol hiện có.
2. Nếu bắt buộc tạo mới, phải ghi rõ lý do và impact compatibility.
3. Update lại `skill-knowledge-base.md` + file này ngay sau khi chốt canonical mới.
