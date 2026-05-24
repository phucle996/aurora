# 1. Bối cảnh và mục tiêu thay đổi

Dựa trên review `controlplane/internal/security/docs/review/review-anti-probing-ratelimit-flow.md`, cần tạo plan fix tập trung cho flow anti-probing rate-limit (PreAuth/PostAuth) theo ưu tiên rủi ro:
- P0: khóa ownership `RateLimitPreAuth` ở global-only (không gắn preauth per-route) để tránh over-throttle và tăng Redis hot-path load.
- P1: triển khai log sampling cho security-event deny path, giảm ingest/latency risk.
- P1: giảm lock contention khi local deny-cache full (eviction O(n) under lock).
- P1: bổ sung alert cho `security_ratelimit_error_total` để phát hiện degradation Redis/backend.
- P2: làm rõ semantics metric miss/expired miss và chuẩn bị config-driven bypass policy.

Mục tiêu release của plan này:
1) giữ boundary middleware/security-core như hiện tại,
2) giảm risk cascade trong burst/degradation,
3) khóa docs-code contract để tránh drift vòng tiếp theo.

# 2. Phạm vi

## Trong phạm vi
- `controlplane/internal/http/middleware/ratelimiter.go`:
  - tách rõ stage metric,
  - giảm duplicate preauth pressure,
  - sampling security-event logs,
  - tối ưu local cache eviction path,
  - chuẩn hóa cache-miss semantics metric.
- `controlplane/internal/app/app.go` + `controlplane/internal/iam/route.go`:
  - chỉnh middleware ownership để tránh double preauth không chủ đích.
- `controlplane/internal/config/config.go`:
  - thêm config knobs cho sampling/bypass/stage control (nếu cần theo compatibility).
- `controlplane/alerts/rules/anti-probing-ratelimit-v1-alerts.yaml`:
  - thêm alert về backend error surge và deny/error ratio.
- `controlplane/internal/security/docs/spec/controlplane-anti-probing-v1-spec.md` + plan hiện hành:
  - update implementation status và decision contract thực tế.

## Ngoài phạm vi
- Không đổi business logic IAM handler/service/repository.
- Không triển khai full `cooldown/block` state machine distributed (defer phase sau).
- Không thay đổi `ratelimit.Bucket` Redis Lua contract ở vòng fix này.
- Không mở rộng sang risk-engine hoặc circuit-breaker implementation.

### Phase roadmap
- **P0 (safety-first wiring)**: giải quyết double preauth ownership.
- **P1 (operational hardening)**: sampling logs + alert error surge + cache eviction optimization.
- **P2 (contract clarity)**: metrics semantics clarity + docs/spec alignment + config hardening.

# 3. Pre-Change Log

## CURRENT_CODE snapshot
- Global chain có `RateLimitPreAuth(global_preauth)` ở `app.go` đồng thời nhiều auth routes còn `RateLimitPreAuth(iam_...)` ở `iam/route.go`.
- `RateLimitPreAuth` và `RateLimitPostAuth` đều emit deny security-event log trực tiếp, chưa sampling theo policy.
- Local deny-cache dùng map + RWMutex, eviction scan full map khi chạm cap.
- Metrics đã có `security_ratelimit_local_cache_total`, nhưng `miss` đang gồm cả absent + expired paths.
- Alert rules hiện chủ yếu xoay quanh local cache efficiency/churn, chưa cover backend-unavailable surge.

## Impacted files + current responsibilities
- `internal/http/middleware/ratelimiter.go`: transport security gate + metrics/log emit owner.
- `internal/app/app.go`: global middleware composition owner.
- `internal/iam/route.go`: route-level middleware wiring owner.
- `internal/config/config.go`: runtime knobs owner.
- `alerts/rules/anti-probing-ratelimit-v1-alerts.yaml`: operational detection rules owner.
- `internal/security/docs/spec/*` và `internal/security/docs/plan/*`: docs contract owner.

## Docs-code mismatches cần fix
- Spec/plan nói đầy đủ ladder `allow/throttle/cooldown/block`, nhưng runtime hiện thực tế mới `allow/throttle`.
- Logging policy sampling đã ghi trong docs nhưng code chưa enforce.

# 4. Naming Plan

## Naming decisions chốt
1. Stage ownership labels cho metrics/log:
   - `preauth_global`
   - `postauth_identity`

2. Security-event sampling config struct (new):
   - `type RateLimitLogSamplingConfig struct`
   - fields:
     - `ThrottleSamplePercent int`
     - `CooldownSamplePercent int`
     - `BlockSamplePercent int`
     - `ErrorSamplePercent int`

3. Local cache metrics action tách rõ:
   - `miss_absent`
   - `miss_expired`
   - giữ `hit|lookup|set|evict_expired|set_drop_at_capacity`

4. Compatibility strategy (rename/migrate)
- **No public rename** cho `RateLimitPreAuth` / `RateLimitPostAuth`.
- Bổ sung fields/labels theo backward-compatible additive strategy.
- Alert names hiện tại giữ nguyên; thêm alert mới tên:
  - `ControlplaneRateLimitBackendUnavailableSurge`

# 5. File-Scoped Action Plan (gộp file + function)

## File: `controlplane/internal/app/app.go`
- Ownership layer: app bootstrap/global composition.

### Function block: `engine.Use(...)`
- Current state: global `RateLimitPreAuth(global_preauth)` đang always-on.
- Planned action: **update**
  - khóa cứng `RateLimitPreAuth` là global-only.
  - explicit ordering giữ: `RequestID -> ... -> RateLimitPreAuth(stage=preauth_global) -> AccessLog`.
- Expected behavior: mỗi request auth chỉ chịu đúng 1 preauth stage.
- Caller/callee impact: cần update wiring nhất quán ở `iam/route.go`.

## File: `controlplane/internal/iam/route.go`
- Ownership layer: transport wiring.

### Function: `RegisterRoutes(...)`
- Current state: đa số auth/admin routes đang có route-level preauth.
- Planned action: **update**
  - remove toàn bộ route-level `RateLimitPreAuth(...)`.
  - giữ `RateLimitPostAuth(...)` sau `Access(...)`/`AdminAPIKeyAuth(...)` như hiện tại.
- Expected behavior: tránh double token-bucket checks không chủ đích.
- Caller/callee impact: không đổi handler business.

## File: `controlplane/internal/http/middleware/ratelimiter.go`
- Ownership layer: transport security gate + observability owner.

### Function: `RateLimitPreAuth(...)`
- Current state: deny logs always emit; chưa có stage label rõ; local cache miss semantics gộp.
- Planned action: **update**
  - thêm stage context cố định `preauth_global` vào metric/log labels.
  - áp sampling gate trước khi emit security-event log.
  - split miss metric thành `miss_absent` và `miss_expired`.
- Expected behavior: observability chính xác hơn, giảm log cost.
- Caller/callee impact: caller cần pass/derive stage (qua wrapper private hoặc config).

### Function: `RateLimitPostAuth(...)`
- Current state: tương tự preauth deny log always emit.
- Planned action: **update**
  - apply sampling gate + stage label `postauth_identity`.
  - giữ identity key priority `ip+device -> ip+user`.
- Expected behavior: giảm log amplification khi burst deny.
- Caller/callee impact: không đổi signature public, only internal labels/logic.

### Function: `emitRateLimitSecurityEvent(...)`
- Current state: always log.
- Planned action: **update**
  - thêm decision-aware sampling check.
  - thêm field `stage` và `sampled=true/false` (nếu sampled=false thì skip write, chỉ metric increment).
- Expected behavior: log policy bám docs (throttle thấp, block cao).
- Caller/callee impact: pre/post auth callers cần truyền decision/stage đầy đủ.

### Function: `denySubjectCache.getActive(...)`
- Current state: miss increment nhiều nhánh chung action `miss`.
- Planned action: **update**
  - emit `miss_absent` khi key không tồn tại,
  - emit `miss_expired` khi key hết hạn.
- Expected behavior: dashboard hit ratio và churn analysis chính xác hơn.
- Caller/callee impact: update alert/PromQL queries theo action mới.

### Function: `denySubjectCache.setBlocked(...)` và `evictExpiredLocked(...)`
- Current state: full scan eviction under lock khi cap.
- Planned action: **update**
  - chuyển sang bounded eviction step mỗi lần set (ví dụ max N scan keys/attempt) để giảm lock hold-time.
  - giữ hard cap và drop-at-capacity metric.
- Expected behavior: giảm contention under burst.
- Caller/callee impact: không đổi caller behavior.

### Function: `RegisterRateLimitMetrics(...)`
- Current state: register collectors hiện tại.
- Planned action: **defer (không fix trong increment này)**
  - giữ contract metrics hiện tại để tránh churn labels khi policy runtime còn chưa ổn định.
  - phần mở rộng labels/contract sẽ chuyển sang phase `policy engine/runtime` mới (global policy hot-reload), nơi schema metrics được chốt một lần cho toàn app.
- Expected behavior: không phá dashboard/alert hiện tại; giảm rủi ro đổi labels nhiều lần.
- Caller/callee impact: observability queries giữ nguyên ở release này; migration queries sẽ thực hiện cùng rollout policy engine.

## File: `controlplane/internal/config/config.go`
- Ownership layer: config/runtime knobs.

### Function/block: config struct loading
- Current state: chưa có knobs riêng cho rate-limit security-event sampling/bypass source.
- Planned action: **add**
  - thêm config nhóm `RateLimit`:
    - `BypassPaths []string` (default giữ set hiện tại)
    - `LogSampling` (`throttle/cooldown/block/error` percent)
- Expected behavior: ops tune không phải patch code cho mỗi lần chỉnh policy.
- Caller/callee impact: app/middleware đọc config khi bootstrap.

## File: `controlplane/alerts/rules/anti-probing-ratelimit-v1-alerts.yaml`
- Ownership layer: operations/alerting.

### Rule set updates
- Current state: chưa có alert backend error surge.
- Planned action: **update**
  - add alert `ControlplaneRateLimitBackendUnavailableSurge` từ `security_ratelimit_error_total{error_type="backend_unavailable"}`.
  - add alert deny/error ratio bất thường (throttle+error spike).
  - update PromQL theo action split (`miss_absent`, `miss_expired`) nếu áp dụng.
- Expected behavior: phát hiện sớm dependency degradation.
- Caller/callee impact: SRE dashboard/alerts sync.

## File: `controlplane/internal/security/docs/spec/controlplane-anti-probing-v1-spec.md`
- Ownership layer: spec SoT.

### Section updates
- Current state: có nội dung rộng hơn implementation hiện tại ở vài điểm.
- Planned action: **update**
  - thêm block `CURRENT_CODE / NEXT_INCREMENT / FUTURE_EVOLUTION`.
  - ghi rõ runtime hiện tại thực thi `allow/throttle`; `cooldown/block` là planned increment.
  - đồng bộ logging sampling implementation status.
- Expected behavior: giảm drift giữa spec và runtime.
- Caller/callee impact: docs-only.

## File: `controlplane/internal/security/docs/plan/plan-controlplane-anti-probing-v1-implementation.md`
- Ownership layer: execution plan SoT.

### Section updates
- Planned action: **update**
  - add remediation checklist theo findings P0/P1/P2.
  - add verification gates cho stage ownership, sampling efficacy, backend error alerts.
- Expected behavior: kế hoạch triển khai và review rõ ràng theo findings fix.
- Caller/callee impact: docs-only.

# 7. Contract & Boundary Checks

- Handler -> Service -> Repository -> DB chain **không đổi**; toàn bộ thay đổi nằm ở middleware/observability/config/docs.
- Không đưa SQL/business rules vào middleware.
- `ratelimit` core vẫn là nơi quyết định Redis token-bucket state.
- Middleware giữ quyền:
  - stage gating,
  - local cache optimization,
  - metric/log emission.
- Fail-open/fail-closed expectation:
  - giữ theo hiện trạng `SetFailOpen(false)` cho control-plane security posture,
  - nhưng phải có alert rõ cho backend_unavailable surge.

# 8. Risk / Impact Analysis

- **Risk 1**: Bỏ route-level preauth có thể làm mất một số threshold per-route đang dựa vào name riêng.
  - Mitigation: migrate threshold cần thiết sang `RateLimitPostAuth` hoặc map route metadata ở global preauth.
- **Risk 2**: Sampling quá thấp làm mất forensic.
  - Mitigation: block/error giữ sample cao; thêm metric decision counters để bù.
- **Risk 3**: Bounded eviction chưa tối ưu có thể vẫn contention.
  - Mitigation: benchmark lock hold-time, tune scan batch size.
- **Risk 4**: Alert noise khi thêm rule backend errors.
  - Mitigation: dùng `for` duration + threshold theo baseline prod.

# 9. Verification Plan

## Phase-gated checks

### P0 verification (stage ownership)
- Check P0-1: route auth sample chỉ có 1 preauth stage/check mỗi request.
- Check P0-2: reject ratio không tăng bất thường sau bỏ double preauth.
- Success criteria:
  - `security_ratelimit_check_total` giảm trùng lặp stage,
  - functional auth flows vẫn pass.

### P1 verification (sampling + cache + alerts)
- Check P1-1: deny log volume giảm theo sampling config, nhưng decision metrics vẫn đầy đủ.
- Check P1-2: `miss_absent`/`miss_expired` phản ánh đúng traffic pattern.
- Check P1-3: lock contention của cache giảm (p95 middleware latency không xấu hơn baseline).
- Check P1-4: alert `BackendUnavailableSurge` trigger đúng khi mô phỏng Redis degradation.
- Success criteria:
  - log cost giảm,
  - alertability tăng,
  - latency không regress đáng kể.

### P2 verification (docs/spec conformance)
- Check P2-1: spec/plan nêu rõ runtime vs future features.
- Check P2-2: không còn mismatch lớn giữa docs và behavior thực tế.
- Success criteria:
  - reviewer có thể trace từ docs -> code không mơ hồ.

## Layer checks
- **Transport/middleware**:
  - verify preauth/postauth ordering trên `iam` routes.
  - verify bypass endpoints vẫn reachable (`/metrics`, `/api/v1/health/*`).
- **Config**:
  - verify default values không phá behavior hiện tại.
- **Observability**:
  - verify metric labels mới được scrape ổn định.
  - verify alert rules load thành công và firing đúng condition test.

## Metrics contract updates (explicit)
- `security_ratelimit_local_cache_total{action,rule_scope,stage?}` (nếu thêm stage vào metric này).
- Actions chuẩn sau fix:
  - `lookup|hit|miss_absent|miss_expired|set|evict_expired|set_drop_at_capacity`.

## Log contract updates (explicit)
- Security-event log fields bắt buộc:
  - `request_id`, `route_pattern`, `rule_scope`, `stage`, `decision`, `escalation_reason`, `retry_after_ms`, `ttl_ms`, `subject_key_hash`.
- Sampling behavior:
  - `throttle`: configurable low sample.
  - `error/backend_unavailable`: high sample hoặc 100% trong incident mode.

## Phase-to-skill mapping
- `P0 -> plan-writer`: chốt wiring/mode migration checklist chi tiết theo file/function.
- `P1 -> backend-code-implementer`: implement stage ownership + sampling + cache eviction optimization + alert additions.
- `P1.5 -> distributed-flow-reviewer`: review lại flow sau patch để xác nhận risk P0/P1 đã giảm.
- `P2 -> spec-writer`: đồng bộ spec/status contract theo code thực tế.

# 10. Rollback Plan

- Rollback code:
  - revert `ratelimiter.go`, `app.go`, `iam/route.go`, `config.go`, alert yaml theo commit boundary phase.
- Rollback runtime:
  - restore route-level preauth wiring theo commit trước nếu phát sinh regress,
  - set sampling về 100% tạm thời khi cần forensic đầy đủ.
- Rollback alert:
  - disable rule mới `BackendUnavailableSurge` nếu gây noise ngoài dự kiến.

# 11. Open Questions

1. Route-level threshold cũ nào cần migrate chính xác sang postauth để không mất guardrail?
2. Stage label có cần xuất hiện ở tất cả metrics hay chỉ ở decision/check/local_cache metric?
3. Sampling percent mặc định cho `throttle/error` ở môi trường hiện tại là bao nhiêu để cân bằng forensic-cost?
4. Bypass paths có cần dynamic config ngay vòng này hay để vòng sau sau khi có config rollout framework?
5. Trong Redis outage, có cần policy fail-open selective theo route class ngay ở plan fix này không?
