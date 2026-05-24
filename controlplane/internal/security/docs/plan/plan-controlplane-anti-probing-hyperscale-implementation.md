# 1. Bối cảnh và mục tiêu thay đổi

Dựa trên `controlplane/internal/security/docs/spec/controlplane-anti-probing-hyperscale-evolution-v1.md`, cần triển khai track tăng cường bảo vệ controlplane theo phạm vi **single-region, multi-instance** hiện tại, không đợi multi-region.

Mục tiêu chính:
- tăng khả năng chống abuse/retry storm trong traffic cao,
- giảm risk over-throttle do policy chồng chéo,
- tăng alertability cho degradation của rate-limit backend,
- giữ UX/cost-first enforcement (`throttle -> temporary isolation -> block`) ở mức implementable.

Mục tiêu release này không phải xây full hyperscale stack, mà là tạo nền runtime vững để scale-up dần theo phase.

# 2. Phạm vi

## Trong phạm vi
- Chuẩn hóa ownership:
  - `RateLimitPreAuth` global-only,
  - `RateLimitPostAuth` per-route sau auth guard.
- Nâng chất lượng admission runtime:
  - local deny-cache bounded + eviction bounded scan,
  - security-event sampling theo decision/error.
- Bổ sung enforcement state tối thiểu cho `temporary_isolation` + `block` theo TTL policy nội bộ middleware/ratelimit scope.
- Bổ sung observability/alerts cho:
  - backend_unavailable surge,
  - deny/error ratio,
  - local cache pressure/hit ratio.
- Đồng bộ docs/spec/plan theo runtime thực tế.

## Ngoài phạm vi
- Không triển khai policy hot-reload toàn app trong đợt này.
- Không triển khai multi-region replication.
- Không triển khai probabilistic detection primitives (CMS/Bloom/HLL).
- Không thay đổi business logic IAM service/repo/DB.

### Phase roadmap
- **P0**: ổn định wiring + contract (global preauth/per-route postauth).
- **P1**: hardening deny path (sampling, bounded cache behavior).
- **P2**: implement `temporary_isolation` + `block` state contract ở runtime hiện tại.
- **P3**: observability/alert completion + docs conformance lock.

# 3. Pre-Change Log

## CURRENT_CODE snapshot
- `RateLimitPreAuth` đang global ở `internal/app/app.go`.
- `RateLimitPostAuth` đã gắn cho nhóm auth/admin/me-device routes trong `internal/iam/route.go`.
- Local deny-cache đã có cap + TTL + metrics cơ bản trong `internal/http/middleware/ratelimiter.go`.
- Alert rules đã có cache efficiency/pressure + backend degradation cơ bản trong `controlplane/alerts/rules/anti-probing-ratelimit-v1-alerts.yaml`.
- Runtime decision hiện tại chủ yếu `allow/throttle` (chưa có state engine rõ cho `temporary_isolation/block` theo spec mới).

## Known gaps cần fix
- Chưa có runtime contract rõ cho `temporary_isolation` / `block`.
- Eviction path local cache vẫn có full-scan branch khi cap pressure cao.
- Sampling policy mới ở mức cứng; chưa chuẩn hóa contract theo decision classes.
- Docs/spec dễ drift nếu không khóa acceptance criteria theo code hiện tại.

## Docs-code mismatch hiện tại
- Spec hyperscale đã nêu ladder mới, nhưng code chưa đủ state transitions tương ứng.
- Plan fix cũ có phần labels/metric đã defer; cần khóa lại tránh hiểu nhầm “must-do now”.

# 4. Naming Plan

## Public symbols giữ nguyên
- `func RateLimitPreAuth(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc`
- `func RateLimitPostAuth(limiter *ratelimit.Bucket, name string, capacity, refill int64, period time.Duration) gin.HandlerFunc`

## New/updated naming (runtime contract)
1. Decision levels:
- `allow`
- `throttle`
- `temporary_isolation`
- `block`

2. Escalation reasons (log/metric fields):
- `capacity_exceeded`
- `abuse_exceeded`
- `local_deny_cache`
- `backend_unavailable`
- `isolation_active`
- `block_active`

3. Local deny cache actions:
- giữ: `lookup|hit|set|evict_expired|set_drop_at_capacity`
- chuẩn hóa miss actions: `miss_absent|miss_expired`

4. Compatibility notes
- Không đổi signature middleware public.
- Response body tiếp tục generic.
- Header/metric bổ sung theo additive strategy; không xóa metric cũ trong cùng release.

# 5. File-Scoped Action Plan (gộp file + function)

## File: `controlplane/internal/app/app.go`
- Ownership layer: app bootstrap/global composition.

### Function block: `engine.Use(...)`
- Current state: có global preauth.
- Planned action: **update nhẹ**
  - giữ global preauth cố định.
  - verify ordering không đổi: `RequestID -> ... -> RateLimitPreAuth -> AccessLog`.
- Expected behavior: fail-fast admission trước business/auth chain.
- Caller/callee impact: route-level preauth không tái xuất hiện.

## File: `controlplane/internal/iam/route.go`
- Ownership layer: transport wiring.

### Function: `RegisterRoutes(...)`
- Current state: đã bỏ route-level preauth, giữ postauth ở routes auth-sensitive.
- Planned action: **update**
  - chuẩn hóa toàn bộ auth-sensitive routes cần postauth theo matrix route/security class nội bộ.
  - không thêm lại preauth per-route.
- Expected behavior: postauth coverage đủ mà không chồng preauth.
- Caller/callee impact: không đổi handler/service.

## File: `controlplane/internal/http/middleware/ratelimiter.go`
- Ownership layer: transport admission/security gate + observability emitter.

### Function: `RateLimitPreAuth(...)`
- Current state: allow/throttle + deny-cache fast path.
- Planned action: **update**
  - thêm branch `temporary_isolation` và `block` dựa trên local state keys/TTL contract.
  - enforce escalation ordering `throttle -> temporary_isolation -> block`.
- Expected behavior: có enforcement ladder implementable ngay, không cần challenge flow.
- Caller/callee impact: caller không đổi, nhưng decision/log fields mở rộng.

### Function: `RateLimitPostAuth(...)`
- Current state: identity-aware allow/throttle.
- Planned action: **update**
  - áp cùng escalation contract như preauth với identity key priority.
  - đảm bảo `ip+device -> ip+user` không đổi.
- Expected behavior: giảm abuse lặp lại theo subject có context.
- Caller/callee impact: route behavior có thể tăng deny severity theo TTL active state.

### Function: `denySubjectCache.getActive(...)`
- Current state: hit/miss cơ bản, miss semantics chưa tách rõ.
- Planned action: **update**
  - tách `miss_absent` và `miss_expired`.
  - giữ contract return `remainingTTL` + `blocked`.
- Expected behavior: dashboard interpret chính xác cache quality.
- Caller/callee impact: alert/query cập nhật action labels.

### Function: `denySubjectCache.setBlocked(...)` + `evictExpiredLocked(...)`
- Current state: eviction scan full map khi cap.
- Planned action: **update**
  - bounded-eviction step mỗi lần set (max scan batch/attempt).
  - giữ cap cứng, drop-at-capacity metric.
- Expected behavior: giảm lock contention under pressure.
- Caller/callee impact: không đổi API caller.

### Function: `emitRateLimitSecurityEvent(...)`
- Current state: đã có sampling gate cơ bản.
- Planned action: **update**
  - map sampling theo decision class mở rộng (`throttle/isolation/block/error`).
  - thêm field `decision_level` ổn định cho forensic.
- Expected behavior: log cost thấp hơn nhưng đủ điều tra.
- Caller/callee impact: queries log cần đọc field mới.

### Function: `RegisterRateLimitMetrics(...)`
- Current state: register metrics hiện có.
- Planned action: **update nhẹ**
  - add metrics mới cho isolation/block transitions (counter).
  - không thay đổi/không xóa metric đã phát hành.
- Expected behavior: backward-compatible observability.
- Caller/callee impact: dashboard mở rộng thêm panel, panel cũ vẫn chạy.

## File: `controlplane/internal/security/ratelimit/keys.go`
- Ownership layer: security core key-builder.

### Function: `KeyIPDevice(...)`, `KeyIPUser(...)`
- Current state: đã có composite keys.
- Planned action: **update nhẹ**
  - chuẩn hóa prefix/scope cho keys phục vụ isolation/block state (nếu cần key namespace phụ).
- Expected behavior: key collision risk thấp và dễ forensic.
- Caller/callee impact: middleware dùng key builder thống nhất, không concat thủ công.

## File: `controlplane/alerts/rules/anti-probing-ratelimit-v1-alerts.yaml`
- Ownership layer: operations/alerting.

### Rule set
- Current state: có cache alerts + backend degradation alerts.
- Planned action: **update**
  - thêm alerts cho isolation/block abnormal surge.
  - tinh chỉnh thresholds theo baseline thật sau canary.
- Expected behavior: phát hiện sớm escalation bất thường hoặc dependency cascade.
- Caller/callee impact: on-call routing cập nhật theo severity.

## File: `controlplane/alerts/README.md`
- Ownership layer: operational docs.

### Section: current rules + on-call guide
- Current state: có quick guide cơ bản.
- Planned action: **update**
  - bổ sung runbook notes cho isolation/block incidents.
- Expected behavior: on-call triage nhanh, giảm MTTR.
- Caller/callee impact: docs-only.

## File: `controlplane/internal/security/docs/spec/controlplane-anti-probing-hyperscale-evolution-v1.md`
- Ownership layer: spec SoT.

### Section updates
- Planned action: **update**
  - khóa implementation status theo phase P0-P3.
  - annotate rõ phần đã triển khai vs deferred.
- Expected behavior: tránh scope drift giữa dev/ops/review.
- Caller/callee impact: docs-only.

## File: `controlplane/internal/security/docs/plan/plan-anti-probing-ratelimit-findings-fix.md`
- Ownership layer: execution plan SoT.

### Section updates
- Planned action: **update**
  - sync phase status sau mỗi increment.
  - thêm completion gate cho ladder `throttle/isolation/block`.
- Expected behavior: reviewer thấy tiến độ rõ theo evidence.
- Caller/callee impact: docs-only.

# 7. Contract & Boundary Checks

- Handler/service/repo/db boundaries: không đổi, không đưa SQL/business vào middleware.
- Anti-probing runtime vẫn đặt ở middleware + ratelimit core.
- Fail-open/fail-closed expectation:
  - giữ `SetFailOpen(false)` cho runtime hiện tại.
  - backend_unavailable phải có alert + runbook xử lý.
- Compatibility:
  - không đổi public API handlers,
  - không đổi route path,
  - response message vẫn generic.

# 8. Risk / Impact Analysis

- Risk: escalation quá nhạy gây false positive.
  - Mitigation: phase rollout theo route class, canary trước full rollout.
- Risk: lock contention local cache vẫn cao khi abuse burst.
  - Mitigation: bounded scan + benchmark before/after.
- Risk: alert noise sau thêm rules.
  - Mitigation: tune thresholds bằng dữ liệu canary 24-72h.
- Risk: docs-code drift khi thay decision ladder.
  - Mitigation: update spec+plan cùng PR với runtime code.

# 9. Verification Plan

## Phase-gated verification

### P0 checks
- Check P0-1: auth routes không còn route-level preauth.
- Check P0-2: global preauth vẫn active và bypass health/metrics đúng.
- Success criteria:
  - mỗi request auth chỉ có 1 preauth stage.

### P1 checks
- Check P1-1: deny log volume giảm theo sampling policy.
- Check P1-2: cache metrics có `miss_absent`/`miss_expired` tách rõ.
- Check P1-3: cache cap pressure không làm p95 middleware latency tăng quá baseline +10%.
- Success criteria:
  - log cost giảm,
  - observability rõ hơn,
  - latency ổn định.

### P2 checks
- Check P2-1: escalation `throttle -> temporary_isolation -> block` hoạt động đúng transition.
- Check P2-2: isolation TTL và block TTL release có jitter hợp lệ.
- Check P2-3: identity-aware postauth escalation không block oan NAT theo baseline test.
- Success criteria:
  - escalation đúng,
  - false positive không vượt ngưỡng vận hành.

### P3 checks
- Check P3-1: alerts mới firing đúng khi inject scenarios (`backend_unavailable`, isolation surge, block surge).
- Check P3-2: on-call quick guide đủ để triage không cần đọc code.
- Success criteria:
  - alertability + operability đạt yêu cầu trực ca.

## Layer checks
- **Transport**: middleware ordering, response codes, retry-after headers.
- **Security core**: key format deterministic, no empty-key regressions.
- **Observability**: metric labels stable, alert rules load thành công.
- **Docs**: spec/plan/review đồng nhất quyết định CURRENT/NEXT/DEFERRED.

## Metrics contract (release này)
- Bắt buộc có:
  - `security_ratelimit_check_total{route_pattern,rule_scope,result}`
  - `security_ratelimit_decision_total{route_pattern,decision,rule_scope}`
  - `security_ratelimit_error_total{route_pattern,error_type}`
  - `security_ratelimit_eval_duration_seconds{rule_scope}`
  - `security_ratelimit_local_cache_total{action,rule_scope}`
- Action labels chuẩn:
  - `lookup|hit|miss_absent|miss_expired|set|evict_expired|set_drop_at_capacity`

## Log contract (release này)
- Fields bắt buộc:
  - `request_id`, `route_pattern`, `rule_scope`, `decision`, `escalation_reason`, `retry_after_ms`, `ttl_ms`, `subject_key_hash`.
- Sampling mặc định:
  - throttle low sampling,
  - temporary_isolation medium sampling,
  - block high sampling,
  - backend_unavailable high/100% sampling.

## Phase-to-skill mapping
- `P0 -> backend-code-implementer`: remove route-level preauth drift + keep global preauth ordering.
- `P1 -> backend-code-implementer`: sampling + cache eviction hardening + metrics miss split.
- `P2 -> backend-code-implementer`: implement isolation/block transitions.
- `P2.5 -> distributed-flow-reviewer`: re-review flow và cập nhật risk ranking.
- `P3 -> spec-writer`: lock final docs contract theo runtime implementation.

# 10. Rollback Plan

- Rollback code theo phase commit:
  - P0 rollback: restore pre-change route middleware chain.
  - P1 rollback: disable sampling/keep old logging behavior.
  - P2 rollback: fallback decision ladder về `allow/throttle`.
- Rollback alert:
  - disable new alerts nếu noise cao,
  - giữ alert backend_unavailable là mandatory.
- Rollback docs:
  - revert spec status block đồng bộ với rollback code state.

# 11. Open Questions

1. TTL chuẩn cho `temporary_isolation` và `block` nên chốt bao nhiêu theo traffic thật?
2. Mức sampling mặc định cho `temporary_isolation` trong production là bao nhiêu để cân bằng cost/forensic?
3. Route matrix nào là “auth-sensitive bắt buộc postauth escalation”, route nào giữ throttle-only?
4. Có cần bật block cho tất cả routes hay chỉ cho nhóm critical/admin trước?
5. Khi backend unavailable kéo dài, có cần policy degrade đặc biệt cho route essential ngay trong release này không?
