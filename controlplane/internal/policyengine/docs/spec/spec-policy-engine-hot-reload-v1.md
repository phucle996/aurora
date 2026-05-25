# Spec: Policy Engine Hot Reload (Option B - YAML Source Adapter per Environment)

## 1. Tổng quan
- Tên hệ thống: `controlplane/internal/policyengine`.
- Bài toán: cập nhật policy runtime không downtime, không restart process, phục vụ traffic CCU cao trong single-region HA.
- Phạm vi spec: mô tả target implementation cho Option B đã chốt trong idea doc: YAML-only SoT + adapter theo môi trường + runtime in-memory atomic swap + cross-instance propagation near-instant.
- Scope vận hành: đây là module internal ops runtime (non user-facing). Tối ưu ưu tiên low-overhead, deterministic behavior và độ ổn định; không ưu tiên feature breadth.
- Scope consumer hiện tại: policy engine chỉ phục vụ `admin_cidr` và `rate_limit`.
- Scope boundary: spec này chỉ tập trung cơ chế hot reload runtime; policy semantics thực chiến cho từng consumer nằm ở idea/spec riêng.

## 2. Mục tiêu
- Engineering goals:
  - Hot reload policy từ YAML file mà không restart.
  - Request hot-path đọc policy O(1) từ snapshot in-memory.
  - Invalid policy không được active; giữ `last-known-good`.
- Operational goals:
  - SRE thao tác publish policy bằng cơ chế native của môi trường (K8s/systemd/docker).
  - Có log/metrics đủ để xác nhận version/checksum active trên từng pod.
- Measurable targets:
  - p95 apply-time sau khi file thay đổi: `<= 2s` mỗi instance.
  - p95 cross-instance convergence (single-region): `<= 8s` ở profile thường, `<= 10s` ở profile peak 50k.
  - policy-read error rate: `< 0.01%`/5m.

## 3. Non-Goals
- Không dùng DB làm source-of-truth cho policy.
- Không mở route/handler cho manual reload hay quan sát policy.
- Không triển khai multi-region replication/consensus.
- Không thay đổi business policy semantics của module khác trong spec này.
- Không mở rộng phạm vi “cung cấp policy cho toàn bộ middleware” trong spec này.
- Không định nghĩa rule logic thực chiến cho `admin_cidr` và `rate_limit` trong spec này.

## 4. Thuật ngữ và định nghĩa
- `Policy SoT`: file YAML là nguồn sự thật duy nhất cho runtime policy.
- `Active Snapshot`: bản policy đang được process dùng để evaluate request.
- `Last-Known-Good`: snapshot gần nhất đã validate thành công.
- `Policy Source Adapter`: adapter đọc thay đổi source file theo từng môi trường.
- `Policy Propagation Notifier`: kênh event metadata để tăng tốc đồng bộ liên instance.
- `Convergence`: thời gian để toàn bộ instances trong region dùng cùng checksum.

## 5. Kiến trúc tổng thể
- Control plane (vận hành):
  - Kubernetes: ConfigMap projected file.
  - systemd: managed file path (`/etc/controlplane/policies/policy.yaml`).
  - Docker: bind-mounted YAML file.
- Data plane (runtime process):
  - `PolicyEngineCore` giữ snapshot in-memory.
  - `PolicySourceAdapter` phát hiện thay đổi file (watch/poll).
  - `PolicyPropagationNotifier` phát tín hiệu thay đổi liên instance (event-only, không mang full policy payload).
  - `PolicyValidator` validate schema + semantic constraints.
  - `SnapshotStore` thực hiện atomic swap.
- Boundary ownership:
  - `app/module`: chọn adapter theo runtime và inject vào policyengine.
  - `policyengine/service`: sở hữu load/validate/swap/fallback/metrics decision.
  - Không layer nào ngoài policyengine được phép ghi snapshot runtime policy.

## 6. Luồng xử lý
1. SRE publish YAML policy mới vào source file.
2. Adapter phát hiện thay đổi theo chiến lược tối ưu chống bottleneck:
   - `watch-first` (fsnotify hoặc tương đương) cho detection near real-time,
   - `poll-fallback` theo chu kỳ chậm hơn để self-heal khi miss event,
   - coalesce event theo checksum/meta để không parse lặp vô ích.
3. Engine đọc raw YAML -> parse -> validate version/schema/semantic.
4. Nếu hợp lệ:
   - tính checksum SHA-256,
   - tạo snapshot mới,
   - atomic swap `active_snapshot = new_snapshot`,
   - update `last_known_good`.
5. Nếu không hợp lệ:
   - reject snapshot mới,
   - giữ nguyên `active_snapshot`,
   - emit warning log + metrics failure.
6. Request path luôn đọc `active_snapshot` hiện tại, không thực hiện I/O file.

### 6.1 Fast Cross-Instance Propagation (near-instant)
1. Khi instance A apply snapshot mới thành công, A publish event `policyengine.policy.changed.v1`.
2. Event chỉ chứa metadata (`version`, `checksum`, `source_type`, `emitted_at`, `emitter_instance_id`).
3. Instance B/C nhận event:
   - nếu `checksum` khác `active_snapshot.checksum` -> trigger reload ngay từ YAML source local,
   - nếu trùng checksum -> bỏ qua.
4. Nếu miss event hoặc bus lag/down, watch/poll fallback vẫn self-heal và hội tụ state.
5. Event path chỉ là acceleration channel; YAML file vẫn là SoT duy nhất.

## 7. API Contract
> Không có HTTP API contract cho module này theo quyết định phạm vi.

### 7.1 Internal Service Contract
- Symbol/path: `policyengine/domain/service.EngineService`
- Owner layer: service

**`Current(ctx)`**
- Input keys:
  - `ctx` (required)
- Output keys:
  - `PolicySet` gồm `version`, `updated_at`, `source`, `checksum_sha`, `policies`
- Validation/invariants:
  - luôn trả snapshot immutable copy.
- Error mapping:
  - `ErrPolicyUnavailable` khi chưa có snapshot active.

**`Reload(ctx)`**
- Input keys:
  - `ctx` (required)
- Output keys:
  - `PolicySet` mới nếu reload thành công.
- Validation/invariants:
  - chỉ swap khi parse + validate pass.
  - nếu fail, snapshot active không đổi.
- Error mapping:
  - `ErrPolicyInvalid` khi schema/semantic fail.
  - `ErrPolicySourceUnavailable` khi đọc file lỗi.

### 7.2 Adapter Contract
- Symbol/path: `PolicySourceAdapter` (new contract, domain/service)
- Input keys:
  - `Start(ctx, onChange)`
  - `ReadCurrent(ctx)`
  - `SourceMeta()`
  - `DetectionMode()` -> `watch_first_poll_fallback`
- Output keys:
  - raw bytes + metadata (`path`, `mtime`, `size`)
- Invariants:
  - adapter không validate business policy; chỉ chịu trách nhiệm source detection/read.
  - Không spawn nhiều worker parse song song từ cùng một source event stream.
  - Chỉ giữ tối đa 1 pending reload event (latest-wins coalescing).
- Failure mapping:
  - transient read error -> retry loop, không clear active snapshot.
  - watch channel lỗi -> degrade sang poll-only mode + emit cảnh báo.

### 7.3 Propagation Event Contract
- Symbol/path: `PolicyPropagationNotifier` (new contract, service layer)
- Owner layer: service (adapter implementation ở infra)
- Event topic:
  - `policyengine.policy.changed.v1`
- Payload keys:
  - `version` (string, required)
  - `checksum` (string[64], required)
  - `source_type` (enum: `k8s_configmap|systemd_file|docker_bind`, required)
  - `emitted_at` (RFC3339 UTC, required)
  - `emitter_instance_id` (string, required)
- Validation/invariants:
  - không mang full policy payload.
  - consumer idempotent theo `checksum`.
  - duplicate/out-of-order event không làm rollback snapshot đã mới hơn.
- Failure/error mapping:
  - publish fail -> log warning + increment metric; không rollback local snapshot đã apply thành công.
  - subscribe lag/down -> auto fallback watch/poll.

## 8. Data Model
- Domain entity: `PolicySet`
  - `version` (string, required, allowed: `v1`)
  - `updated_at` (time, required)
  - `source` (string path/source-id, required)
  - `checksum_sha` (string[64], required)
  - `policies` (map[string]interface{}, required, non-empty)
- Persistence model:
  - Không có persistence model trong DB cho runtime policy.
- Index/cardinality:
  - Không có DB index vì policy không lưu DB.
  - In-memory chỉ giữ 2 snapshot tham chiếu chính: `active`, `last_known_good`.

## 9. Reliability & Failure Handling
- Retry strategy:
  - Adapter retry read theo watch-first + poll-fallback.
  - Poll fallback mặc định `10s` (không dùng `2s` như hot mode) để giảm CPU khi watch hoạt động bình thường.
  - Exponential backoff khi error burst (2s -> 4s -> 8s -> max 30s).
- Timeout budget:
  - Parse+validate per file update timeout: `<= 500ms` default, max `2s`.
- Backpressure policy:
  - Queue event cập nhật tối đa `1` pending event/instance (coalesce by checksum/meta).
  - Nếu event burst, drop intermediate events và xử lý latest state.
  - Single-flight reload worker: chỉ 1 goroutine parse/validate/swap.
  - Event consumer queue depth tối đa `1` (latest checksum wins).
- Consistency model:
  - Eventual consistency trong single-region.
- Degradation matrix:
  - File source unavailable: giữ active snapshot + log warn.
  - YAML invalid: reject + giữ active snapshot + increment failure metric.
  - Event bus down/lag: degrade sang watch/poll-only, vẫn hội tụ theo SLA fallback.
  - CPU pressure cao: kéo poll interval lên guardrail max theo config.
  - Process cold start chưa có snapshot: readiness fail (prod mode).
- Concurrency control:
  - 1 writer goroutine cho swap path.
  - Reads lock-free/low-lock qua immutable snapshot reference.
  - Pre-check 2 phase để giảm parse cost:
    - phase 1: check nhanh `mtime + size` (hoặc inode/version marker),
    - phase 2: chỉ khi phase 1 đổi thì mới read full file + hash + parse/validate.
  - Event-triggered reload và watch/poll-triggered reload dùng chung single-flight gate.

## 10. Security
- Authn/Authz:
  - Không có endpoint công khai cho reload trong spec này.
  - Publish policy file là quyền của SRE platform pipeline.
- Secret handling:
  - Policy file không chứa secret plaintext bắt buộc; nếu có key nhạy cảm phải mã hóa/được inject từ secret store khác.
- Logging policy:
  - Không log toàn bộ payload `policies`.
  - Chỉ log metadata: `version`, `checksum`, `source`, `size`, `result`.
- Audit:
  - Audit ở pipeline publish layer (GitOps/CI/job) + runtime reload event logs.

## 11. Scalability & Performance
- Throughput/CCU assumptions:
  - Baseline: 5k RPS/instance.
  - Expected: 25k RPS/instance.
  - Peak: 50k RPS/instance (short burst 1-3 phút).
- Single-region HA stance:
  - 3+ replicas, spread ít nhất 2 AZ trong cùng region.
  - Multi-region out-of-scope để giảm complexity consensus.
- Latency budget (hot path request evaluate policy):
  - Gateway: p95 5ms / p99 15ms.
  - App routing+auth: p95 10ms / p99 30ms.
  - Policy lookup (in-memory): p95 < 0.2ms / p99 < 0.5ms.
  - Total app-side budget (profile peak 50k): p95 <= 25ms, p99 <= 80ms.
- Hot path complexity:
  - Policy lookup O(1) map access + pointer snapshot.
  - Cấm scan/list toàn bộ policy map trong request hot path.
- Capacity guardrails:
  - Watch mode là primary; poll fallback default 10s, min 2s, max 30s.
  - Nếu poll-only mode, default poll 2s, min 500ms, max 10s.
  - Worker concurrency cho reload path: 1.
  - Max YAML file size mặc định: 1MB; hard cap 2MB.
  - Parse timeout budget mỗi lần reload: 500ms default, hard timeout 2s.
  - CPU isolation: reload worker tách budget CPU khỏi request worker.
  - HPA: scale theo CPU + request latency + `policyengine_reload_queue_depth`.

## 12. SLO / SLA
- Availability SLO (policy read capability): `99.95%` theo tháng.
- Reload success SLO: `>= 99.9%` valid policy updates được apply thành công/30 ngày.
- Reload detection SLA (per instance): p95 `<= 2s`, p99 `<= 5s`.
- Cross-instance convergence SLA (single-region): p95 `<= 10s`, p99 `<= 20s` ở profile peak 50k.
- Error budget:
  - Availability budget: 21m54s/tháng.
  - Burn-rate alerts:
    - fast burn: 14x trong 5 phút.
    - slow burn: 2x trong 1 giờ.

## 13. Source of Truth (SoT) by Caller
- Caller: request runtime (`admin_cidr`, `rate_limit`)
  - SoT: `active_snapshot` in-memory trong process.
  - Fallback: fail-closed cho critical enforcement; fallback last-known-good cho non-critical checks.
  - Conflict resolution: `active_snapshot` ưu tiên tuyệt đối; chỉ đổi qua validated swap.
- Caller: policyengine reload worker
  - SoT: YAML file source qua adapter môi trường.
  - Fallback: giữ `last_known_good` khi source lỗi/invalid.
  - Conflict resolution: checksum mới nhất thắng; duplicate checksum bỏ qua.
- Caller: policy propagation notifier consumer
  - SoT: event metadata chỉ để trigger reload, không làm SoT payload.
  - Fallback: nếu event unavailable, dùng watch/poll path.
  - Conflict resolution: idempotent theo checksum; stale/out-of-order event bị drop.
- Caller: SRE observability
  - SoT: logs + metrics emitted bởi policyengine runtime.
  - Fallback: nếu metrics lag, dùng logs structured theo checksum/version.

## 14. Observability
- Metrics bắt buộc (tối thiểu, tránh nhiễu):
  - `policyengine_reload_total{result}` (`success|failed`)  
    -> đo tỷ lệ reload thành công/thất bại.
  - `policyengine_reload_duration_seconds`  
    -> đo latency reload để bám SLO apply-time.
  - `policyengine_active_version_info{version,checksum}` (gauge=1)  
    -> xác định policy đang active trên từng instance.
  - `policyengine_snapshot_age_seconds`  
    -> phát hiện policy quá cũ/stale.
  - `policyengine_propagation_lag_seconds`  
    -> đo độ trễ hội tụ cross-instance.
- Log contract (theo `pkg/logger/logger.go`, ưu tiên `logger.SysInfoFields` / `logger.SysWarnFields` / `logger.SysErrorFields`):
  - Dùng `log_type=system` và `op` làm khóa chính để quan sát.
  - Chỉ log 4 event quan trọng:
    - `op=policyengine.reload.success` (Info): reload thành công, snapshot đổi.
    - `op=policyengine.reload.failed` (Warn): reload lỗi, giữ `last_known_good`.
    - `op=policyengine.source.degraded` (Warn): watch lỗi, degrade sang poll.
    - `op=policyengine.propagation.failed` (Warn): publish/consume event lỗi.
  - Field bắt buộc tối thiểu cho các event trên:
    - `source_type`, `source_mode`, `version`, `checksum`, `trigger`.
  - Field optional khi có ngữ cảnh lỗi:
    - `reason`, `error` (được logger attach), `emitter_instance_id`, `propagation_lag_ms`.
  - Không log các event nhiễu cao (`reload_attempt`, `snapshot_unchanged`, duplicate consume) ở mức Info/Warn; các trường hợp này chỉ phản ánh qua metrics.
- Alerts:
  - `policyengine_reload_total{result="failed"}` >= 5 trong cửa sổ 10 phút.
  - `policyengine_snapshot_age_seconds` > 86,400s trong 15 phút liên tục.
  - `policyengine_active_version_info` mismatch giữa replicas kéo dài > 10 phút.
  - `policyengine_propagation_lag_seconds` p95 > 2s trong 5 phút liên tục.

## 15. Deployment & Rollout
- Rollout pattern single-region HA:
  - Rolling update với `maxUnavailable=1`, `maxSurge=1`.
  - Canary 10% replicas trước 10 phút, sau đó full rollout.
- Blast radius control:
  - Validate policy trong CI trước khi publish.
  - Enforce size/schema gate trước khi file được apply.
  - ConfigMap update contract (bắt buộc):
    - Pipeline chỉ publish policy theo cơ chế atomic của Kubernetes API (`kubectl apply`/server-side apply), không patch thủ công từng phần dữ liệu file trên node.
    - Không cho phép workflow ghi trực tiếp vào projected file path trong container.
    - Mỗi lần publish phải mang version/checksum mới và có bước verify sau apply.
    - Nếu verify thất bại, rollback bằng cách re-apply revision ConfigMap trước đó.
- Runtime rollout:
  - Khi publish YAML mới: không restart pod; engine tự reload.
  - Khi có notifier event: instance reload ngay theo event trigger nếu checksum mới.
- Rollback:
  - Re-apply revision file/checksum trước đó.
  - Verify qua `policyengine_active_version_info` + logs.

## 16. Testing Strategy
- Unit tests:
  - validator (schema/semantic/version), checksum logic, swap invariants.
  - failure path: invalid yaml, empty policies, unsupported version.
- Integration tests:
  - adapter + engine end-to-end với temp file updates.
  - propagation notifier consume/publish idempotency.
  - assert no active snapshot regression khi reload fail.
- Load tests (high-CCU):
  - steady: 10k RPS trong 30 phút.
  - spike: 10k -> 50k RPS trong 2 phút.
  - recovery: về 10k RPS và verify no policy lookup degradation.
- Failure drills:
  - file permission denied.
  - partial/truncated file write.
  - repeated invalid policy publish.
  - simulated node-level config propagation delay.
  - watch channel close/error -> verify auto degrade poll fallback.
  - event bus unavailable/lag/duplicate/out-of-order -> verify idempotency + fallback convergence.

## 17. Quyết định chốt cho triển khai hiện tại
- Notifier backend mặc định cho sync cross-instance: `Redis Pub/Sub`.
- `max YAML size` production chốt: `1MB` (giữ cấu hình lean; sẽ đánh giá tăng sau khi có số liệu vận hành thực tế).
- Mapping fail-closed/fail-open và policy semantics chi tiết cho `admin_cidr` / `rate_limit` sẽ nằm ở idea/spec riêng; bản spec này chỉ định contract runtime hot reload.
