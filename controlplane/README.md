# Controlplane

## 1. Overview

### Project này là gì
`controlplane` là service trung tâm điều phối của hệ Aurora-style platform. Nó là nơi nhận request quản trị, xác thực, điều phối workflow hệ thống, ghi nhận trạng thái chuẩn hóa vào PostgreSQL, và phát lệnh xuống các runtime thành phần khác.

### Controlplane chịu trách nhiệm gì
- Expose HTTP/gRPC entrypoint cho admin/API/runtime integration.
- Quản lý config runtime và bootstrap hạ tầng dùng chung.
- Điều phối module nghiệp vụ như IAM, billing, topology, task, resource platform, hypervisor, SMTP.
- Ghi source of truth vào PostgreSQL.
- Phát job/event cho worker hoặc runtime khác qua Redis Stream / queue pattern.
- Quản lý authn/authz, RBAC, audit, token lifecycle, rate limit, observability.

### Controlplane không chịu trách nhiệm gì
- Không trực tiếp thực thi data-plane workload dài hạn trên host/VM/node.
- Không thay thế dataplane hoặc agent trong việc thao tác infra cục bộ.
- Không coi Redis là source of truth cho business state.
- Không đặt business logic vào shared HTTP middleware/global app bootstrap.
- Không lưu raw secret, raw token, raw key trong DB.

## 2. Architecture

### Mô hình controlplane / dataplane / agent
- `controlplane`: lớp điều phối trung tâm, nhận request, validate policy, ghi state chuẩn vào DB, phát lệnh xuống runtime khác.
- `dataplane`: lớp xử lý runtime/workload gần tài nguyên thực thi; tiêu thụ lệnh, thực hiện task, báo trạng thái ngược lại.
- `agent`: tiến trình chạy gần host/node/VM/service edge; nhận lệnh đặc thù, thực hiện side effect cục bộ, gửi heartbeat/result.

### Luồng request chính
1. Client gọi HTTP/gRPC vào controlplane.
2. Middleware gắn request ID, auth, access log, rate limit, origin/cookie guard.
3. Handler bind/validate request và gọi service module.
4. Service thực thi business logic, gọi repository nếu cần đọc/ghi DB.
5. Repository là nơi duy nhất phát SQL vào PostgreSQL.
6. Nếu có workflow async, service ghi state rồi enqueue job/event.
7. Response trả về từ handler, không rò internal error.

### Luồng job qua Redis Stream
1. Service tạo business record hoặc job metadata trong PostgreSQL.
2. Controlplane publish event/job sang Redis Stream hoặc queue abstraction.
3. Worker/dataplane/agent tiêu thụ message.
4. Runtime thực thi side effect, cập nhật trạng thái về controlplane hoặc DB path được kiểm soát.
5. Nếu Redis mất message tạm thời, PostgreSQL vẫn là nguồn state chuẩn để replay/reconcile.

### PostgreSQL là source of truth
- Mọi business state bền vững phải nằm ở PostgreSQL.
- Redis chỉ phục vụ queue/cache/coordination/transient runtime state.
- Không suy ra trạng thái chuẩn chỉ từ Redis nếu chưa được commit/đồng bộ vào DB.

## 3. Runtime Components

### Các binary/service chính
- `cmd/server/main.go`: binary entrypoint của controlplane.
- Shared runtime package:
  - `infra/psql`: PostgreSQL connectivity.
  - `infra/redis`: Redis connectivity.
  - `internal/http`: shared handler + middleware.
  - `internal/security`: crypto/auth/security helpers.
  - `internal/observability`: tracing, metrics, hooks.

### Service nào chạy bằng systemd
Trong production, controlplane thường được chạy bằng systemd unit riêng cho từng instance hoặc node role. Repo này đã bỏ template `packaging/systemd` cũ của aurora để tránh reuse sai, nhưng deploy prod vẫn nên dùng systemd hoặc orchestrator tương đương.

### Service nào expose HTTP/gRPC
- HTTP API: cổng app chính, mặc định `8080` theo config local hiện tại.
- gRPC server: cấu hình qua `GRPC_PORT`, mặc định local là `9443`, dùng một listener duy nhất với mTLS.
- Prometheus target/read path phụ thuộc cách app expose metrics endpoint trong runtime implementation.

## 4. Folder Structure

### Giải thích các folder global
- `cmd/server`: process entrypoint.
- `infra`: kết nối hạ tầng dùng chung như PostgreSQL, Redis, Telegram.
- `internal/app`: bootstrap ứng dụng, lifecycle, top-level composition, route assembly.
- `internal/config`: parse env/config typed struct.
- `internal/core`: shared kernel-level types/rules dùng toàn app.
- `internal/http`: shared HTTP handler/middleware/transport utilities.
- `internal/observability`: tracing, metrics, hooks, telemetry glue.
- `internal/primitive`: primitive dùng lại nhiều nơi nhưng không gắn business cụ thể.
- `internal/ratelimit`: rate limit primitives + Redis-backed logic.
- `internal/security`: password, JWT, token, PKI, MFA, secret crypto.
- `pkg`: shared package có thể dùng xuyên nhiều layer khi hợp lý.
- `proto`: protobuf contract dùng cho gRPC/service integration.
- `scripts`: helper scripts cho dev/build/ops.
- `dev`: local development assets như Prometheus config.

### `app` dùng làm gì
- Dùng cho bootstrap application lifecycle.
- Tạo dependency graph mức global.
- Compose top-level route/module.
- Chứa template style viết cho `app.go`, `module.go`, `route.go`, `bootstrap/*`.

### `config` dùng làm gì
- Load config từ environment.
- Chuẩn hóa default local/dev.
- Trả typed config để các layer khác dùng.
- Fallback phải nằm ở đây, không hardcode rải rác trong runtime package.

### `http` dùng làm gì
- Shared middleware: access log, authz, CORS, rate limiter, origin/csrf, observability.
- Shared transport helpers/health endpoint.
- Không chứa business logic module.

### `security` dùng làm gì
- Password hashing/verification.
- JWT/token helpers.
- MFA utilities.
- PKI/secret crypto/provider.
- Device signature verification.

### `observability` dùng làm gì
- OTEL tracing setup.
- Prometheus integration.
- PGX/Redis hooks cho telemetry.
- Request/trace correlation.

### `primitive` dùng làm gì
- Primitive/helper nền tảng không phụ thuộc module business.
- Chỉ nên chứa các kiểu/utility thật sự generic và ổn định.

### Từng module nằm ở đâu
- Mỗi module business nằm dưới `internal/<module>/`.
- Ví dụ shape mong muốn: `internal/iam`, `internal/billing`, `internal/task`, `internal/topology`, `internal/resourceplatform`.
- Shared global code không được trộn vào module và ngược lại.

## 5. Module Convention

### Cấu trúc bắt buộc cho module
Mỗi module nên chia tối thiểu:
- `domain` hoặc `entity`
- `repository`
- `service`
- `transport` (`http`, `grpc` nếu có)
- `migrations`
- `docs`
- `test` hoặc test file theo layer

### Rule giữa các layer
- Handler/transport -> Service -> Repository -> DB
- Không đi ngược chiều phụ thuộc.
- SQL chỉ nằm trong repository.
- Model DB không leak sang service.
- DTO transport không đi xuống repository.

### Handler không chứa business logic
- Handler chỉ bind, validate, normalize input, gọi service, map lỗi, trả response.

### Service không phụ thuộc HTTP
- Service nhận input ở dạng domain/entity/primitive phù hợp.
- Service không biết `gin.Context`, HTTP header, response JSON.

### Repository không xử lý authz
- Repository chỉ làm persistence.
- Authz/policy thuộc service hoặc lớp policy riêng phía trên repository.

## 6. Configuration

### Config lấy từ đâu
- Toàn bộ runtime config lấy từ environment variables.
- Parse qua `internal/config/config.go`.

### Env cần có
Tối thiểu cho local/dev:
- `APP_HTTP_PORT`
- `APP_TIMEZONE`
- `PSQL_HOST`, `PSQL_PORT`, `PSQL_USER`, `PSQL_PASSWORD`, `PSQL_DBNAME`
- `REDIS_ADDR`, `REDIS_PASSWORD`, `REDIS_DB`
- `PROMETHEUS_BASE_URL`
- `OTEL_EXPORTER_OTLP_ENDPOINT`
- Các secret auth/security khi module tương ứng dùng đến

### Secret inject kiểu gì
- Qua environment, file mounted secret, hoặc secret manager ở production.
- Không commit secret vào repo.
- Không hardcode secret ở source code.

### Config prod khác config local thế nào
- Local có thể dùng default/fallback trong `internal/config`.
- Prod phải set explicit env, TLS/mTLS, domain, secret, CIDR, endpoint.
- Prod không dựa vào localhost mặc định.

## 7. Database & Migration

### Mỗi module tự có migration
- Migration phải nằm trong `internal/<module>/migrations/`.
- Global controlplane không gom business migration vào một chỗ chung.

### Quy tắc up/down migration
- `up` phải idempotent ở mức hợp lý khi có `IF EXISTS/IF NOT EXISTS` hoặc `ON CONFLICT`.
- `down` phải reverse theo thứ tự dependency ngược lại.
- Không gộp nhiều concern không liên quan vào cùng file migration.

### Quy tắc seed
- Seed system data phải idempotent.
- Seed role/permission/config baseline phải dùng key ổn định.
- Seed không được tạo dữ liệu runtime ngẫu nhiên khó rollback.

### Quy tắc rollback
- Mỗi migration phải có path rollback rõ ràng.
- Rollback phải cân nhắc data loss và dependency side effect.
- Với migration phá hủy dữ liệu, phải có runbook/backup trước khi chạy.

### Không sửa DB thủ công trên prod
- Không `ALTER TABLE` tay trực tiếp trên production.
- Mọi thay đổi schema phải đi qua migration có review.

## 8. Security Model

### Cookie auth
- Session/cookie cho user-facing auth flow.
- Cookie phải dùng `Secure`, `HttpOnly`, `SameSite` phù hợp production context.

### Admin API key
- Admin API key là luồng tách biệt với user login.
- Chỉ lưu hash/prefix, không lưu raw key.

### mTLS nội bộ
- gRPC/runtime nội bộ production nên bật mTLS giữa controlplane và runtime components.
- Cert/key/CA inject qua config/secret path, không hardcode.

### RBAC
- Authorization phải dựa trên role/permission/scope rõ ràng.
- Scope thực thi nằm ở service/policy layer, không nằm trong repository.

### Audit log
- Action nhạy cảm phải có audit event.
- Audit gồm actor, target, scope, request/trace metadata khi khả dụng.

### Token rotation
- Refresh token phải hỗ trợ rotate/revoke/replay detection.
- Không lưu raw refresh token hoặc one-time token plain text.

### Không lưu raw secret/token/key
- Chỉ lưu hash, encrypted blob, hoặc public key tùy loại dữ liệu.
- Private key/secret gốc phải được bảo vệ bởi secret store hoặc env/file secret an toàn.

## 9. Observability

### Log format
- Log phải có cấu trúc đủ để search và correlation.
- Không log secret/token/key.

### Metrics
- Metrics phục vụ health/runtime performance/capacity.
- Nên expose endpoint scrape rõ ràng cho Prometheus.

### Tracing
- Dùng OTEL cho trace propagation.
- Endpoint tracing phải lấy từ config, không hardcode runtime fallback trong package observability.

### Request ID
- Mỗi request inbound nên có request ID để trace log/app flow.

### Trace ID
- Trace ID phải được propagate qua HTTP/gRPC khi có tracing.

### Audit event
- Audit event không thay thế log vận hành, nhưng là log bảo mật/nghiệp vụ chuẩn hóa.

### Health check
- Cần có endpoint health/readiness và kiểm tra DB/Redis tùy mức độ readiness.

## 10. Local Development

### Cách chạy local
Có 2 kiểu chính:
- Chạy local process trực tiếp bằng Go.
- Chạy bằng Docker Compose dev stack.

### Cần PostgreSQL/Redis không
- Có. Phần lớn flow thực tế cần PostgreSQL và Redis.
- Dev stack hiện đã có `docker-compose.dev.yml` kèm `psql`, `redis`, `prometheus`, `grafana`.

### Cách chạy migration
- Tùy tool migration được chọn cho repo/module.
- Rule là chạy migration theo module path tương ứng, không apply SQL thủ công.

### Cách chạy server
- Docker hot reload: `docker compose -f docker-compose.dev.yml up --build`
- Go local: `go run ./cmd/server`

## 11. Testing

### Unit test
- Dành cho utility thuần và business logic cô lập.

### Service test
- Test rule nghiệp vụ, mock repository/dependency ngoài.

### Repository integration test
- Chạy với PostgreSQL thật hoặc test container.
- Verify SQL, migration, transaction, mapping.

### Handler test
- Test request binding, status code, response contract, middleware behavior.

### Load test
- Dành cho API path nóng, auth path, queue ingestion path, job dispatch path.

### Command chạy test
- Toàn repo: `go test ./...`
- Theo package: `go test ./internal/...`
- Có thể tách thêm command riêng khi test infra/repository cần env hỗ trợ.

## 12. Deployment

### Chạy bằng systemd
- Prod có thể chạy từng binary bằng systemd unit riêng.
- Mỗi instance nên có env/config file rõ ràng.

### HA systemd
- Có thể chạy nhiều instance controlplane phía sau LB.
- Cần đảm bảo shared DB/Redis, sticky/session strategy nếu flow yêu cầu.

### Nginx/LB
- Repo này đã bỏ template Nginx cũ, nhưng production vẫn có thể đặt sau Nginx/HAProxy/LB/cloud LB.
- TLS termination hoặc pass-through phải được xác định rõ.

### PostgreSQL HA
- PostgreSQL là source of truth nên HA/backup/replication là bắt buộc ở prod nghiêm túc.

### Redis HA
- Redis dùng cho queue/cache/coordination nên cần persistence/replication phù hợp mức criticality.

### Config production
- Prod config phải explicit cho domain, TLS, secret, DB, Redis, OTEL, metrics, CIDR, mTLS.

## 13. Operational Runbook

### Check service status
- systemd: `systemctl status <service>`
- Docker: `docker ps`, `docker compose ps`

### Xem logs
- systemd: `journalctl -u <service> -f`
- Docker: `docker logs -f <container>`

### Check health
- HTTP health endpoint.
- Readiness DB/Redis khi cần.

### Check DB
- Verify PostgreSQL reachable.
- Check migration version/schema/module tables.

### Check Redis
- Ping Redis.
- Check stream/group lag nếu dùng Redis Stream.

### Restart service
- systemd: `systemctl restart <service>`
- Docker: `docker compose restart <service>`

### Rollback cơ bản
- Stop rollout mới.
- Restore config/image/version trước đó.
- Chạy rollback migration nếu migration path cho phép và đã đánh giá data impact.

## 14. Troubleshooting

### Login lỗi thì check gì
- Cookie config, origin/csrf guard, secret signing, user status, session store, clock skew.

### Permission denied thì check gì
- Role assignment, scope resolution, permission mapping, admin CIDR, middleware authz path.

### Redis stream lỗi thì check gì
- Redis connectivity, stream/group state, pending entries, consumer lag, retry/ack flow.

### DB migration lỗi thì check gì
- Migration order, schema search path, object dependency, seed idempotency, rollback state.

### Admin API key lỗi thì check gì
- Key hash verification, prefix parsing, expiry/revoke state, CIDR restriction, clock/timezone mismatch.

## 15. Production Checklist

- Migration ok
- Secret ok
- Cookie `Secure` / `HttpOnly` ok
- mTLS enabled
- Rate limit enabled
- Audit enabled
- Backup enabled
- Monitoring enabled
- PostgreSQL HA/backup verified
- Redis persistence/HA verified
- Trace/metrics/log correlation verified

---

## Current Repo Notes

- `Dockerfile.prod` dùng cho production image.
- `Dockerfile.dev` + `docker-compose.dev.yml` dùng cho local hot reload.
- `docker-compose.dev.yml` hiện đã có `psql`, `redis`, `prometheus`, `grafana`.
- `internal/app` hiện được mang sang như template global/style composition để làm chuẩn cách viết.
