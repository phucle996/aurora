# Idea: Runtime Policyization cho RateLimit + Decision Engine v1

## 1) Bối cảnh
Hiện tại `ratelimiter` và `internal/security/ratelimit` đã có cơ chế enforce khá đầy đủ, nhưng nhiều tham số vận hành đang hardcode trong code:
- admission baseline (`capacity/refill/period`),
- escalation state (`throttle/isolation/block` TTL + threshold + window),
- bypass endpoints,
- log sampling theo decision,
- local engine guard (`max keys`, eviction scan limit).

Hệ quả:
- Mỗi lần tune phải đổi code + redeploy.
- Khó tách vận hành policy với vòng đời code.
- Dễ drift giữa spec vận hành và runtime behavior thực tế.

## 2) Ý tưởng chính
Đưa các tham số vận hành của rate-limit vào runtime policy (YAML), đọc qua policyengine hiện có để:
- tune nhanh không đổi business logic,
- giữ một SoT policy cho security admission,
- tách rành mạch: code = enforcement mechanics, policy = vận hành threshold/TTL/sampling.

## 3) Mục tiêu
- Policy hóa các tham số quan trọng nhất của `RateLimitPreAuth`, `RateLimitPostAuth`, `DecisionEngine`.
- Giữ backward-safe: nếu thiếu key policy thì fallback về default hiện tại.
- Không đổi contract response public của middleware.

## 4) Phạm vi
### Trong phạm vi
- Policy keys cho:
  - `capacity/refill/period` theo policy class,
  - escalation params (`throttle_ttl`, `isolation_ttl`, `block_ttl`, `escalation_window`, `block_threshold`),
  - bypass route patterns,
  - sampling percent theo decision,
  - local engine guards (`max_keys`, `evict_scan_limit`).
- Parse typed policy từ `policyengine/runtime/configyaml`.
- Inject policy runtime snapshot vào middleware/ratelimit khi init app.

### Ngoài phạm vi
- Không thiết kế risk-engine mới.
- Không đổi IAM business logic.
- Không thay đổi semantics HTTP lỗi public.

## 5) Các phương án khả thi

### Option A — Hardcode giữ nguyên (baseline hiện tại)
- Ưu điểm: zero implementation cost.
- Nhược điểm: vận hành chậm, phải redeploy để tune.
- Rủi ro: incident response chậm, drift cao giữa docs-code.
- Effort: S.

### Option B — Policy hóa đầy đủ key vận hành (khuyến nghị)
- Ưu điểm: tune nhanh, SoT rõ, giảm code churn.
- Nhược điểm: cần thêm parse/validation runtime policy.
- Rủi ro: cấu hình sai policy có thể tăng false positive.
- Effort: M.

### Option C — Chỉ policy hóa admission rate, giữ escalation hardcode
- Ưu điểm: scope nhỏ, triển khai nhanh.
- Nhược điểm: chưa giải quyết tuning escalation/sampling khi incident.
- Rủi ro: vận hành vẫn phải sửa code cho block/isolation behavior.
- Effort: S-M.

## 6) Trade-off matrix + khuyến nghị

| Tiêu chí | A | B | C |
|---|---|---|---|
| Time-to-deliver | Tốt nhất | Trung bình | Tốt |
| Vận hành/tune runtime | Kém | Tốt nhất | Trung bình |
| Boundary clarity | Trung bình | Tốt | Trung bình |
| SoT alignment | Kém | Tốt nhất | Trung bình |
| Incident agility | Kém | Tốt nhất | Trung bình |

Khuyến nghị: **Option B**.
Lý do: cân bằng tốt giữa effort và lợi ích vận hành, phù hợp hướng policyengine runtime-provider vừa refactor.

## 7) Xác minh khả thi theo codebase
- Có sẵn policy runtime path:
  - `internal/policyengine/runtime/configyaml/policies.go`
  - `runtime/policies/policy.yaml`
- Có sẵn điểm enforce:
  - `internal/http/middleware/ratelimiter.go`
  - `internal/security/ratelimit/decision_engine.go`
- Có sẵn module wiring policy snapshot:
  - `internal/policyengine/module.go`
  - `internal/app/module.go`

Kết luận: **FEASIBLE_NOW**.

## 8) Ràng buộc và giả định
- Redis vẫn là backend limiter hiện tại.
- Thiếu policy key -> fallback default hiện tại để không break runtime.
- Rollout theo phase để tránh blast radius.

## 9) Tiêu chí hoàn thành
- Tất cả key vận hành quan trọng có mặt trong policy schema typed.
- Middleware/decision engine đọc policy thay vì hardcode ở các điểm chính.
- Có test cho fallback/default + invalid policy handling.

## 10) Câu hỏi mở
1. Có cần tách `preauth` và `postauth` thành policy class riêng per-route group ngay v1 không?
2. Bypass list áp dụng pattern-level hay exact route-level?
3. Có cho phép config fail-open theo route class hay giữ fail-closed toàn cục?
