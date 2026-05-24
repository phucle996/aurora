# Review: Anti-Probing RateLimit Flow (PreAuth/PostAuth)

## 1. Review Scope & Evidence

- **Entrypoint flow reviewed**: HTTP request vào route auth/admin có chain `RateLimitPreAuth(...)` và/hoặc `RateLimitPostAuth(...)`.
- **Terminal boundaries**:
  - Cache local deny-cache trong middleware (`localDenyCache`).
  - Redis token bucket qua `ratelimit.Bucket.Allow(...)`.
  - Metrics registry qua `observability.InitPrometheus(...)`.
- **Files/functions đã review**:
  - `controlplane/internal/http/middleware/ratelimiter.go`
    - `RateLimitPreAuth`
    - `RateLimitPostAuth`
    - `denySubjectCache.getActive`
    - `denySubjectCache.setBlocked`
    - `denySubjectCache.evictExpiredLocked`
    - `emitRateLimitSecurityEvent`
  - `controlplane/internal/http/middleware/access.go`
    - `Access`
    - `GetUserID`
    - `GetRuntimeDeviceID`
  - `controlplane/internal/security/ratelimit/bucket.go`
    - `Bucket.Allow`
    - `SetFailOpen`
  - `controlplane/internal/security/ratelimit/keys.go`
    - `KeyIPDevice`
    - `KeyIPUser`
  - `controlplane/internal/observability/prometheus.go`
    - `RegisterModuleMetrics`
    - `InitPrometheus`
  - `controlplane/internal/app/app.go`
    - global `engine.Use(...)` ordering
  - `controlplane/internal/iam/route.go`
    - route-level middleware wiring cho auth/admin/me-device

## 2. End-to-End Call Graph

1. `app.NewApplication` tạo `ratelimiter := ratelimit.NewBucket(rds)` và `SetFailOpen(false)`.
2. Global chain: `RequestID -> OTel -> PrometheusHTTPMetrics -> ... -> RateLimitPreAuth(global_preauth) -> AccessLog`.
3. Với route có preauth riêng (`iam/route.go`), request đi qua `RateLimitPreAuth(route_specific)`.
4. Với route auth, sau đó vào `Access(...)` để inject `user_id`, `runtime_device_id` vào context.
5. Nếu route có postauth, `RateLimitPostAuth(...)` dùng `ip+device` (ưu tiên) hoặc `ip+user` (fallback).
6. Cả pre/post auth check local deny-cache trước; miss mới gọi Redis qua `Bucket.Allow(...)`.
7. Deny path trả `429` hoặc `503` (khi backend unavailable và not-allowed), emit metrics + security-event log.
8. Metrics được register qua module registrar trong `InitPrometheus(...)`.

## 3. Layer Boundary Findings

### Finding 3.1 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:114`
- **Function**: `RateLimitPreAuth`
- **Reason**: Fail-fast gate đặt đúng transport boundary.
- **Impact**: Bảo vệ tài nguyên trước khi vào auth/business logic.
- **Evidence**: Middleware chặn sớm trước `Access`/handler.
- **Confidence**: High

### Finding 3.2 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:206`
- **Function**: `RateLimitPostAuth`
- **Reason**: Identity-aware enforcement tách riêng khỏi business/service.
- **Impact**: Boundary rõ giữa anti-abuse transport layer và domain logic.
- **Evidence**: Chỉ dùng context identity (`GetRuntimeDeviceID`, `GetUserID`), không gọi service/repo.
- **Confidence**: High

## 4. Ownership & Source of Truth Findings

### Finding 4.1 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:41`
- **Function**: package-level `localDenyCache`
- **Reason**: Local cache là fast-path optimization, không phải source of truth enforcement.
- **Impact**: SoT vẫn là Redis token-bucket state.
- **Evidence**: Cache chỉ hold deny TTL ngắn; miss thì gọi `Bucket.Allow`.
- **Confidence**: High

### Finding 4.2 (Risk)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:41`
- **Function**: package-level `localDenyCache`
- **Reason**: Cache global process-level, không tách namespace theo limiter name/route ngoài subject key; dễ tạo implicit coupling khi naming key không nhất quán giữa routes.
- **Impact**: Có thể gây bleed hiệu ứng deny nếu convention `name` bị reuse sai.
- **Evidence**: Key tạo từ `ratelimit.Key(prefix=name, ...)`; cache map dùng raw subject key string.
- **Confidence**: Medium

## 5. Duplicate Logic / Duplicate Data Findings

### Finding 5.1 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:114`, `:206`
- **Function**: `RateLimitPreAuth`, `RateLimitPostAuth`
- **Reason**: Hai hàm lặp block logic tương tự (cache check, allow call, metric/log deny).
- **Impact**: Tăng maintenance cost; drift behavior giữa pre/post có thể xảy ra về sau.
- **Evidence**: Cấu trúc xử lý gần như tương đồng, khác rule-scope/key build.
- **Confidence**: High

## 6. Implicit Behavior & Hidden Contract Findings

### Finding 6.1 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:422`
- **Function**: `retryAfterFromRate`
- **Reason**: Fallback Retry-After mặc định `2s` là contract ngầm.
- **Impact**: Ảnh hưởng trực tiếp client backoff behavior dù không route-specific.
- **Evidence**: Nếu `res.RetryAfter <= 0` thì hardcode `2 * time.Second`.
- **Confidence**: High

### Finding 6.2 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:394`
- **Function**: `shouldBypassRateLimit`
- **Reason**: Bypass list hardcoded trong code.
- **Impact**: Thay đổi ops endpoint cần patch/redeploy, không thể tune runtime.
- **Evidence**: switch literal cho `/metrics` và health endpoints.
- **Confidence**: High

## 7. Bottleneck & Performance Findings

### Finding 7.1 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:352-360`
- **Function**: `denySubjectCache.setBlocked`
- **Reason**: Khi cache full, eviction scan toàn map (`O(n)`) dưới lock.
- **Impact**: Dưới abuse burst + near-capacity có thể tăng lock contention.
- **Evidence**: `evictExpiredLocked` iterate toàn bộ `entries`.
- **Confidence**: High

### Finding 7.2 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:432`
- **Function**: `emitRateLimitSecurityEvent`
- **Reason**: Log emit ở mọi deny path, chưa thấy sampling implemented theo doc policy.
- **Impact**: Ingestion cost tăng khi blocked burst cao.
- **Evidence**: `logger.L().WithFields(...).Warn(...)` luôn gọi trong deny branches.
- **Confidence**: High

## 8. Consistency Model Findings

### Finding 8.1 (Confirmed)
- **Location**: `controlplane/internal/security/ratelimit/bucket.go:86`
- **Function**: `Bucket.Allow`
- **Reason**: Redis Lua dùng atomic update/read cho từng key -> strong per-key consistency tại Redis primary.
- **Impact**: Quyết định per-subject nhất quán trong node/cluster (phụ thuộc Redis availability/latency).
- **Evidence**: `tokenBucketScript.Run(...)` parse result per request.
- **Confidence**: High

### Finding 8.2 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:298`
- **Function**: `denySubjectCache.getActive`
- **Reason**: Local cache tạo eventual behavior cross-instance (mỗi node cache riêng).
- **Impact**: Hai request cùng subject ở node khác nhau có thể nhận decision timing khác nhau ngắn hạn.
- **Evidence**: cache in-memory process-local, không sync cluster.
- **Confidence**: High

## 9. Cache / Consistency / Staleness Findings

### Finding 9.1 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:335`
- **Function**: `denySubjectCache.setBlocked`
- **Reason**: TTL bounded (`<=1m`) giảm stale window.
- **Impact**: Giảm nguy cơ deny kéo dài do stale local state.
- **Evidence**: retryAfter cap về `time.Minute`.
- **Confidence**: High

### Finding 9.2 (Risk)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:306-321`
- **Function**: `denySubjectCache.getActive`
- **Reason**: Miss metrics có thể inflate (miss ghi cả khi key absent và khi expired).
- **Impact**: Hit ratio interpretation cần hiểu semantics “hard miss + expired miss”.
- **Evidence**: miss increment ở nhiều nhánh.
- **Confidence**: Medium

## 10. Consistency & Partition Tolerance Assessment

- **Partition behavior**:
  - Redis unreachable + `failOpen=false` (app setup) => deny path có thể tăng (service unavailable/blocked) tùy branch.
  - Local deny-cache vẫn hoạt động cho subject đã cached.
- **Assessment**:
  - Hệ thống đang ưu tiên **protective consistency** hơn availability trong Redis outage cho paths cần limiter.
  - Đây phù hợp control-plane security nhưng có risk availability khi dependency incident.

## 11. Reliability Target Assessment

- **Inferred target**: bảo vệ auth surfaces khỏi burst/abuse mà không gây access-log bloat.
- **Status**: **AT_RISK**.
- **Reason**:
  1. Có local fast-path và metrics tốt (điểm cộng).
  2. Nhưng chưa có sampling trong security-event logs (cost risk).
  3. Dual preauth (global + route-level preauth) có thể làm double-throttle khó tune.

## 12. Degradation & Failure Cascade Findings

### Finding 12.1 (Confirmed)
- **Location**: `controlplane/internal/app/app.go:134`, `controlplane/internal/iam/route.go:17+`
- **Function**: global `engine.Use(...)`, `RegisterRoutes`
- **Reason**: Global preauth + route preauth cùng tồn tại trên nhiều route.
- **Impact**: Một request có thể chịu 2 lần preauth token-bucket check -> tăng reject probability và Redis calls.
- **Evidence**: app global `RateLimitPreAuth(global_preauth)` + route-level `RateLimitPreAuth(iam_...)`.
- **Confidence**: High

### Finding 12.2 (Hypothesis)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:432`
- **Function**: `emitRateLimitSecurityEvent`
- **Reason**: Khi blocked storm, sync logging có thể góp phần CPU/IO pressure.
- **Impact**: tail latency tăng ở hot path.
- **Evidence**: deny path luôn log warn; chưa thấy async/sampling guard.
- **Confidence**: Medium

## 13. RFC / ADR Conformance Assessment

- **So với anti-probing plan/spec đã viết**:
  - Conform:
    - tách `RateLimitPreAuth` / `RateLimitPostAuth`.
    - bypass health/metrics.
    - local cache bounded + anti-poisoning.
    - metric ownership nằm ở middleware, observability chỉ register.
  - Drift:
    - spec/plan nói decision ladder `allow/throttle/cooldown/block`, code hiện tại thực thi thực tế mới `allow/throttle`.
    - logging policy sampling theo decision chưa implement đủ như doc.

## 14. Operational Risk Findings

### Finding 14.1 (Confirmed)
- **Location**: `controlplane/internal/http/middleware/ratelimiter.go:76`
- **Function**: metric declarations
- **Reason**: Không có metric trực tiếp cho “double preauth chain overlap” để phát hiện misconfiguration nhanh.
- **Impact**: Điều tra reject anomaly sẽ khó hơn khi policy chồng nhau.
- **Evidence**: metrics hiện theo rule_scope/result/action, không có stage/global-vs-route tag.
- **Confidence**: Medium

### Finding 14.2 (Confirmed)
- **Location**: `controlplane/alerts/rules/anti-probing-ratelimit-v1-alerts.yaml:1`
- **Function**: alert rules artifact
- **Reason**: Alert đã có cho cache efficiency/pressure nhưng chưa thấy alert cho deny/error path ratio.
- **Impact**: Có thể bỏ sót incident “backend_unavailable” tăng mạnh.
- **Evidence**: rules tập trung local_cache metric.
- **Confidence**: High

## 15. Risk Ranking (P0/P1/P2)

- **P0**
  1. Double preauth enforcement (global + route-level) có thể gây over-throttle và tăng hot-path Redis load.
- **P1**
  1. Security-event log chưa sampling theo policy -> cost/latency risk khi burst.
  2. O(n) eviction under lock khi cache cap.
- **P2**
  1. Hardcoded bypass list khó vận hành runtime.
  2. Cache miss semantics cần giải thích rõ khi làm dashboard.

## 16. Optimization Recommendations

1. **Unify preauth stage ownership**
   - **Priority**: P0
   - **Effort**: Medium
   - **Risk Reduction**: High
   - **Expected Gain**: Giảm double-throttle + giảm Redis calls.
   - **Impacted**: `internal/app/app.go:127`, `internal/iam/route.go:17-101`

2. **Implement deny-log sampling gates theo decision**
   - **Priority**: P1
   - **Effort**: Low-Medium
   - **Risk Reduction**: High
   - **Expected Gain**: Giảm logging cost trong burst, giữ forensic signal.
   - **Impacted**: `internal/http/middleware/ratelimiter.go:432`

3. **Add bounded incremental eviction strategy**
   - **Priority**: P1
   - **Effort**: Medium
   - **Risk Reduction**: Medium
   - **Expected Gain**: Giảm lock hold-time khi cache full.
   - **Impacted**: `internal/http/middleware/ratelimiter.go:368`

4. **Add alert on ratelimit backend error surge**
   - **Priority**: P1
   - **Effort**: Low
   - **Risk Reduction**: Medium
   - **Expected Gain**: Phát hiện sớm Redis/limiter degradation.
   - **Impacted**: `alerts/rules/anti-probing-ratelimit-v1-alerts.yaml`, metric `security_ratelimit_error_total`

5. **Config-driven bypass list**
   - **Priority**: P2
   - **Effort**: Medium
   - **Risk Reduction**: Medium
   - **Expected Gain**: Ops agility khi thêm/chuyển health endpoints.
   - **Impacted**: `internal/http/middleware/ratelimiter.go:394`, `internal/config/*`

## 17. Verification Plan

1. **PreAuth overlap verification**
   - Route sample `POST /api/v1/auth/login`: đo số lần increment `security_ratelimit_check_total` theo request.
   - Kỳ vọng sau chỉnh ownership: 1 preauth stage/request (trừ route explicitly dual-stage theo design).

2. **Log sampling verification**
   - Generate blocked burst, so sánh deny count vs emitted security logs.
   - Kỳ vọng đúng ratio policy (5/50/100 hoặc policy mới).

3. **Cache pressure verification**
   - Stress test near `rateLimitDenyCacheMaxKeys`.
   - Theo dõi `set_drop_at_capacity`, `evict_expired`, latency middleware.

4. **Backend degradation verification**
   - Mô phỏng Redis timeout/unavailable.
   - Theo dõi `security_ratelimit_error_total{error_type="backend_unavailable"}` và HTTP outcome consistency.

## 18. Open Questions

1. Global preauth có chủ đích luôn-on cho mọi route, hay chỉ intended cho non-auth surfaces?
2. Có chấp nhận bỏ route-level preauth ở nhóm đã có global preauth để tránh double enforcement không?
3. Policy sampling security-event log sẽ đặt ở config runtime hay hardcoded theo decision?
4. Khi Redis unavailable, availability target ưu tiên deny-protect hay fail-open selective theo route class?
