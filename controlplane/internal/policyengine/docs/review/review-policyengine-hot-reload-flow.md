# Review: PolicyEngine Hot-Reload Flow

## 1. Review Scope & Evidence
- Entrypoint flow reviewed: `policyengine.NewModule` -> `EngineService.Start` -> `runReloadLoop` / `runPropagationConsumeLoop` -> `Reload` -> `loadPolicySnapshotFromSource`.
- Terminal boundaries:
  - Source read: YAML file adapter (`internal/policyengine/adapter/yaml_file_source_adapter.go`).
  - Cross-instance bus: Redis Pub/Sub notifier (`internal/policyengine/service/redis_notifier.go`).
- Files/functions reviewed:
  - `internal/policyengine/module.go`: `NewModule`, `Stop`
  - `internal/policyengine/service/engine_service.go`: `NewEngineService`, `Start`, `Current`, `Reload`, `runReloadLoop`, `runPropagationConsumeLoop`, `loadPolicySnapshotFromSource`
  - `internal/policyengine/service/redis_notifier.go`: `NewRedisPubSubNotifier`, `PublishPolicyChanged`, `SubscribePolicyChanged`
  - `internal/policyengine/adapter/yaml_file_source_adapter.go`: `ReadMeta`, `ReadCurrent`
  - `internal/policyengine/domain/service/engine_service.go`: interfaces/contracts
  - `internal/policyengine/domain/entity/policy.go`: runtime entities
  - `internal/policyengine/docs/spec/spec-policy-engine-hot-reload-v1.md`

## 2. End-to-End Call Graph
- Bootstrap path:
  - `app.NewGlobalModules` -> `policyengine.NewModule` -> tạo adapter/notifier/subscriber -> `NewEngineService` -> `service.Start(workerCtx)`.
- Runtime sync path (poll):
  - `runReloadLoop` ticker 3s -> `Reload` -> `ReadMeta` gate -> nếu đổi -> `ReadCurrent` + parse YAML + validate + checksum + swap snapshot.
- Runtime sync path (event):
  - `runPropagationConsumeLoop` -> `SubscribePolicyChanged` -> event checksum khác active -> gọi `Reload`.
- Side effects:
  - Logs qua `logger.SysInfoFields` / `SysWarnFields`.
  - Redis publish currently **not triggered** from `Reload` (see findings).

## 3. Layer Boundary Findings
1) **Confirmed**
- Location: `internal/policyengine/module.go:26`
- Function: `NewModule`
- Reason: Fail-fast dependency validation đặt ở module boundary đúng intent.
- Impact: Boundary rõ giữa provisioning (module) và execution (service).
- Evidence: check `rds == nil`, source/notifier/subscriber nil trước khi build service.
- Confidence: High

2) **Confirmed**
- Location: `internal/policyengine/service/engine_service.go:53`
- Function: `NewEngineService`
- Reason: Constructor không tự spawn goroutine; lifecycle thuộc module.
- Impact: Tránh hidden side-effect khi DI/provisioning.
- Evidence: `Start(ctx)` gọi riêng trong module.
- Confidence: High

## 4. Ownership & Source of Truth Findings
1) **Confirmed**
- Location: `internal/policyengine/service/engine_service.go:183`
- Function: `loadPolicySnapshotFromSource`
- Reason: SoT là YAML file; service không đụng DB.
- Impact: Đúng decision “DB chỉ business data”.
- Evidence: read từ adapter, validate `.yaml/.yml`, parse YAML.
- Confidence: High

2) **Confirmed**
- Location: `internal/policyengine/service/engine_service.go:123`
- Function: `Reload`
- Reason: `lastChecksum`/`lastMetaKey` là authority nội bộ cho dedupe/swap.
- Impact: Tránh swap lặp, giữ hot path ổn định.
- Evidence: gates `sameChecksum || sameMeta`.
- Confidence: High

## 5. Duplicate Logic / Duplicate Data Findings
1) **Confirmed**
- Location: `internal/policyengine/adapter/yaml_file_source_adapter.go:24` and `internal/policyengine/service/engine_service.go:87`
- Function: `ReadMeta`, `Reload`
- Reason: Metadata key được tạo nhiều nơi (adapter tạo fields, service tự compose `metaKey`).
- Impact: Rủi ro drift format key nếu thay đổi 1 bên.
- Evidence: service compose `path:version:size` thủ công ở nhiều chỗ.
- Confidence: Medium

## 6. Implicit Behavior & Hidden Contract Findings
1) **Confirmed**
- Location: `internal/policyengine/module.go:33`
- Function: `NewModule`
- Reason: Path YAML hardcoded `runtime/policies/policy.yaml` là implicit contract với runtime deploy.
- Impact: Deploy sai mount path -> service chạy nhưng reload fail liên tục.
- Evidence: path literal không qua config contract.
- Confidence: High

2) **Confirmed**
- Location: `internal/policyengine/service/engine_service.go:28`
- Function: constants
- Reason: Cooldown 5s hardcoded; không nêu rõ ảnh hưởng convergence SLA trong code.
- Impact: Thay đổi burst behavior cần patch code, khó tune vận hành.
- Evidence: `defaultReloadCooldown = 5 * time.Second`.
- Confidence: High

## 7. Bottleneck & Performance Findings
1) **Confirmed**
- Location: `internal/policyengine/service/engine_service.go:87`
- Function: `Reload`
- Reason: Đã có metadata-first gate trước read/parse, giảm IO/CPU khi file không đổi.
- Impact: Giảm cost đáng kể ở steady-state.
- Evidence: `ReadMeta` + `unchanged` early return.
- Confidence: High

2) **Hypothesis**
- Location: `internal/policyengine/service/engine_service.go:162`
- Function: `runPropagationConsumeLoop`
- Reason: Event consumer channel buffer=1; nếu burst event + parse chậm có thể drop timeliness (không drop data vì reload re-check source).
- Impact: Convergence latency tăng trong burst.
- Evidence: channel typed from notifier buffered 1.
- Confidence: Medium

## 8. Consistency Model Findings
- Actual model inferred:
  - Within instance: atomic swap semantics (RWMutex guarded state update).
  - Cross-instance: eventual consistency via Redis event trigger + local source reload.
- Finding:
  - Location: `internal/policyengine/service/engine_service.go:162`
  - Function: `runPropagationConsumeLoop`
  - Reason: consumer relies on event for fast path, poll for fallback.
  - Impact: Guarantees eventual, not strong consistency.
  - Evidence: event mismatch checksum -> call `Reload`; poll loop always running.
  - Confidence: High

## 9. Cache / Consistency / Staleness Findings
1) **Confirmed**
- Location: `internal/policyengine/service/engine_service.go:110`
- Function: `Reload`
- Reason: `last-known-good` behavior achieved by no mutation on parse/validate error.
- Impact: stale window controlled, no poison-snapshot overwrite.
- Evidence: error path return trước khi swap.
- Confidence: High

2) **Confirmed**
- Location: `internal/policyengine/service/engine_service.go:128`
- Function: `Reload`
- Reason: cooldown 5s có thể kéo dài stale window có chủ đích.
- Impact: đổi mới policy có thể delay apply tối đa gần 5s/instance.
- Evidence: inCooldown early return.
- Confidence: High

## 10. Consistency & Partition Tolerance Assessment
- Partition behavior (Redis unavailable):
  - Degrade sang poll-only (consumer subscribe fail/close -> warn + return).
  - Instance vẫn hội tụ nhờ `runReloadLoop`.
- CAP practical stance:
  - Availability ưu tiên trên fast propagation when bus partitioned.
  - Consistency cross-instance degrade from near-instant -> poll interval bound.
- Assessment: hợp lý cho single-region operational runtime.

## 11. Reliability Target Assessment
- Status: **AT_RISK**
- Reason:
  - Spec nói có propagation event path đầy đủ và observability contract tối thiểu; code hiện tại đã có consume path nhưng thiếu publish path trong `Reload` (removed previously).
- Evidence:
  - `internal/policyengine/service/engine_service.go:140` (no `PublishPolicyChanged` call after swap).
- Confidence: High

## 12. Degradation & Failure Cascade Findings
1) **Confirmed**
- Location: `internal/policyengine/service/engine_service.go:166`
- Function: `runPropagationConsumeLoop`
- Reason: subscribe fail -> log warn -> return; no retry loop inside consumer.
- Impact: event acceleration mất cho đến restart module/service context.
- Evidence: returns on subscribe error/channel close.
- Confidence: High

2) **Confirmed**
- Location: `internal/policyengine/service/engine_service.go:148`
- Function: `runReloadLoop`
- Reason: poll loop continues independently; prevents full cascade.
- Impact: resilience tốt nhưng convergence chậm hơn khi bus lỗi.
- Evidence: ticker loop always calls `Reload`.
- Confidence: High

## 13. RFC / ADR Conformance Assessment
- Against spec `internal/policyengine/docs/spec/spec-policy-engine-hot-reload-v1.md`:
  - Conforms:
    - YAML-only SoT
    - no HTTP route for policyengine
    - module-level fail-fast provisioning
    - low-noise logging style
  - Drift:
    - Spec section fast propagation expects publish+consume metadata event; current code has consume path but missing publish call from reload success.
- Overall: **PARTIAL_CONFORMANCE**

## 14. Operational Risk Findings
1) **Confirmed**
- Location: `internal/policyengine/module.go:33`
- Function: `NewModule`
- Reason: hardcoded source path risks misconfiguration in deploy variants.
- Impact: silent operational mismatch if mount path changes.
- Evidence: string literal path.
- Confidence: High

2) **Confirmed**
- Location: `internal/policyengine/service/engine_service.go:94`
- Function: `Reload`
- Reason: `ReadMeta` error silently falls through to full read path (not logged at metadata stage).
- Impact: debug harder for intermittent stat errors.
- Evidence: `if err == nil { ... }` no warn on err.
- Confidence: Medium

## 15. Risk Ranking (P0/P1/P2)
- **P0**
  - Missing publish on successful reload causes cross-instance fast sync contract to be incomplete.
  - Location: `internal/policyengine/service/engine_service.go:140`
- **P1**
  - Propagation consumer exits on subscribe failure without retry.
  - Location: `internal/policyengine/service/engine_service.go:166`
- **P1**
  - Hardcoded path operational drift risk.
  - Location: `internal/policyengine/module.go:33`
- **P2**
  - Meta-key composition duplicated, maintenance drift risk.
  - Location: `internal/policyengine/service/engine_service.go:92,208`

## 16. Optimization Recommendations
1) Priority: P0 | Effort: S | Risk Reduction: High | Expected Gain: restore near-instant cross-instance convergence
- Impacted file/function: `internal/policyengine/service/engine_service.go:Reload`
- Recommendation: re-add `notifier.PublishPolicyChanged(...)` after successful swap; keep best-effort (warn only, no rollback).

2) Priority: P1 | Effort: M | Risk Reduction: Medium | Expected Gain: propagation resilience under Redis flaps
- Impacted file/function: `internal/policyengine/service/engine_service.go:runPropagationConsumeLoop`
- Recommendation: wrap subscribe in retry loop with backoff (2s->4s->8s max 30s) instead of return-once.

3) Priority: P1 | Effort: S | Risk Reduction: Medium | Expected Gain: deploy portability
- Impacted file/function: `internal/policyengine/module.go:NewModule`
- Recommendation: inject source path from module config/bootstrap constant; avoid hardcoded literal in constructor call.

4) Priority: P2 | Effort: S | Risk Reduction: Low | Expected Gain: maintainability
- Impacted file/function: `internal/policyengine/service/engine_service.go`
- Recommendation: centralize `metaKey` builder helper để tránh drift string format.

## 17. Verification Plan
- Check 1 (P0 fix):
  - Trigger successful reload on instance A, verify instance B receives Redis event and enters reload path within poll interval budget.
  - Success criteria: B logs reload success with new checksum before next poll tick window.
- Check 2 (retry resilience):
  - Simulate Redis temporary down/up.
  - Success criteria: consumer reconnects without process restart.
- Check 3 (staleness guard):
  - Push invalid YAML.
  - Success criteria: active checksum unchanged; warn log emitted.
- Check 4 (cooldown correctness):
  - Trigger burst events <5s window.
  - Success criteria: at most one swap per cooldown window.

## 18. Open Questions
- Có chủ đích bỏ publish event để chỉ poll-based sync không? Nếu không, nên coi đây là regression.
- Path `runtime/policies/policy.yaml` có cố định cho mọi môi trường deploy hiện tại không?
- Có cần retry subscribe loop bắt buộc ở phase hiện tại hay chấp nhận restart-based recovery?
