# Controlplane Skill Knowledge Base (SoT)

## 1) Mục tiêu
File này là **single source of truth** cho các skill khi đọc codebase `controlplane`.
Mọi skill (plan/spec/idea) phải ưu tiên dùng file này để:
- hiểu module organization,
- hiểu app global composition,
- tái sử dụng symbol/utility sẵn có,
- tránh tạo mới logic trùng lặp.

## 2) Read Order (bắt buộc)
1. `controlplane/docs/knowledge/skill-knowledge-base.md` (file này)
2. `controlplane/docs/knowledge/naming-conventions.md`
3. `controlplane/docs/knowledge/reuse-registry.md`
4. `controlplane/internal/app/app.go`
5. `controlplane/internal/app/route.go`
6. `controlplane/internal/app/module.go`
7. module đích (`internal/iam/*` hoặc `internal/core/*`)

Nếu task chỉ thuộc module cụ thể, dừng ở subset tối thiểu; không scan toàn repo.

## 3) Kiến trúc tổ chức codebase

### 3.1 App Global (composition root)
- Bootstrap/runtime wiring:
  - `controlplane/internal/app/app.go`
  - `controlplane/internal/app/bootstrap.go`
  - `controlplane/internal/app/route.go`
  - `controlplane/internal/app/module.go`
- Config:
  - `controlplane/internal/config/config.go`
- Observability bootstrap:
  - `controlplane/internal/observability/prometheus.go`
  - `controlplane/internal/observability/otel.go`

### 3.2 Shared transport/middleware
- `controlplane/internal/http/middleware/*`
- Rule: middleware chỉ làm transport/security gate, không nhúng business logic module.

### 3.3 Module organization
- `internal/iam/*`: IAM domain/app/repo/svc/transport.
- `internal/core/*`: core domain/app/repo/svc/transport.
- Module pattern ưu tiên:
  - `domain/entity`
  - `domain/repo`
  - `domain/service`
  - `model` (DB model)
  - `repo_impl`
  - `service`
  - `transport/http/handler`

## 4) Boundary Rules (must follow)
- Strict chain: `handler -> service -> repository -> db`.
- SQL chỉ ở repo implementation.
- Service dùng domain entity; không kéo DB model lên service.
- Middleware không thay business rules của IAM/Core.
- Lỗi nội bộ map qua error envelope chuẩn; response public phải generic khi cần security.

## 5) Reuse-First Catalog (không được tự tạo mới nếu đã có)

### 5.1 Logging
- Canonical logger: `controlplane/pkg/logger/logger.go`
- Rule:
  - Dùng logger package hiện có cho structured logs.
  - Không tạo logger framework mới trong module.
  - Không log secret/token/raw sensitive subject.

### 5.2 HTTP header keys/constants
- Canonical headers constants: `controlplane/pkg/constant/http_header.go`
- Rule:
  - Khi cần header key, ưu tiên constant ở file này.
  - Không hardcode header string nếu constant đã tồn tại.

### 5.3 Context keys
- Canonical context keys: `controlplane/pkg/constant/context_key.go`
- Rule:
  - Reuse context key constants; không tự đặt key string mới trừ khi thật cần.

### 5.4 Cookie constants
- Canonical cookie names/options: `controlplane/pkg/constant/cookie.go`

### 5.5 API response envelope
- Canonical response helpers:
  - `controlplane/pkg/apires/success_response.go`
  - `controlplane/pkg/apires/error_response.go`
  - `controlplane/pkg/apires/api_response.go`

### 5.6 App errors
- Canonical error type/map:
  - `controlplane/pkg/apperr/app_error.go`

### 5.7 Security ratelimit utilities
- Ratelimit core:
  - `controlplane/internal/security/ratelimit/bucket.go`
  - `controlplane/internal/security/ratelimit/stacked.go`
  - `controlplane/internal/security/ratelimit/keys.go`
  - `controlplane/internal/security/ratelimit/helpers.go`

## 6) Decision Table: Dùng gì ở đâu
- Nếu cần log security decision -> dùng `pkg/logger/logger.go` + redact sensitive fields.
- Nếu cần header key -> check `pkg/constant/http_header.go` trước.
- Nếu cần context cross-middleware -> check `pkg/constant/context_key.go` trước.
- Nếu cần response JSON -> dùng `pkg/apires/*` hiện có.
- Nếu cần rate-limit key/eval -> dùng `internal/security/ratelimit/*` hiện có, không clone logic sang middleware.

## 7) Change Guardrails cho Skills
Mọi skill khi đề xuất code phải có mục `Reuse Check` ngắn:
- Reused symbol/constants từ file nào?
- Có tạo symbol mới không? Nếu có, vì sao không reuse được?
- Boundary có bị leak không (handler/service/repo/db)?

## 8) Anti-Drift Rule
Khi có file canonical mới/đổi vị trí:
- update file này trước,
- rồi mới update plan/spec/idea docs liên quan.

Nếu có mâu thuẫn giữa tài liệu khác và file này, coi file này là SoT cho skill routing/reuse cho đến khi được cập nhật.
