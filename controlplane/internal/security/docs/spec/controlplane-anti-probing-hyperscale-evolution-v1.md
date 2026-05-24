# Controlplane Anti-Probing Hyperscale Evolution v1

## 1) Mục tiêu

Tài liệu này mô tả hướng kiến trúc khi hệ thống đạt mức:
- CCU cao,
- HA thực tế multi-instance,
- cloud edge / multi-region,
- hyperscale traffic.

> Spec này được dùng để triển khai ngay theo phase, với phạm vi runtime hiện tại: **single-region, multi-instance**.

### 1.1 Implementation status

- **CURRENT (triển khai ngay)**
  - Global pre-auth admission (`RateLimitPreAuth`) + per-route post-auth enforcement (`RateLimitPostAuth`).
  - Local deny fast-path cache (bounded size + bounded TTL + anti-poisoning).
  - Metrics + alerts cho cache pressure và backend degradation.
  - Enforcement ladder mặc định UX/cost-first: `throttle -> temporary isolation -> block`.
- **NEXT (pha kế tiếp trong cùng track)**
  - Admission pressure tuning bằng EWMA + hysteresis ở mức thực thi.
- **DEFERRED (chưa thuộc runtime hiện tại)**
  - Policy runtime hot-reload toàn app (atomic snapshot + last-good fallback).
  - Multi-region replication semantics.
  - Probabilistic detection primitives (CMS/Bloom/HLL).

Ghi chú scope:
- Policy hot-reload **không triển khai trong đợt hiện tại**; chỉ xem như hướng mở rộng sau khi runtime hiện tại ổn định.

## 2) Nguyên tắc kiến trúc

- Local-first hot path: quyết định admission càng local càng tốt.
- Approximate over exact: chấp nhận xấp xỉ để đổi lấy latency/availability.
- Async distributed sync: trạng thái toàn cục đồng bộ bất đồng bộ.
- Graceful degradation: backend phụ trợ lỗi không kéo sập auth plane.
- Edge autonomy: mỗi node/region tự bảo vệ được ngắn hạn.

## 3) Chuyển dịch trách nhiệm theo lớp

| Lớp | Trách nhiệm chính |
|---|---|
| Edge local | fast admission, adaptive concurrency, local reputation |
| Distributed shared state | coarse sync, eventual replication |
| Central intelligence | policy tuning, long-window correlation, forensic |

## 4) Redis ở hyperscale

- Redis không nằm synchronous hot-path cho mọi request.
- Redis dùng cho:
  - coarse coordination,
  - long-lived abuse memory,
  - async reputation replication.
- Hot-path ưu tiên local cache + local limiter.

## 5) Admission nâng cao

- Bổ sung adaptive concurrency limiter bên cạnh token bucket.
- Capacity hiệu dụng động theo pressure:

`effective_capacity = base_capacity * pressure_factor`

`pressure_factor` có thể lấy từ inflight, latency, queue depth, saturation.

### 5.1 Admission cost model (bắt buộc ở hyperscale)

```go
type RequestCost uint32

const (
    CostLight   RequestCost = 1
    CostMedium  RequestCost = 5
    CostHeavy   RequestCost = 20
    CostExtreme RequestCost = 100
)
```

- Inflight budget tính theo tổng cost:

`effective_inflight = Σ(request_cost)`

- Không dùng request count thuần để quyết định quá tải.

### 5.2 Priority-based load shedding

```go
type RoutePriorityClass string

const (
    RoutePriorityEssential  RoutePriorityClass = "essential"
    RoutePriorityNormal     RoutePriorityClass = "normal"
    RoutePriorityBestEffort RoutePriorityClass = "best_effort"
)
```

Trong overload:
- `essential`: giữ sống lâu nhất,
- `normal`: degrade có kiểm soát,
- `best_effort`: drop trước.

## 6) Approximate/probabilistic detection

Với cardinality rất cao, ưu tiên primitive xác suất cho anonymous/distributed abuse:
- Count-Min Sketch,
- Bloom Filter,
- HyperLogLog,
- probabilistic counters.

Không ép exact global counter cho mọi subject.

## 7) Multi-region strategy (DEFERRED)

> Hiện tại deployment scope là single-region, multi-instance. Mục này giữ như target architecture cho phase sau.

- Enforcement theo region-local.
- Replicate reputation/state async giữa regions.
- Tránh exact global window sync real-time.

### 7.1 Distributed reputation replication semantics

- Replication scope:
  - `block`: region-local mặc định, optional cross-region cho attack diện rộng.
  - `cooldown`: local/region-local, không bắt buộc global.
  - `throttle signals`: aggregate async, không replicate từng event thô.
- Merge semantics:
  - ưu tiên state severity cao hơn (`block > cooldown > throttle`).
  - conflict theo timestamp canonical từ source region + bounded staleness window.
- Không yêu cầu strong consistency toàn cục.

### 7.2 Clock & monotonic fallback semantics

- Bình thường: distributed windows dùng Redis/server time canonical.
- Degraded mode: cho phép local monotonic clock fallback với TTL ngắn hơn và confidence thấp hơn.
- Khi recovered: resync về canonical time, không kéo dài state từ degraded mode vượt max TTL policy.

## 7.3 Pressure aggregation model

- `cluster_pressure` phải được smooth bằng EWMA:

`pressure = EWMA(latency) + EWMA(queue_depth) + EWMA(inflight)`

- Hysteresis để tránh flapping:
  - enter overload khi `pressure >= 0.8`
  - leave overload khi `pressure <= 0.6`

## 8) Observability cost control

Sampling mặc định đề xuất:

| Event | Sampling |
|---|---|
| allow | 0.01% |
| throttle | 5% |
| cooldown | 50% |
| block | 100% |

## 9) Enforcement escalation roadmap (UX/cost-first)

`throttle -> temporary isolation -> block`

Ghi chú vận hành:
- Không dùng `PoW/CAPTCHA/MFA challenge` trong roadmap mặc định của controlplane anti-probing.
- Lý do: tăng độ phức tạp vận hành, tăng cost và có thể làm xấu UX trong khi hiệu quả không tương xứng cho bối cảnh hiện tại.
- Trọng tâm enforcement giữ ở local-first admission + identity-aware isolation + block có TTL/jitter.

### 9.1 Retry-storm mitigation khi release block/cooldown

- TTL release có jitter là bắt buộc.
- Thêm token warmup/slow-start refill sau giai đoạn block lớn.
- Có thể áp dụng probabilistic allow ngắn hạn ngay sau release để tránh synchronized retry burst.

## 10) Ranh giới với v1

- `controlplane-anti-probing-v1-spec.md` vẫn là base contract cho middleware/runtime behavior.
- Tài liệu này mở rộng thành execution spec theo phase cho mục tiêu scale-up hiện tại.

## 10.1 Acceptance criteria (triển khai hiện tại)

- Admission pre-auth chạy global, không double preauth ở route-level.
- Post-auth enforcement chạy per-route sau auth guard.
- Bypass endpoints health/metrics luôn reachable.
- Local deny-cache metrics/alerts hoạt động và có runbook annotations.
- Alert backend degradation (`backend_unavailable`) hoạt động với ngưỡng vận hành đã chốt.

## 11) On-call quick guide (critical vs warning)

Mục tiêu: giúp đội vận hành xử lý nhanh theo mức độ rủi ro, giảm thời gian triage khi có alert anti-probing/admission.

### 11.1 Khi alert là `critical`

- Hành động ngay theo on-call (không chờ giờ hành chính).
- Ưu tiên kiểm tra theo thứ tự:
  1. Dependency health: Redis/queue/network saturation, timeout, error burst.
  2. Admission pressure: inflight, queue depth, p95/p99 latency, reject/throttle surge.
  3. Blast radius: route/region nào bị ảnh hưởng nặng nhất.
- Biện pháp giảm tải tạm thời (incident mode):
  - tăng mức bảo vệ local-first,
  - siết best-effort traffic trước,
  - giữ đường `essential` ổn định,
  - bật policy degrade an toàn theo runbook.
- Escalate ngay khi:
  - lỗi kéo dài > 10-15 phút,
  - ảnh hưởng lan multi-region,
  - error budget burn rate cao.

### 11.2 Khi alert là `warning`

- Xử lý sớm trong giờ trực, theo dõi trend 15-30 phút trước khi can thiệp mạnh.
- So sánh với baseline:
  - cache hit ratio,
  - backend error ratio,
  - throttle/block distribution theo route/region.
- Nếu warning tăng dần hoặc đi kèm latency/error spike:
  - nâng mức ưu tiên,
  - chuẩn bị playbook chuyển sang critical handling.

### 11.3 Nguyên tắc chung

- Không rollback vội khi chưa xác định rõ failure domain.
- Mọi thay đổi policy trong incident phải:
  - ghi rõ `version/checksum`,
  - có thời hạn rollback,
  - lưu audit reason để hậu kiểm.
