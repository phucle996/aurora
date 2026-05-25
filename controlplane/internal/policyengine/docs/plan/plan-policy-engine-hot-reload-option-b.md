# Plan: Policy Engine Hot Reload Option B

## 1. Bối cảnh và mục tiêu thay đổi
Hiện `policyengine` đã có baseline hot reload YAML bằng polling nhưng chưa khớp đầy đủ spec Option B đã chốt:
- còn giữ transport handler/module handler không cần thiết cho scope runtime-only,
- chưa có adapter contract theo môi trường,
- chưa có notifier Redis Pub/Sub để tăng tốc sync cross-instance,
- chưa tối ưu watch-first + poll-fallback + coalescing/single-flight theo spec,
- log contract/metric contract chưa tối giản đúng bản spec cuối.

Mục tiêu của plan này là đưa code về đúng scope: **runtime hot-reload mechanism only**, YAML SoT, sync cross-instance bằng Redis Pub/Sub, low-overhead cho vận hành.

## 2. Phạm vi
### Trong phạm vi
- Refactor `policyengine` service theo Option B (adapter + notifier + single-flight).
- Loại bỏ dependency route/handler khỏi module policyengine.
- Chốt log contract theo `pkg/logger/logger.go` (system logs, low-noise).
- Chốt metric tối thiểu theo spec.
- Đồng bộ module wiring để app có thể inject runtime dependencies.

### Ngoài phạm vi
- Không define policy semantics thực chiến cho `admin_cidr`/`rate_limit`.
- Không mở API endpoint quản trị policy.
- Không mở rộng sang DB SoT hoặc multi-region.
- Không thay đổi middleware business logic trong plan này.

## 3. Pre-Change Log
- `internal/policyengine/runtime/engine_service.go`
  - CURRENT_CODE: poll-only mỗi 3s; load YAML; checksum compare; swap snapshot; log Info/Warn.
  - GAP: chưa có watch-first/poll-fallback mode; chưa có adapter abstraction; chưa có Redis propagation.
- `internal/policyengine/runtime/engine_service.go`
  - CURRENT_CODE: chỉ có `EngineService{Current, Reload}`.
  - GAP: thiếu contract cho source adapter và propagation notifier.
- `internal/policyengine/module.go`
  - CURRENT_CODE: tạo cả `EngineService` và `EngineHandler`.
  - GAP: lệch spec runtime-only (không cần handler/route).
- `internal/policyengine/transport/http/handler/engine_handler.go`
  - CURRENT_CODE: có `GetCurrent`/`Reload` endpoint.
  - GAP: trái scope hiện tại (không route/handler).
- `internal/policyengine/docs/spec/spec-policy-engine-hot-reload-v1.md`
  - CURRENT_CODE: contract đã chốt Option B + Redis Pub/Sub + metric/log tối giản.
  - GAP: code chưa phản ánh đủ contract này.

## 4. Naming Plan
- Type/contract mới:
  - `PolicySourceAdapter` (new) -> contract source detection/read theo môi trường.
  - `PolicyPropagationNotifier` (new) -> contract publish/subscribe metadata event.
  - `PolicyChangedEvent` (new) -> payload chuẩn cho Redis Pub/Sub.
- Function naming chuẩn:
  - `startAutoReloadLoop` -> `runReloadLoop` (rõ ownership runtime loop).
  - `loadPolicyFromYAML` -> `loadPolicySnapshotFromSource` (không hardcode ý nghĩa file parsing trong tên khi có adapter).
- Module naming:
  - `EngineHandler` giữ tạm để tương thích compile phase đầu, sau đó remove khỏi module expose.
- Compatibility impact:
  - `NewEngineService` giữ entrypoint công khai, mở rộng signature theo dependency injection (nếu cần) bằng constructor mới để tránh break lớn ngay lập tức.

## 5. File-Scoped Action Plan (gộp file + function)
- `internal/policyengine/runtime/engine_service.go` (layer: service contract)
  - `type EngineService`
    - Current state: `Current`, `Reload`.
    - Planned action: **update** giữ nguyên 2 method; thêm comment contract fail-fast/fallback.
    - Expected behavior: caller không đổi interface usage.
  - `type PolicySourceAdapter` (new)
    - Planned action: **add** contract `Start`, `ReadCurrent`, `SourceMeta`, `DetectionMode`.
    - Caller/callee impact: `service/engine_service.go` consume adapter.
  - `type PolicyPropagationNotifier` (new)
    - Planned action: **add** contract publish/subscribe event metadata.
    - Caller/callee impact: `service/engine_service.go` consume notifier.

- `internal/policyengine/runtime/engine_service.go` (layer: service impl)
  - `func NewEngineService(...)`
    - Current state: nhận `cfg`, tự tạo poll loop background.
    - Planned action: **update** để nhận injected adapter + notifier + options (poll interval, size cap).
    - Expected behavior: bootstrap fail-fast nếu dependency required nil.
  - `func (s *EngineService) Current(ctx)`
    - Planned action: **update** giữ semantics immutable snapshot copy; chuẩn hóa error `ErrPolicyUnavailable`.
  - `func (s *EngineService) Reload(ctx)`
    - Planned action: **update** dùng single-flight gate + 2-phase change check + checksum idempotency.
    - Expected behavior: swap chỉ khi valid + changed; unchanged không log nhiễu.
  - `func (s *EngineService) runReloadLoop(ctx)`
    - Planned action: **rename/update** từ `startAutoReloadLoop`; thực thi watch-first + poll-fallback.
    - Expected behavior: watch lỗi -> degrade poll mode + warn log.
  - `func (s *EngineService) handlePropagationEvent(...)` (new)
    - Planned action: **add** consume Redis event, trigger reload by checksum mismatch.
    - Expected behavior: duplicate/stale event skip; no rollback local active.
  - `func (s *EngineService) publishPropagationEvent(...)` (new)
    - Planned action: **add** publish event sau reload success.
    - Expected behavior: publish lỗi chỉ warn, không fail local swap.
  - `func (s *EngineService) loadPolicySnapshotFromSource(...)`
    - Planned action: **rename/update** parse YAML + strict checks (`version=v1`, non-empty policies, size<=1MB).
  - `func resolvePolicyFilePath()`
    - Planned action: **remove or move** vào file adapter nếu path ownership chuyển sang adapter.

- `internal/policyengine/runtime/*adapter*.go` (new files, layer: service infra-adapter)
  - `type FileSourceAdapter` (new)
    - Planned action: **add** adapter cho file source; watch-first/poll-fallback.
    - Expected behavior: emit onChange coalesced, tránh burst.
  - `type RedisPubSubNotifier` (new)
    - Planned action: **add** notifier dùng Redis Pub/Sub topic `policyengine.policy.changed.v1`.
    - Expected behavior: at-least-once consume, idempotent at service layer.

- `internal/policyengine/module.go` (layer: module wiring)
  - `type Module`
    - Current state: expose `EngineService` + `EngineHandler`.
    - Planned action: **update** chỉ expose `EngineService` cho runtime scope.
  - `func NewModule(cfg *config.Config)`
    - Planned action: **update** nhận thêm dependency cần thiết (ít nhất Redis client hoặc notifier factory).
    - Expected behavior: fail-fast nếu dependency required thiếu.

- `internal/policyengine/transport/http/handler/engine_handler.go` (layer: handler)
  - `type EngineHandler`, `GetCurrent`, `Reload`
    - Planned action: **remove** khỏi module wiring; file có thể delete sau khi xác nhận không caller nào dùng.
    - Expected behavior: không còn policyengine HTTP surface.

- `internal/app/module.go` (layer: app bootstrap)
  - `func NewGlobalModules(...)`
    - Planned action: **update** wiring policyengine module với Redis notifier + source adapter dependency.
    - Expected behavior: fail-fast đúng boundary nếu policyengine init lỗi.

- `internal/policyengine/docs/spec/spec-policy-engine-hot-reload-v1.md` (layer: docs)
  - Planned action: **update nhẹ** nếu symbol/fn name thay đổi khác spec hiện tại.

## 7. Contract & Boundary Checks
- Boundary chain:
  - Runtime caller (`admin_cidr`, `rate_limit`) -> `EngineService.Current`.
  - Source adapter/notifier chỉ ở service infra boundary; không rò sang business middleware.
- Contract checks:
  - YAML file vẫn là SoT duy nhất.
  - Redis event chỉ là trigger channel, không chứa full policy payload.
  - `Reload` invariant: invalid -> giữ `last_known_good`.
- Leakage checks:
  - Không có SQL/DB access trong policyengine service.
  - Không log payload policy chi tiết.

## 8. Risk / Impact Analysis
- Rủi ro deadlock/race khi thêm single-flight + watcher + subscriber.
  - Giảm thiểu: một writer loop, queue depth 1, immutable snapshot.
- Rủi ro noisy logs/metrics làm tăng chi phí quan sát.
  - Giảm thiểu: chỉ log 4 event quan trọng theo spec.
- Rủi ro divergence cross-instance khi Redis lag/down.
  - Giảm thiểu: watch/poll fallback + convergence SLA checks.
- Rủi ro break compile do remove handler/module field.
  - Giảm thiểu: phase remove sau khi grep caller và cập nhật wiring.

## 9. Verification Plan
- Service-level checks:
  - Valid YAML -> reload success, checksum đổi, snapshot đổi.
  - Invalid YAML -> reload failed, snapshot giữ nguyên.
  - Same checksum -> không swap, không log noise.
- Propagation checks:
  - Instance A reload success -> publish event.
  - Instance B consume event -> reload ngay nếu checksum khác.
  - Duplicate/stale event -> skip idempotent.
- Observability checks:
  - Log phải chỉ có 4 op: `policyengine.reload.success`, `policyengine.reload.failed`, `policyengine.source.degraded`, `policyengine.propagation.failed`.
  - Metrics tối thiểu có đủ 5 metric core theo spec.
- Bootstrap checks:
  - Missing required dependency (source/notifier) -> fail-fast tại `NewModule`/`NewGlobalModules`.
- Phase-gated sequence:
  - P1: lock contracts + compile pass.
  - P2: service refactor + local unit/integration tests.
  - P3: wiring app + end-to-end reload propagation check.

## 10. Rollback Plan
- Rollback code:
  - Revert `policyengine` module về poll-only baseline trước refactor.
- Rollback runtime behavior:
  - Disable notifier consume/publish qua config flag (nếu có) để fallback watch/poll-only.
- Rollback policy config:
  - Re-apply ConfigMap revision trước đó; verify checksum active qua metric/log.

## 11. Open Questions
- Constructor strategy:
  - Giữ `NewEngineService(cfg)` và thêm `NewEngineServiceWithDeps(...)` hay đổi signature trực tiếp?
- Redis topic namespacing:
  - Dùng cứng `policyengine.policy.changed.v1` hay prefix theo env/project?
- Watch implementation:
  - Dùng fsnotify trực tiếp hay poll-only phase đầu rồi nâng cấp watch ở increment sau?
