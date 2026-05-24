# Controlplane Resilience Circuit Breaker v1 Spec

## 1) Mục tiêu

Định nghĩa circuit breaker như một **shared resilience substrate** cho control plane, không bị nhốt trong một middleware hay domain cụ thể.

Mục tiêu:
- Ngăn cascading failure khi dependency chậm/lỗi/quá tải.
- Hỗ trợ graceful degradation thay vì hard fail đồng loạt.
- Đồng bộ ngữ nghĩa vận hành giữa admission, abuse control, risk engine và các dependency khác.

## 2) Phạm vi

### Trong phạm vi
- Breaker contract dùng chung cho các module:
  - IAM auth,
  - RateLimit,
  - Risk engine,
  - outbox,
  - KMS/HSM,
  - policy engine,
  - admin APIs,
  - queue/webhook/discovery integrations.
- Failure semantics, mode semantics, half-open probing, retry coordination.
- Integration points với admission pressure model.
- Observability contract thống nhất.

### Ngoài phạm vi
- Không thay thế admission/rate-limit logic.
- Không thay thế risk scoring model.
- Không định nghĩa chi tiết business policy IAM.

## 3) Kiến trúc vai trò

Circuit breaker là resilience primitive nằm dưới lớp admission/abuse control:

- Admission control gọi breaker để đọc dependency pressure.
- Abuse/risk layers dùng breaker để degrade enrichment khi dependency lỗi.
- Breaker phát signal chuẩn hóa cho observability và auto-tuning.

## 4) Failure semantics

```go
type FailureType string

const (
    FailureTimeout            FailureType = "timeout"
    FailureSaturation         FailureType = "saturation"
    FailureTransportError     FailureType = "transport_error"
    FailureDependencyOverload FailureType = "dependency_overload"
    FailureSemanticFailure    FailureType = "semantic_failure"
    FailureLocalPressure      FailureType = "local_pressure"
)
```

Yêu cầu:
- Mỗi failure event phải gắn `dependency` và `route_class`.
- Semantic failure dùng cho response invalid/contract broken, không dùng cho auth deny thông thường.

## 5) Breaker state & mode contract

### 5.1 State

```go
type BreakerState string

const (
    StateClosed   BreakerState = "closed"
    StateOpen     BreakerState = "open"
    StateHalfOpen BreakerState = "half_open"
)
```

### 5.2 Mode

```go
type BreakerMode string

const (
    BreakerModeObserve    BreakerMode = "observe"
    BreakerModeSoftReject BreakerMode = "soft_reject"
    BreakerModeHardReject BreakerMode = "hard_reject"
    BreakerModeDegraded   BreakerMode = "degraded"
    BreakerModeFailOpen   BreakerMode = "fail_open"
    BreakerModeFailClosed BreakerMode = "fail_closed"
)
```

Mode chọn theo dependency criticality:
- metrics/logging: fail-open/degraded
- auth signing/critical policy: fail-closed/hard-reject
- risk enrichment: degraded/soft-reject

## 6) Policy contract

```go
type BreakerPolicy struct {
    FailureThreshold    float64
    RecoveryWindow      time.Duration
    HalfOpenTokens      int
    RetryBudgetPerSec   int
    DegradedModeEnabled bool
    FailMode            BreakerMode
}
```

Ràng buộc:
- Policy phải cấu hình theo dependency + route class.
- Không dùng một global policy cho mọi dependency.

## 7) Adaptive trigger model

Breaker trigger không chỉ dựa fixed threshold; cần kết hợp:
- latency EWMA,
- error ratio EWMA,
- inflight/concurrency saturation,
- queue pressure.

```text
pressure_score = w1*EWMA(latency) + w2*EWMA(error_ratio) + w3*EWMA(inflight) + w4*EWMA(queue)
```

Hysteresis bắt buộc:
- enter open/degraded ở ngưỡng cao,
- exit ở ngưỡng thấp hơn để tránh flapping.

## 8) Hierarchical semantics

Breaker phải hỗ trợ nhiều scope:
- request-level
- route-level
- dependency-level
- node-level
- region-level (coarse, eventual)

Nguyên tắc:
- hot-path ưu tiên node-local autonomy,
- region/global chỉ sync coarse state bất đồng bộ.

## 9) Half-open probing semantics

Half-open recovery phải có giới hạn:
- token-limited probes,
- jittered recovery,
- probabilistic probe gating.

Cấm mở hoàn toàn traffic ngay khi vừa rời `open`.

## 10) Retry coordination contract

Breaker và retry layer phải phối hợp qua retry budget:
- khi breaker open -> retry budget giảm mạnh,
- khi half-open -> chỉ cho retry theo probe tokens,
- tránh retry storm gây self-DDoS.

## 11) Brownout / degraded mode

Hỗ trợ partial capability degradation:
- disable expensive audit branches,
- disable non-critical risk enrichment,
- fallback cached/static policy,
- giữ core auth path sống.

Không biến degraded mode thành silent data loss: phải audit rõ feature nào đang tắt.

## 12) Admission integration

Breaker state/pressure phải feed vào admission model:
- `ClusterPressure`
- `RequestCost` multipliers
- `EffectiveCapacity`

Ví dụ:
- Redis breaker pressure tăng -> auth route `RequestCost` tăng -> throttle sớm hơn.

## 13) Distributed coordination semantics

- Không yêu cầu exact sync breaker state toàn cluster.
- Node-local state là source of truth cho hot-path.
- Region-level sharing theo eventual consistency.
- Conflict resolution ưu tiên severity + freshness window (bounded staleness).

## 14) Observability contract

### 14.1 Metrics
- `breaker_state{dependency,scope}`
- `breaker_transition_total{dependency,from,to,reason}`
- `breaker_reject_total{dependency,mode,route_class}`
- `breaker_halfopen_total{dependency}`
- `breaker_probe_latency_seconds{dependency}`
- `breaker_dependency_pressure{dependency}`

### 14.2 Logs
- `dependency`
- `scope`
- `breaker_state`
- `transition_reason`
- `latency_ewma`
- `error_ratio_ewma`
- `queue_pressure`
- `retry_budget`

## 15) Rollout (bám code hiện tại)

### Phase 1
- Gắn breaker cho dependency trọng yếu trong đường auth/controlplane:
  - Redis (ratelimit/risk),
  - DB auth-critical paths,
  - signing/KMS nếu có.

### Phase 2
- Mở rộng sang outbox/webhook/queue/policy engine/discovery.

## 16) Tiêu chí chấp nhận

- Breaker reusable across modules (không domain-coupled).
- Có đầy đủ failure semantics + mode semantics + half-open policy.
- Có retry coordination để tránh retry storm.
- Admission layer đọc được breaker pressure để adaptive capacity.
- Observability đủ cho forensic + operational tuning.

## 17) Open questions

- Breaker policy per dependency nên config static hay policy-service driven?
- Ngưỡng hysteresis mặc định cho từng dependency class là bao nhiêu?
- Region-level state share theo kênh nào để tối ưu latency/cost?
