# 1. Bối cảnh và mục tiêu thay đổi

## Implementation status (code-aligned)

- `DONE (P1)`
  - `RateLimitPreAuth(...)` đã implement trong `internal/http/middleware/ratelimiter.go`.
  - Global pre-auth middleware đã wire ở `internal/app/app.go`.
  - Bypass policy đã apply: `/metrics`, `/api/v1/health/liveness`, `/api/v1/health/readiness`, `/api/v1/health/startup`.
  - Ratelimiter metrics đã register qua `observability.RegisterModuleMetrics(...)`.
- `DONE (P2 - core routes)`
  - `RateLimitPostAuth(...)` đã implement trong `internal/http/middleware/ratelimiter.go`.
  - Key builders `ip+device`, `ip+user` đã thêm ở `internal/security/ratelimit/keys.go`.
  - Post-auth đã wire sau auth middleware cho các route:
    - `GET /api/v1/auth/session`
    - `POST /api/v1/auth/logout`
    - `GET /admin/auth/session`
    - `POST /admin/auth/logout`
    - `POST /admin/auth/refresh`
    - `POST /admin/auth/rotate-key`
    - `GET /api/v1/me/devices`
    - `POST /api/v1/me/devices/:device_id/revoke`
    - `POST /api/v1/me/devices/logout-others`
    - `POST /api/v1/me/devices/logout-all`
- `NEXT`
  - Chuẩn hóa threshold per-route theo `RouteCostClass/RouteSecurityClass`.
  - Bổ sung verification matrix cho fallback path (`ip+user`) khi thiếu runtime device identity.

Dựa trên `controlplane/internal/security/docs/spec/controlplane-anti-probing-v1-spec.md`, cần triển khai v1 anti-probing theo hướng:
- tách middleware rõ phase:
  - `func RateLimitPreAuth(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc`
  - `func RateLimitPostAuth(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc`
- nâng từ 1-key IP sang multi-dimensional stacked evaluation,
- tách rõ objective capacity (admission) và objective abuse (identity-aware),
- bám sát code hiện tại để tránh scope drift.

Mục tiêu implementation vòng này:
1) đạt v1 implementable theo codebase hiện tại,
2) giữ boundary rõ giữa middleware và package `internal/security/ratelimit`,
3) hạn chế thêm helper rải rác, ưu tiên tái sử dụng symbol hiện có.

# 2. Phạm vi

## Trong phạm vi
- Cập nhật `internal/http/middleware/ratelimiter.go` để tách pre-auth và post-auth:
  - `RateLimitPreAuth(...)`
  - `RateLimitPostAuth(...)`
- Mở rộng package `internal/security/ratelimit` cho stacked decision + attribution tối thiểu.
- Bổ sung local fast-path cache mức middleware/ratelimit theo spec v1.
- Bổ sung metrics/log fields tối thiểu cho rule_scope + decision.
- Cập nhật route registration trong `internal/iam/route.go` ở mức tối thiểu nếu cần truyền thêm baseline metadata (không thay đổi semantics route).

## Ngoài phạm vi
- Không implement full risk engine scoring loop (đã có spec riêng).
- Không implement breaker substrate đầy đủ (đã có spec riêng).
- Không thay đổi business logic handler/service/repo IAM.
- Không triển khai probabilistic structures/hyperscale roadmap (nằm ở spec evolution).

## Phase roadmap (chuẩn bị code dài hơi)

### Phase P0 — Hardening docs + rollout guardrails (nhanh, ít rủi ro)
- Chốt spec/plan khớp route thực tế (`/api/v1/health/*`, `/metrics`).
- Chốt middleware order ở app bootstrap (RequestID trước AccessLog; PreAuth trước AccessLog).
- Chốt naming và context-key contract giữa middleware và observability.

### Phase P1 — PreAuth admission foundation (implementation-first)
- Đổi `RateLimit` hiện tại thành `RateLimitPreAuth`.
- PreAuth chỉ enforce rule `ip` + `route_pattern` + bypass policy.
- Gắn global preauth middleware ở `app.go`.
- Thêm metrics register trong `ratelimiter.go`, observability bootstrap gọi register.
- Giữ access log lean; security-event log riêng cho quyết định `throttle/cooldown/block`.
- Status: `DONE`.

### Phase P2 — PostAuth identity enforcement
- Thêm `RateLimitPostAuth` và gắn sau `Access(...)` cho route cần auth.
- Enforce `ip+tracking_device_id` và optional `user_id`.
- Hoàn thiện `FinalDecision` attribution + log sampling policy.
- Rollout trước cho nhóm route `RouteSecurityClass=sensitive|critical`.
- Status: `DONE` (đã wire cho nhóm auth/admin/me device routes trong implementation hiện tại).

### Phase P3 — Performance hardening + consistency
- Tối ưu stacked evaluation (giới hạn rule/window, tối ưu key builders).
- Thêm local fast-path cache cho cooldown/block + anti-poisoning cap.
- Tăng test coverage concurrency + benchmark Redis QPS/p95/p99.
- Chuẩn bị hook integration cho risk-engine/circuit-breaker (chưa bật full).
- Status: `IN PROGRESS`.

P3 increment đã làm:
- `ratelimiter` đã có local deny fast-path cache (size bounded + TTL bounded).
- Chỉ subject bị chặn mới vào cache (anti-poisoning).
- Cache đầy sẽ evict expired trước, không mở rộng unbounded.
- Thêm metrics local cache để đo hiệu quả offload Redis:
  - `security_ratelimit_local_cache_total{action,rule_scope}`
  - `action=lookup|hit|miss|set|evict_expired|set_drop_at_capacity`.

### Phase P4 — Optional evolution hooks (deferred)
- Cluster pressure adaptive inputs.
- Atomic multi-rule Lua nâng cao (nếu cần vượt ngưỡng tải).
- Route cost/security class auto-tuning pipeline.

# 3. Pre-Change Log

## 3.1 Hành vi hiện tại
- `internal/http/middleware/ratelimiter.go`:
  - `RateLimitPreAuth(...)` chỉ build 1 key: `ratelimit.Key("", name, clientIdentity(c))` (trước khi mở rộng sang stacked rules).
  - mỗi request gọi `Bucket.Allow(...)` đúng 1 lần.
  - decision chỉ có allow/429/503.
- `internal/security/ratelimit/bucket.go`:
  - có Redis Lua token bucket, parse result đầy đủ (`allowed`, `remaining`, `retry`, `reset`).
  - đã hỗ trợ `cost`.
- `internal/security/ratelimit/stacked.go`:
  - đã có `Stacked.Allow(...)` + `Rule`, `Decision`, `StackedResult`.
  - hiện chưa được middleware gọi.
- `internal/security/ratelimit/keys.go`:
  - có key scopes `ip/device/user/tenant`.
  - đã có helper composite `ip+device`, `ip+user` cho post-auth enforcement.
  - helper `route_pattern` key riêng chưa implement (defer cho phase stacked rules).
- `internal/app/app.go`:
  - đã set `ratelimiter.SetFailOpen(false)`.

## 3.2 File impacted và trách nhiệm hiện tại
- `controlplane/internal/http/middleware/ratelimiter.go` (transport/middleware): admission gate trước handler.
- `controlplane/internal/security/ratelimit/bucket.go` (security core): bucket execution + redis Lua bridge.
- `controlplane/internal/security/ratelimit/stacked.go` (security core): multi-rule orchestration.
- `controlplane/internal/security/ratelimit/keys.go` (security core): key construction.
- `controlplane/internal/security/ratelimit/helpers.go` (security core): headers + parse utils.
- `controlplane/internal/iam/route.go` (transport wiring): nơi áp middleware cho route auth/admin.

## 3.3 Gaps thúc đẩy thay đổi
- IP-only key gây false positive cao trong NAT.
- Chưa có dual objective rõ (capacity vs abuse) ở runtime implementation.
- Chưa có final decision attribution để forensic/tuning.
- Chưa dùng stacked evaluator dù package đã có nền.
- Chưa có bypass policy rõ cho health/ops endpoints (`, /api/v1/health/liveness`, `, /api/v1/health/readiness`, `/metrics`).

# 4. Naming Plan

## 4.1 Symbol mới/đổi tên
- `ratelimiter.go`
  - rename: `RateLimit -> RateLimitPreAuth`.
  - add: `RateLimitPostAuth`.
  - thêm type nội bộ: `rateLimitRuntimeInput` (mới) để gom context tránh truyền rời.
- `ratelimit/keys.go`
  - add: `KeyRoutePattern(prefix, routePattern string) string`.
  - add: `KeyIPDevice(prefix, ip, trackingDeviceID string) string`.
- `ratelimit/stacked.go`
  - add/extend result attribution: `TriggerRuleScope`, `EscalationReason` (mới) trong outcome struct phù hợp.
- `ratelimit` package
  - add enum/string constants rule scope chuẩn:
    - `RuleScopeIP`
    - `RuleScopeRoutePattern`
    - `RuleScopeIPDevice`
    - `RuleScopeUser`

## 4.2 Rename compatibility
- Đổi naming middleware theo phase rõ ràng: `RateLimitPreAuth(...)` và `RateLimitPostAuth(...)`; giữ nguyên contract baseline params để giảm chi phí migrate.
- Không đổi tên `Bucket.Allow(...)`, `Stacked.Allow(...)` để tránh lan scope.

## 4.3 Function Signatures (chốt trước khi code)

- Chữ ký middleware public sau update:
  - `func RateLimitPreAuth(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc`
  - `func RateLimitPostAuth(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc`

- Hàm private mới trong middleware (1 helper):
  - `func buildRateLimitRules(prefix string, baseline ratelimit.Rate, input rateLimitRuntimeInput) []ratelimit.Rule`

- Struct runtime input nội bộ middleware:
  - `type rateLimitRuntimeInput struct {
      RoutePattern string
      IP string
      TrackingDeviceID string
      UserID string
    }`

- Key builder mới trong `ratelimit` package:
  - `func KeyRoutePattern(prefix, routePattern string) string`
  - `func KeyIPDevice(prefix, ip, trackingDeviceID string) string`

- `Stacked` giữ chữ ký hiện có (không phá compatibility):
  - `func (s *Stacked) Allow(ctx context.Context, rules []Rule) (StackedResult, error)`

- Nếu cần attribution bổ sung trong `StackedResult`, ưu tiên mở rộng struct thay vì đổi chữ ký hàm.

# 5. File-Scoped Action Plan (gộp file + function)

## File: `controlplane/internal/http/middleware/ratelimiter.go`
- Ownership layer: handler/transport middleware.

### Function: `func RateLimitPreAuth(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc`
- Current state:
  - single-key IP evaluation.
- Planned action: **update**
  - giữ nguyên signature.
  - thay flow từ `Bucket.Allow` 1 lần sang build admission rules và gọi `Stacked.Allow`.
  - map decision theo severity (tối thiểu v1: allow/throttle; cooldown/block theo key state khi đã có).
- Expected behavior after change:
  - áp rule pre-auth: `ip` -> `route_pattern`.
  - response vẫn generic, set rate-limit headers theo blocked rule.
- Caller/callee impact:
  - caller app bootstrap và route registration cần cập nhật chain pre-auth/post-auth rõ ràng.
  - callee mới: `ratelimit.NewStacked(...).Allow(...)`.

### Function: `clientIdentity(...)`
- Current state:
  - trả IP/remote host.
- Planned action: **keep + minor update (if needed)**
  - giữ làm nguồn IP canonical cho runtime input.
- Expected behavior:
  - không đổi output contract.
- Caller/callee impact:
  - không ảnh hưởng.

### Function: `func RateLimitPostAuth(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc`
- Current state:
  - chưa có.
- Planned action: add.
- Expected behavior:
  - áp rule post-auth: `ip+tracking_device_id` -> `user_id(optional)` sau `Access(...)`.
- Caller/callee impact:
  - route chain cần đặt sau `Access(...)`.

### Function mới dự kiến trong file
- `buildPreAuthRules(...)` (mới, private)
  - current state: chưa có.
  - planned action: add.
  - expected behavior: sinh rules deterministic cho pre-auth từ baseline + runtime context.
  - impact: giúp `RateLimitPreAuth` không phình logic, nhưng vẫn giới hạn helper count.

- `buildPostAuthRules(...)` (mới, private)
  - current state: chưa có.
  - planned action: add.
  - expected behavior: sinh rules deterministic cho post-auth dựa trên context đã enrich.
  - impact: boundary rõ giữa admission và abuse enforcement.

## File: `controlplane/internal/security/ratelimit/keys.go`
- Ownership layer: security core.

### Function: `KeyRoutePattern(...)` (new)
- Current state: chưa có.
- Planned action: add.
- Expected behavior: build stable key cho route pattern normalize.
- Impact: dùng bởi middleware rule builder.

### Function: `KeyIPDevice(...)` (new)
- Current state: chưa có.
- Planned action: add.
- Expected behavior: build key composite `ip + tracking_device_id` có separator ổn định.
- Impact: giảm duplicate concat logic trong middleware.

## File: `controlplane/internal/security/ratelimit/stacked.go`
- Ownership layer: security core.

### Function: `Allow(...)`
- Current state:
  - trả `StackedResult` với blocked decision cơ bản.
- Planned action: **update nhỏ**
  - bổ sung attribution đủ dùng v1 (`blocked rule scope/name`) để middleware map `FinalDecision`.
- Expected behavior:
  - giữ backward compatibility cho caller cũ.
- Impact:
  - middleware có dữ liệu forensic tốt hơn mà không cần parse ngoài.

## File: `controlplane/internal/security/ratelimit/helpers.go`
- Ownership layer: security core.

### Function: `RateLimitHeaders(...)`
- Current state:
  - build headers từ `Result`.
- Planned action: **update nhẹ (nếu cần)**
  - đảm bảo path blocked từ stacked vẫn trả headers nhất quán.
- Expected behavior:
  - không đổi contract header names.
- Impact:
  - observability/caller stable.

## File: `controlplane/internal/http/middleware/observability.go`
- Ownership layer: handler/transport middleware observability.

### Function: `PrometheusHTTPMetrics(...)` (integration point)
- Current state:
  - đang ghi nhận HTTP metrics chung.
- Planned action: **update (nếu cần hook)**
  - bổ sung hook ghi metric rate-limit decision từ context keys do `RateLimitPreAuth/RateLimitPostAuth` inject.
- Expected behavior:
  - metric rate-limit được emit cùng request lifecycle, không tạo đường ghi riêng lẻ ngoài middleware chain.
- Caller/callee impact:
  - caller không đổi; `RateLimitPreAuth/RateLimitPostAuth` cần set context fields chuẩn.

## File: `controlplane/internal/http/middleware/accesslog.go`
- Ownership layer: handler/transport logging.

### Function: `AccessLog(...)`
- Current state:
  - log request metadata chung.
- Planned action: **keep lean (không phình)**
  - giữ access log ở mức field chuẩn như hiện tại.
  - không nhét full rate-limit forensic fields vào access log mặc định.
- Expected behavior:
  - chi phí log ổn định, không tăng cardinality/size đột biến do anti-probing fields.
- Caller/callee impact:
  - không đổi caller.

## File: `controlplane/internal/http/middleware/ratelimiter.go`
- Ownership layer: handler/transport admission + security-event logging ownership.

### Function mới: `logRateLimitSecurityEvent(...)` (private)
- Current state:
  - chưa có luồng security-event log riêng cho rate-limit.
- Planned action: **add**
  - chỉ log khi decision là `throttle/cooldown/block`.
  - structured log dùng logger package hiện tại (`controlplane/pkg/logger/logger.go`).
  - không log raw token/secret/raw subject id.
- Expected behavior:
  - forensic đủ sâu cho anti-abuse mà không làm phình access log chung.
  - tách rõ kênh operational access log và security-event log.
- Caller/callee impact:
  - `RateLimitPreAuth/RateLimitPostAuth` gọi private logger function này theo decision.

## File: `controlplane/internal/iam/route.go`
- Ownership layer: transport wiring.

### Function: `RegisterRoutes(...)`
- Current state:
  - đang gắn middleware rate-limit theo route (single-phase cũ).
- Planned action: **update**
  - thay `RateLimitPreAuth(...)` + `RateLimitPostAuth(...)` bằng chain rõ phase:
    - `RateLimitPreAuth(...)` trước `Access(...)`
    - `RateLimitPostAuth(...)` sau `Access(...)`
  - giữ nguyên semantics business handlers.
- Expected behavior:
  - route semantics giữ nguyên.
- Impact:
  - không đụng business handlers.

## File: `controlplane/internal/security/docs/spec/controlplane-anti-probing-v1-spec.md`
- Ownership layer: docs/spec.

### Section updates
- Planned action: **update**
  - thêm implementation status block: CURRENT_CODE / NEXT_INCREMENT / FUTURE_EVOLUTION.
  - chốt phần nào bắt buộc cho v1 code ngay, phần nào deferred.
- Expected behavior:
  - spec không lệch tone với codebase.

# 7. Contract & Boundary Checks

- Middleware boundary:
  - `RateLimitPreAuth` làm admission, `RateLimitPostAuth` làm identity-aware enforcement; không nhúng business IAM logic.
- Security core boundary:
  - `ratelimit` package giữ rule/key/eval; middleware không tự làm crypto/state phức tạp.
- Data/infra boundary:
  - Redis interactions tập trung ở `ratelimit` core, không rải qua handlers/services.
- Compatibility strategy:
  - chấp nhận đổi tên middleware public theo phase để boundary rõ; giữ baseline params để giảm phá vỡ callsites.

# 8. Risk / Impact Analysis

- Risk: tăng Redis QPS do multi-rule.
  - Mitigation: giới hạn max rules/request, local fast-path cache cho cooldown/block.
- Risk: false positive ở rollout đầu.
  - Mitigation: thresholds mềm trước, monitor decision distribution theo route scope.
- Risk: docs-code drift.
  - Mitigation: thêm implementation status trong spec và checklist verify trước merge.

# 9. Verification Plan

## 9.0 Phase-gated verification checklist

### P0 verification (docs + guardrails)
- Check P0-1: spec/plan dùng đúng bypass endpoints thực tế (`/api/v1/health/liveness`, `/api/v1/health/readiness`, `/api/v1/health/startup`, `/metrics`).
- Check P0-2: naming contract thống nhất `RateLimitPreAuth` / `RateLimitPostAuth` trong toàn bộ spec+plan.
- Check P0-3: middleware ordering expectation được mô tả rõ ở plan/spec.
- Check P0-4: anti-probing spec và risk-engine spec đều có `Implementation Status` để tránh scope drift giữa docs và code hiện tại.
- Success criteria:
  - không còn mâu thuẫn docs-code baseline trước khi code.

### P1 verification (PreAuth foundation)
- Check P1-1: `RateLimitPreAuth` chạy được cho toàn bộ route chain global.
- Check P1-2: bypass endpoints không bị throttle/cooldown/block.
- Check P1-3: access log vẫn lean, security-event log tách riêng theo decision.
- Check P1-4: metrics register của ratelimiter được gọi đúng 1 lần ở bootstrap.
- Success criteria:
  - không phá flow hiện tại,
  - có signal observability tối thiểu cho preauth decisions.

### P2 verification (PostAuth enforcement)
- Check P2-1: `RateLimitPostAuth` chỉ chạy sau `Access(...)` ở route cần auth.
- Check P2-2: rule `ip+tracking_device_id` hoạt động đúng, giảm block oan theo NAT.
- Check P2-3: optional `user_id` rule không làm fail request khi thiếu context.
- Check P2-4: `FinalDecision` attribution có đủ `decision/rule_scope/reason/retry/ttl`.
- Success criteria:
  - identity-aware enforcement hoạt động đúng,
  - forensic fields đầy đủ cho blocked decisions.

### P3 verification (performance + consistency)
- Check P3-1: latency middleware tăng trong ngưỡng chấp nhận (p95/p99 theo baseline nội bộ).
- Check P3-2: Redis QPS tăng trong ngưỡng dự kiến sau stacked rules.
- Check P3-3: local fast-path cache giảm hit Redis cho trạng thái cooldown/block.
- Check P3-4: không có race logic rõ ràng khi concurrent burst.
- Success criteria:
  - hệ thống ổn định khi load tăng,
  - không phát sinh regression nghiêm trọng.

### P4 verification (evolution hooks, deferred)
- Check P4-1: hooks cho cluster pressure/risk integration không phá contract v1.
- Check P4-2: feature flags/deferred toggles hoạt động an toàn khi bật/tắt.
- Success criteria:
  - có đường mở rộng v2/v3 mà không phải rewrite v1.

## Transport layer
- Check 1: route có chain `RateLimitPreAuth(...)` -> `Access(...)` -> `RateLimitPostAuth(...)` vẫn hoạt động đúng HTTP semantics (allow/429/503 path).
- Success criteria: không đổi contract response body generic.

## Security core
- Check 2: `Stacked.Allow` xử lý đúng thứ tự rule và chọn severity cao nhất.
- Check 3: key builders mới tạo key deterministic, không collision bất thường.
- Success criteria: unit tests pass cho rule ordering + key format.

## Observability
- Check 4: metrics/log có đủ key chuẩn như bảng dưới.
- Success criteria: có thể truy ngược một request bị chặn về trigger scope + reason + TTL/retry.

### 9.1 Metrics contract chi tiết (v1)

#### Metric location
- Định nghĩa metric counters/histograms tại `controlplane/internal/http/middleware/ratelimiter.go`.
- `RateLimitPreAuth/RateLimitPostAuth` là caller emit metrics trực tiếp.
- `controlplane/internal/observability/prometheus.go` gọi hàm register của ratelimiter trong bootstrap.
- `internal/security/ratelimit` core package không phụ thuộc Prometheus.

#### Metric names + labels
1. `security_ratelimit_check_total`
   - type: counter
   - labels: `route_pattern`, `rule_scope`, `result`
   - semantics:
     - `result=allow|blocked|error`
2. `security_ratelimit_decision_total`
   - type: counter
   - labels: `route_pattern`, `decision`, `rule_scope`, `route_cost_class`, `route_security_class`
   - semantics:
     - `decision=allow|throttle|cooldown|block`
3. `security_ratelimit_error_total`
   - type: counter
   - labels: `route_pattern`, `error_type`
   - semantics:
     - `error_type=redis_unavailable|lua_parse_error|invalid_rule|unknown`
4. `security_ratelimit_retry_after_seconds`
   - type: histogram (hoặc gauge nếu chưa có histogram infra)
   - labels: `route_pattern`
5. `security_ratelimit_eval_duration_seconds`
   - type: histogram
   - labels: `rule_scope`
6. `security_ratelimit_local_cache_total`
   - type: counter
   - labels: `action`, `rule_scope`
   - semantics:
     - `action=lookup|hit|miss|set|evict_expired|set_drop_at_capacity`

### 9.1.1 PromQL gợi ý (P3 increment 3)

1. **Local cache hit ratio (5m)**

```promql
sum(rate(security_ratelimit_local_cache_total{action="hit"}[5m]))
/
clamp_min(sum(rate(security_ratelimit_local_cache_total{action="lookup"}[5m])), 1)
```

2. **Local cache set drop rate at capacity (5m)**

```promql
sum(rate(security_ratelimit_local_cache_total{action="set_drop_at_capacity"}[5m]))
```

3. **Redis block-path pressure proxy (5m)**

```promql
sum(rate(security_ratelimit_check_total{result="blocked"}[5m]))
-
sum(rate(security_ratelimit_local_cache_total{action="hit"}[5m]))
```

4. **Per-rule-scope cache efficiency (5m)**

```promql
sum by (rule_scope) (rate(security_ratelimit_local_cache_total{action="hit"}[5m]))
/
clamp_min(sum by (rule_scope) (rate(security_ratelimit_local_cache_total{action="lookup"}[5m])), 1)
```

### 9.1.2 Alert rule draft (P3 increment 3)

1. **Alert: Local cache hit ratio quá thấp**
   - Condition (10m):

```promql
(
  sum(rate(security_ratelimit_local_cache_total{action="hit"}[10m]))
  /
  clamp_min(sum(rate(security_ratelimit_local_cache_total{action="lookup"}[10m])), 1)
) < 0.15
```

   - Ý nghĩa: deny-cache ít hiệu quả, Redis offload kém.

2. **Alert: Cache saturation drop tăng cao**
   - Condition (10m):

```promql
sum(rate(security_ratelimit_local_cache_total{action="set_drop_at_capacity"}[10m])) > 5
```

   - Ý nghĩa: local cache thường xuyên chạm cap, có thể cần tăng cap/tối ưu TTL.

3. **Alert: Eviction expired spike bất thường**
   - Condition (10m):

```promql
sum(rate(security_ratelimit_local_cache_total{action="evict_expired"}[10m])) > 200
```

   - Ý nghĩa: churn keys cao, cần review policy retry-after/TTL và pattern abuse.

### 9.2 Log contract chi tiết (v1)

#### Log location
- Security-event log ở `controlplane/internal/http/middleware/ratelimiter.go` (không nhét vào access log mặc định).
- Access log vẫn giữ lean ở `controlplane/internal/http/middleware/accesslog.go`.
- Chỉ log structured fields, không log raw token/secret/raw subject ids.

#### Required log fields
- `request_id`
- `route_pattern`
- `decision`
- `rule_scope`
- `escalation_reason`
- `retry_after_ms`
- `ttl_ms`
- `subject_key_hash`

#### Logging policy / sampling
- `allow`: không ghi security-event log (hoặc sampling rất thấp nếu cần debug tạm thời).
- `throttle`: sampling 5-10%.
- `cooldown`: sampling 50%.
- `block`: 100%.

Mục tiêu: giữ chi phí ingest thấp nhưng vẫn đủ forensic cho hành vi nghiêm trọng.

#### Context keys RateLimitPreAuth/RateLimitPostAuth phải set để observability consume
- `ratelimit.decision`
- `ratelimit.rule_scope`
- `ratelimit.escalation_reason`
- `ratelimit.retry_after_ms`
- `ratelimit.ttl_ms`
- `ratelimit.subject_key_hash`

## Regression
- Check 5: nhóm route `RouteSecurityClass=sensitive|critical` trong implementation hiện tại không vỡ flow functional.
- Success criteria: smoke test login/session/refresh còn chạy.

# 10. Rollback Plan

- Rollback cấp code:
  - revert các thay đổi ở `ratelimiter.go` + `ratelimit/*` theo commit boundary.
- Rollback cấp runtime:
  - tạm set threshold permissive (capacity/refill lớn) trước khi rollback full.
- Rollback cấp deploy:
  - rollout canary 1 slice route trước, có thể disable via config flag nếu có.

# 11. Open Questions

1. Các route nào bắt buộc rollout `RateLimitPostAuth(...)` ở phase 1, route nào defer phase 2?
2. `tracking_device_id` trusted source ở pre-auth được lấy từ đâu trong chain hiện tại (cookie/header/context nào là canonical)?
3. Với route chưa auth (anonymous), có bật `user_id` rule ngay hay defer sau khi có access context ổn định?
4. Mức rule cap/request và window cap/rule chốt v1 là bao nhiêu trong rollout đầu?

## File: `controlplane/internal/app/app.go`
- Ownership layer: app bootstrap/global composition.

### Function block: `engine.Use(...)`
- Current state:
  - middleware chain chưa có pre-auth limiter global.
  - `AccessLog` đang trước `RequestID`.
- Planned action: **update**
  - thêm `RateLimitPreAuth(...)` vào global chain.
  - reorder để `RequestID` đứng trước `AccessLog`.
  - đảm bảo `RateLimitPreAuth` chạy trước `AccessLog`.
- Expected behavior:
  - access log có đủ `ratelimit.*` context fields.
  - bypass endpoints (`, /api/v1/health/liveness`, `, /api/v1/health/readiness`, `/metrics`) không bị rate-limit enforcement.
- Caller/callee impact:
  - không ảnh hưởng business modules; chỉ đổi thứ tự/hook global middleware.

### Verification addendum
- Check transport-health: `, /api/v1/health/liveness`, `, /api/v1/health/readiness`, `/metrics` không bị throttle/cooldown/block.
- Success criteria: health/metrics luôn reachable khi limiter active.

# 12. Plan Closure (Sign-off)

## 12.1 Execution status

- `P0`: DONE
- `P1`: DONE
- `P2`: DONE (scope routes hiện tại)
- `P3`: IN PROGRESS (increment 1 + 2 + 3 đã hoàn thành)
- `P4`: DEFERRED (evolution hooks)

## 12.2 Completion criteria for current release

Plan này được xem là **đóng cho release hiện tại** khi toàn bộ điều kiện sau đạt:
- Middleware contracts đã stable:
  - `RateLimitPreAuth(...)`
  - `RateLimitPostAuth(...)`
- Route wiring auth/admin/me-device đã dùng đúng phase order:
  - pre-auth trước auth guard,
  - post-auth sau auth guard.
- Bypass policy health/metrics hoạt động ổn định:
  - `/metrics`
  - `/api/v1/health/liveness`
  - `/api/v1/health/readiness`
  - `/api/v1/health/startup`
- Observability tối thiểu đã có:
  - decision/check/error/eval/retry metrics,
  - local deny-cache metrics,
  - security-event logs tách khỏi access logs.
- Alert rules anti-probing local cache đã có artifact tại:
  - `controlplane/alerts/rules/anti-probing-ratelimit-v1-alerts.yaml`.

## 12.3 Release gate checklist

- [x] Code compile pass cho packages đã chạm.
- [x] Docs-plan đồng bộ naming `RateLimitPreAuth/RateLimitPostAuth`.
- [x] Alert rules + playbook annotations đã sẵn sàng để nạp Grafana/Alertmanager.
- [x] Rollback path được mô tả rõ ở mục #10.

## 12.4 Follow-up backlog (không chặn đóng plan)

- Tune threshold theo `RouteCostClass/RouteSecurityClass` dựa trên traffic thật.
- Thêm tests cho local cache hit/miss/eviction behavior.
- Chuẩn hóa runbook URLs từ placeholder sang domain nội bộ chính thức.
- Đánh giá scope bật stacked multi-rule evaluator trong pha tiếp theo.
