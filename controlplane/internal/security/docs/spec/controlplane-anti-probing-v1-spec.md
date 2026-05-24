# Controlplane Anti-Probing (RateLimit) v1 Spec

## Implementation Status

- `CURRENT_CODE`:
  - Middleware hiện tại còn single-phase (`RateLimit`) và chủ yếu IP-based.
  - Chưa có `RateLimitPreAuth` / `RateLimitPostAuth` tách phase trong code.
- `NEXT_INCREMENT (P1/P2)`:
  - P1: triển khai `RateLimitPreAuth` global + bypass health/metrics + metrics registration ownership tại ratelimiter.
  - P2: triển khai `RateLimitPostAuth` sau `Access(...)` cho route cần auth.
- `FUTURE_EVOLUTION (P3+)`:
  - performance hardening, cache discipline nâng cao, integration hooks với risk-engine/circuit-breaker.

## 1) Mục tiêu

Thiết kế anti-probing v1 tập trung vào **RateLimit đa chiều, linh hoạt cho NAT**:
- Giữ middleware per-route và tách rõ pre-auth/post-auth.
- Nâng `RateLimit` để xử lý theo nhiều chiều subject thay vì IP-only.
- Áp dụng progressive enforcement: `allow -> throttle -> cooldown -> block`.

Mục tiêu vận hành gồm 2 nhánh độc lập nhưng phối hợp:
- **Security objective**: giảm DDoS/recon abuse.
- **Capacity objective**: bảo vệ tài nguyên server, tránh quá tải dây chuyền.

> Ghi chú phạm vi: Spec này không bao gồm risk score engine đầy đủ; phần đó ở spec riêng `controlplane-risk-engine-v1-spec.md`.

## 2) Phạm vi

### Trong phạm vi
- `internal/security/ratelimit`: key builders, stacked evaluator, headers.
- `internal/http/middleware/ratelimiter.go`: mở rộng `RateLimit` hiện tại (không tạo func mới).
- Route-level rollout cho nhóm security-sensitive routes trong implementation hiện tại.
- Metrics/log cho rule scope và decision.

### Ngoài phạm vi
- Không thay đổi IAM business logic.
- Không thêm WAF/IDS ngoài hệ thống.
- Không đổi API middleware route registration hiện tại.

## 3) Kiến trúc logic

### 3.0 Dual-objective model

Rate limit v1 chạy theo 2 lớp:
1. **Admission limiter (capacity-first, pre-auth)**
   - Chặn theo `ip` + `route_pattern` + in-flight guard **instance-level**; cluster-level dùng pressure signal thích ứng.
   - Mục tiêu: reject sớm khi hệ thống quá tải, giảm tốn CPU/DB/Redis trong môi trường HA/CCU cao.
2. **Abuse limiter (security-first, identity-aware)**
   - Chặn theo `ip+tracking_device_id` và `user_id` khi có context.
   - Mục tiêu: giảm false positive NAT và chặn đúng đối tượng abuse.

Hai lớp cùng trả về decision thống nhất (`allow/throttle/cooldown/block`) để observability đồng bộ.

### 3.0.1 Runtime flow

1. Request đi qua global middleware `RateLimitPreAuth(...)` tại app bootstrap.
2. Middleware lấy context tối thiểu: `ip`, `route_pattern`.
3. Admission limiter chạy trước (`ip`, `route_pattern`, inflight pressure).
4. Qua `Access(...)`, context được enrich: `tracking_device_id` (trusted runtime id), `user_id` (nếu có).
5. `RateLimitPostAuth(...)` chạy abuse limiter với stacked identity rules.
6. Evaluate **tất cả** rule áp dụng và aggregate decision theo severity cao nhất.
7. Trả response + headers + audit metrics/log.

Thứ tự middleware khuyến nghị ở app bootstrap (prod):
- `RequestID` -> `OTelTraceContext` -> `RateLimitPreAuth` -> `AccessLog` -> middleware còn lại.
- Mục tiêu: `AccessLog` nhìn thấy đủ decision fields từ pre-auth limiter.

Bypass policy bắt buộc cho health/ops endpoints:
- `/api/v1/health/liveness`
- `/api/v1/health/readiness`
- `/api/v1/health/startup`
- `/metrics`

Bypass semantics:
- Không chạy enforcement `throttle/cooldown/block` cho các route trên.
- Vẫn có thể ghi signal/metric nhẹ để quan sát traffic bất thường nếu cần.

Ghi chú bắt buộc:
- `route_pattern` là route đã normalize (ví dụ `/api/v1/users/:id`), không dùng raw URL path.

### 3.1 Thành phần
- `ratelimit`:
  - Key builders theo scope: `ip`, `route_pattern`, `ip+device`, `user`.
  - `Stacked` evaluator.
  - Atomic Redis evaluator (Lua).
  - Header helpers.
- `middleware.RateLimitPreAuth`:
  - Thu input runtime.
  - Build/evaluate admission rules từ baseline params.
- `middleware.RateLimitPostAuth`:
  - Đọc context đã enrich từ `Access(...)`.
  - Build/evaluate identity-aware rules và map action/response.

### 3.2 Subject identity (trust model)
- Ưu tiên enforcement: `ip+tracking_device_id`.
- `tracking_device_id` phải là **server-issued, tamper-evident** (HMAC/signed opaque id).
- Không chấp nhận raw client-generated UUID/header làm identity chính.
- `user_id` là dimension bổ sung khi đã auth.
- Không dùng DB device primary key.
- Nếu chưa xác minh được `tracking_device_id` theo trust model thì không được dùng làm identity rule.

## 4) Contract kỹ thuật

### 4.1 Runtime input

```go
type RuntimeInput struct {
    RoutePattern      string
    IP                string
    TrackingDeviceID  string
    UserID            string
    RequestCost       int
    InflightInstance  int
    InflightLimit     int
    ClusterPressure   float64
}
```

### 4.2 Middleware signatures

Đổi sang tách explicit 2 middleware:

```go
func RateLimitPreAuth(
    limiter *ratelimit.Bucket,
    name string,
    capacity, refill int64,
    period time.Duration,
) gin.HandlerFunc

func RateLimitPostAuth(
    limiter *ratelimit.Bucket,
    name string,
    capacity, refill int64,
    period time.Duration,
) gin.HandlerFunc
```

`name/capacity/refill/period` là baseline để build stacked rules tương ứng từng phase.

### 4.3 Rule stack contract

Rule order cố định:
1. `ip` (admission guard, threshold cao)
2. `route_pattern` (endpoint burst + admission guard)
3. `ip+tracking_device_id` (abuse guard chính)
4. `user_id` (optional, abuse guard bổ sung)

Nguyên tắc:
- Thiếu dimension thì skip rule tương ứng.
- **Không dùng first-fail để ra quyết định**.
- Evaluate toàn bộ rules áp dụng và lấy decision severity cao nhất.

### 4.4 Decision contract

```go
type Decision string

const (
    DecisionAllow    Decision = "allow"
    DecisionThrottle Decision = "throttle"
    DecisionCooldown Decision = "cooldown"
    DecisionBlock    Decision = "block"
)

type FinalDecision struct {
    Decision         Decision
    TriggerRuleScope string
    EscalationReason string
    RetryAfter       time.Duration
    TTL              time.Duration
}
```

Semantics:
- `allow`: request qua bình thường.
- `throttle`: `429`, `Retry-After = 2s` (dải tune `1-3s`).
- `cooldown`: `429/403`, `TTL = 60s` + jitter ±15%.
- `block`: `429/403`, `TTL = 900s` + jitter ±10%.

Contract:
- Mọi request bị chặn/throttle phải sinh `FinalDecision` để phục vụ forensic, tuning và observability.

### 4.5 Policy contracts

1. **Admission policy**
   - Input: `ip`, `route_pattern`, `inflight_instance`, `cluster_pressure`, `request_cost`.
   - Rule scopes: `ip`, `route`, `inflight_instance`, `cluster_pressure`.
   - Admission layer giữ lightweight: ưu tiên throttle ngắn; cooldown/block chỉ khi pressure kéo dài.

2. **Abuse policy**
   - Input: `ip`, `route_pattern`, `tracking_device_id`, `user_id`.
   - Rule scopes: `ip_device`, `user`, kết hợp baseline `ip/route`.

Scale principle:
- Không dùng hard `inflight_cluster_limit` tĩnh.
- Dùng `cluster_pressure` adaptive theo healthy instances + real load.

### 4.6 Decision matrix (v1 baseline)

| Điều kiện | Decision | Response | TTL/Retry |
|---|---|---|---|
| Không rule nào fail | `allow` | pass `c.Next()` | none |
| Sai URL đơn lẻ (404 thường), fail nhẹ lần đầu | `allow` (ghi signal) | pass | none |
| Fail nhẹ lặp lại (`ip/route` >=3 trong 10s) | `throttle` | `429` | `Retry-After=2s` |
| Repeated throttle (>=5 trong 60s) cùng subject | `cooldown` | `429` hoặc `403` | `TTL=60s ±15%` |
| Cooldown lặp lại nghiêm trọng (>=3 trong 10m) hoặc block key active | `block` | `429` hoặc `403` | `TTL=900s ±10%` |
| `inflight_instance` cao hoặc `cluster_pressure` kéo dài | tối thiểu `throttle`, có thể `cooldown` | `429` | retry/ttl theo pressure |

Quy tắc chọn decision cuối:
- Chọn severity cao nhất: `block > cooldown > throttle > allow`.
- `ip` là coarse guard; có identity thì ưu tiên `ip_device/user`.
- 404 thường không auto escalate trừ sensitive pattern hoặc mật độ bất thường.

### 4.7 Atomic evaluation requirement

- Multi-rule evaluate + counter update + decision compute phải chạy atomic bằng Redis Lua.
- Tránh chuỗi GET/INCR/EXPIRE rời rạc gây race condition.
- Distributed TTL/window evaluation MUST dùng Redis server time làm canonical clock source.
- Lua evaluation MUST bounded complexity:
  - Max evaluated rules/request: `6`
  - Max windows/rule: `3`
  - Cấm vòng lặp không giới hạn theo input runtime.

### 4.7.1 Sensitive path canonicalization

- Sensitive path detection MUST evaluate trên:
  1. normalized path,
  2. decoded path,
  3. lowercase canonical form.
- Mục tiêu: tránh bypass bằng URL-encoding, mixed-case, normalize traversal.

### 4.8 Local fast-path cache

- Bắt buộc có local hot cache ngắn hạn:
  - active block cache,
  - short negative cache,
  - tiny LRU/TinyLFU.
- Mục tiêu: giảm Redis QPS/RTT cho rule nóng.
- Cache key phải dùng hash của subject key; không lưu raw token/secret vào cache key/value.
- Anti-poisoning rules:
  - Chỉ subject đang ở trạng thái `cooldown/block` mới được đưa vào local cache.
  - Cache bắt buộc giới hạn kích thước và TTL.

### 4.9 Route classification contract (2 dimensions)

```go
type RouteCostClass string

const (
    RouteCostLight   RouteCostClass = "light"
    RouteCostMedium  RouteCostClass = "medium"
    RouteCostHeavy   RouteCostClass = "heavy"
    RouteCostExtreme RouteCostClass = "extreme"
)

type RouteSecurityClass string

const (
    RouteSecurityPublic    RouteSecurityClass = "public"
    RouteSecuritySensitive RouteSecurityClass = "sensitive"
    RouteSecurityCritical  RouteSecurityClass = "critical"
)
```

- Mọi route gắn `RateLimitPreAuth(...)` / `RateLimitPostAuth(...)` SHOULD map vào cả 2 chiều:
  - `RouteCostClass`: phản ánh resource pressure / computational cost.
  - `RouteSecurityClass`: phản ánh abuse sensitivity / privilege impact.
- Hai chiều này là đầu vào cho metrics, emergency policy và adaptive thresholding.

Ví dụ mapping:

| Route | CostClass | SecurityClass |
|---|---|---|
| `, /api/v1/health/liveness` | `light` | `public` |
| `/auth/login` | `heavy` | `sensitive` |
| `/auth/refresh` | `medium` | `sensitive` |
| `/admin/rotate-key` | `medium` | `critical` |
| `/search` | `heavy` | `public` |

Áp dụng:
- Admission engine: route `heavy/extreme` tiêu tốn inflight budget cao hơn (`RequestCost` cao hơn).
- Security enforcement: route `critical` được fail-closed sớm hơn và escalation nhanh hơn.

## 5) Failure mode

- Không fail-closed cứng một kiểu cho mọi lớp.
- Hành vi khi Redis lỗi:
  - Admission: fail-open degraded + emergency pressure guard local.
  - Abuse: fail-closed selective cho route critical; route thường degrade theo policy.
  - Active block: dùng local cache fallback TTL ngắn.

## 6) Observability

### 6.1 Metrics bắt buộc
- `security_ratelimit_check_total{route_pattern,rule_scope,result}`
- `security_ratelimit_decision_total{route_pattern,decision,rule_scope}`
- `security_ratelimit_error_total{route_pattern,error_type}`
- `security_ratelimit_retry_after_seconds{route_pattern}`
- `security_admission_inflight_current{route_cost_class,route_security_class}`
- `security_admission_reject_total{route_cost_class,route_security_class,reason}`
- `security_ratelimit_eval_duration_seconds{rule_scope}`

### 6.2 Log fields bắt buộc
- `request_id`
- `route_pattern`
- `rule_scope`
- `decision`
- `escalation_reason`
- `retry_after_ms`
- `ttl_ms`
- `subject_key_hash`

## 7) Rollout

### Phase 1
- Áp dụng cho nhóm `RouteSecurityClass=sensitive|critical` trong implementation hiện tại.

### Phase 2
- Mở rộng cho toàn bộ route mutation/privileged surfaces.

### 7.1 Example flow: `GET /api/v1/session` (genericized pattern)

Giả sử chain middleware của route:
1. `RateLimitPreAuth(...)`
2. `Access(...)`
3. `RateLimitPostAuth(...)`
4. `RequireUserDeviceRuntime(...)`
5. `AuthHandler.Session`

#### Case A: request hợp lệ
- Input ban đầu tại `RateLimit`:
  - `ip=203.0.113.10`
  - `route_pattern=/api/v1/session`
  - chưa có `tracking_device_id`, `user_id`
- Admission rules (`ip`, `route_pattern`, inflight) đều pass -> decision tạm: `allow`.
- Qua `Access`, token hợp lệ -> enrich context:
  - `user_id=u_123`
  - `tracking_device_id=td_abc`
- Abuse rules (`ip+tracking_device_id`, `user_id`) không fail -> decision cuối: `allow`.
- Kết quả: `200`, emit metrics `security_ratelimit_check_total{result="allow"}`.

#### Case B: cùng IP NAT nhưng 1 device spam
- Device A spam `/api/v1/session`, device B/C cùng IP dùng bình thường.
- `ip` rule có thể chỉ `throttle` nhẹ (coarse guard), chưa block toàn IP.
- `ip+tracking_device_id=203.0.113.10:td_A` fail lặp lại -> escalation lên `cooldown`.
- Device B/C với `td_B`, `td_C` không fail identity rule -> vẫn `allow`.
- Kết quả: giảm block oan theo NAT, chặn đúng device gây nhiễu.

#### Case C: login state lỗi, request anonymous burst
- Chưa có token hợp lệ nên chưa enrich được `user_id`/`tracking_device_id` trusted.
- Chỉ dùng admission rules (`ip`, `route_pattern`, inflight).
- Burst lặp lại theo matrix -> `throttle` rồi `cooldown` nếu tiếp diễn.
- Kết quả: vẫn bảo vệ được server dù chưa có identity context.

#### Case D: health/metrics bypass
- Request vào `, /api/v1/health/liveness` hoặc `, /api/v1/health/readiness` hoặc `/metrics`.
- `RateLimitPreAuth` nhận diện bypass policy và bỏ qua enforcement.
- Request đi tiếp để giữ khả năng health check/observability trong incident.

#### Audit fields mẫu cho Case B
- `route_pattern=/api/v1/session`
- `rule_scope=ip_device`
- `decision=cooldown`
- `escalation_reason=repeated_throttle`
- `ttl_ms=60000`
- `subject_key_hash=<hashed:203.0.113.10:td_A>`

## 8) Tiêu chí chấp nhận

- `RateLimit` hiện tại chạy được stacked multi-dimensional.
- Trong NAT, enforcement chuyển nhiều sang `ip+device/user`, giảm block oan theo IP.
- Atomic Redis evaluation hoạt động ổn định dưới concurrency cao.
- Redis sự cố không kéo sập toàn bộ control plane auth surfaces.

## 9) Rủi ro và giảm thiểu

- False positive do threshold chặt:
  - tune theo rule scope, rollout dần.
- Redis pressure tăng:
  - atomic Lua + local cache + TTL discipline.
- Retry storm khi TTL release đồng loạt:
  - áp jitter TTL.

## 10) Open questions

- Baseline threshold theo scope nên chốt theo số liệu prod nào?
- Route critical nào cần abuse fail-closed selective khi Redis lỗi?
- Mặc định `cooldown/block` dùng toàn `429` để tránh leak, hay cho route-specific `403`?
