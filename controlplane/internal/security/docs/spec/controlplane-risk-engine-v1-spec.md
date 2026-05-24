# Controlplane Risk Engine v1 Spec

## Implementation Status

- `CURRENT_CODE`:
  - Chưa có risk-engine runtime module/caller đầy đủ trong request path.
- `NEXT_INCREMENT`:
  - giữ spec này làm contract cho integration sau khi RateLimit pre/post-auth ổn định.
- `FUTURE_EVOLUTION`:
  - dynamic scoring, confidence model, challenge orchestration theo roadmap.

## 1) Mục tiêu

Thiết kế Risk Engine như một tính năng độc lập với rate-limit:
- Nhận security signals từ middleware/request flows.
- Tính điểm rủi ro theo subject runtime.
- Trả quyết định động: `allow | throttle | cooldown | block`.
- Audit đầy đủ để vận hành qua Grafana/Loki.

> Ghi chú phạm vi: Spec này **không** định nghĩa lại token-bucket/rate-limit mechanics; phần đó thuộc spec anti-probing/rate-limit riêng.

## 2) Phạm vi

### Trong phạm vi
- `internal/security/riskengine`:
  - signal contract,
  - score model,
  - decision contract,
  - Redis-backed runtime state,
  - audit contract.
- Tích hợp điểm gọi từ middleware để `Evaluate()` risk.
- Chuẩn metric/log cho monitoring và forensic.

### Ngoài phạm vi
- Không thay đổi business logic IAM service/repository.
- Không thay thế các guard bảo mật hiện có ở application layer.
- Không redesign rate-limit API hiện tại.

## 3) Kiến trúc logic

### 3.1 Thành phần
- `SignalIngestor`: chuẩn hóa sự kiện đầu vào từ edge.
- `ScoreStore`: lưu counter/score TTL trong Redis.
- `Scorer`: tính điểm tăng/giảm theo rule.
- `Decider`: map score + context -> decision.
- `Auditor`: emit metrics/log decision.

### 3.2 Subject model
- Subject chính: `ip+tracking_device_id`.
- Fallback:
  - nếu chưa có `tracking_device_id` => subject `ip`.
  - nếu có `user_id` hợp lệ => thêm signal dimension hỗ trợ correlation (không thay subject chính).
- `tracking_device_id` là runtime id từ phiên đã xác thực (Redis runtime), không dùng DB device PK.

### 3.3 Sync/Async execution model

- **Sync hot-path**: `Evaluate(...)` để trả decision realtime cho middleware.
- **Async enrichment**: correlation/reputation aggregation/forensic enrichment.
- Enforcement realtime không được phụ thuộc async enrichment để tránh kéo p99.

## 4) Contract kỹ thuật

### 4.0 Shared vocabulary (đồng bộ với RateLimit spec)

```go
type RouteClass string

const (
    RouteClassCritical   RouteClass = "critical"
    RouteClassStandard   RouteClass = "standard"
    RouteClassPrivileged RouteClass = "privileged"
    RouteClassMutation   RouteClass = "mutation"
    RouteClassPublic     RouteClass = "public"
)
```

- Risk engine SHOULD nhận `route_pattern` + `route_class` thay vì raw path.
- `route_pattern` phải là normalized route pattern (không dùng raw URL path).

### 4.1 Signal contract

```go
type SignalType string

const (
    SignalCredentialFailBurst SignalType = "credential_fail_burst"
    SignalSensitive404Probe   SignalType = "sensitive_404_probe"
    SignalNonceOrSigInvalid   SignalType = "nonce_or_signature_invalid"
    SignalRapidRetryPattern   SignalType = "rapid_retry_pattern"
    SignalRouteAbuseBurst     SignalType = "route_abuse_burst"
)

type Signal struct {
    Type             SignalType
    RoutePattern     string
    RouteClass       RouteClass
    Method           string
    IP               string
    TrackingDeviceID string
    UserID           string
    StatusCode       int
    At               time.Time
    Metadata         map[string]string
}
```

Yêu cầu:
- Không đưa secret/token raw vào `Metadata`.
- Signal phải idempotent ở mức ingestion window (tránh double-count do retry nội bộ).
- Sensitive-path related signals MUST dựa trên canonicalized path:
  - normalized path,
  - decoded path,
  - lowercase canonical form.

### 4.1.1 Identity confidence contract

```go
type IdentityConfidence string

const (
    ConfidenceLow      IdentityConfidence = "low"       // anonymous ip
    ConfidenceMedium   IdentityConfidence = "medium"    // signed device
    ConfidenceHigh     IdentityConfidence = "high"      // authenticated user
    ConfidenceVeryHigh IdentityConfidence = "very_high" // mfa/session step-up
)
```

- Signal và evaluate context SHOULD mang `IdentityConfidence` để scoring thích ứng.

### 4.2 Score contract

```go
type ScoreState struct {
    SubjectKey string
    Score      int64
    UpdatedAt  time.Time
}

type ScoreDelta struct {
    SignalType SignalType
    Delta      int64
    WindowTTL  time.Duration
}

type ScoringContext struct {
    RouteClass         RouteClass
    IdentityConfidence IdentityConfidence
    PressureFactor     float64
}
```

Quy tắc:
- Score tăng theo severity signal.
- Score decay theo thời gian (window-based decay).
- Redis key phải có TTL để tự thu hồi state inactive.

### 4.2.1 Dynamic scoring formula

`EffectiveDelta = BaseDelta * RouteSensitivity * IdentityConfidenceWeight * PressureFactor`

Ràng buộc:
- V1 có thể dùng hệ số tĩnh theo config.
- V2+ có thể chuyển sang adaptive weights theo control loop.

### 4.2.2 Decay model contract

Phải định nghĩa decay semantics theo nhóm hành vi:
- low-risk noise: fast decay,
- probing: medium decay,
- credential/critical abuse: slow decay.

Cho phép triển khai bằng exponential decay hoặc bucketed decay, nhưng semantics trên là bắt buộc.

### 4.3 Decision contract

```go
type Decision string

const (
    DecisionAllow    Decision = "allow"
    DecisionThrottle Decision = "throttle"
    DecisionCooldown Decision = "cooldown"
    DecisionBlock    Decision = "block"
)

type EvaluateInput struct {
    RoutePattern     string
    RouteClass       RouteClass
    Method           string
    IP               string
    TrackingDeviceID string
    UserID           string
}

type EvaluateResult struct {
    Decision Decision
    Reason   string
    Score    int64
    TTL      time.Duration
}

type FinalDecisionAttribution struct {
    Decision         Decision
    TriggerRuleScope string
    EscalationReason string
    RetryAfter       time.Duration
    TTL              time.Duration
}
```

Semantics:
- `allow`: không cưỡng chế thêm.
- `throttle`: yêu cầu middleware giảm ngưỡng xử lý hiện tại.
- `cooldown`: chặn tạm thời TTL ngắn (429/403 theo policy edge hiện hành).
- `block`: chặn mạnh TTL dài hơn cooldown.
- Risk engine KHÔNG tự trả HTTP; Risk engine trả attribution/recommendation để middleware map response.

### 4.3.1 Challenge escalation contract

```go
type ChallengeLevel string

const (
    ChallengeNone      ChallengeLevel = "none"
    ChallengeProofWork ChallengeLevel = "proof_of_work"
    ChallengeCaptcha   ChallengeLevel = "captcha"
    ChallengeMFA       ChallengeLevel = "mfa"
)

type ChallengeDirective struct {
    Level  ChallengeLevel
    Reason string
    TTL    time.Duration
}
```

- V1 có thể trả `ChallengeNone` mặc định.
- Contract giữ sẵn cho v2 challenge orchestration mà không phá interface.

## 5) Redis model

### 5.1 Key namespace (đề xuất)
- `risk:score:<subject_key>`
- `risk:signal:<subject_key>:<signal_type>`
- `risk:block:<subject_key>`

### 5.2 Runtime behavior
- Atomic update score/counter bằng Lua hoặc transaction-safe sequence.
- TTL bắt buộc cho mọi key runtime.
- Redis lỗi => fail-closed theo chính sách security đã chốt ở edge.
- Distributed window/TTL MUST dùng Redis server time làm canonical clock source.
- Lua/atomic eval MUST bounded complexity:
  - Max evaluated rules/request: `6`
  - Max windows/rule: `3`
  - Cấm unbounded iteration theo input runtime.

## 6) Observability & audit

### 6.1 Metrics bắt buộc
- `security_risk_signal_total{signal_type,route}`
- `security_risk_score_current{subject_type}`
- `security_risk_decision_total{decision,reason,route}`
- `security_risk_ttl_seconds{decision,route}`

### 6.2 Log fields bắt buộc
- `request_id`
- `subject_type`
- `subject_key_hash`
- `signal_type`
- `score_before`
- `score_after`
- `decision`
- `reason`
- `ttl_ms`

## 7) Integration contract với middleware

- Middleware gọi `IngestSignal(...)` khi có security-relevant outcome.
- Middleware gọi `Evaluate(...)` trước khi pass request vào handler.
- Middleware chịu trách nhiệm map `Decision` sang response status theo hợp đồng hiện có.
- Risk engine **không** tự ghi response HTTP.
- Middleware SHOULD log `FinalDecisionAttribution` fields để đồng bộ forensic giữa RateLimit và RiskEngine.

## 8) Rollout

### Phase 1 (critical surfaces)
- Áp dụng cho các route có `RouteClass=critical|privileged|mutation` trong implementation hiện tại.

### Phase 2
- Mở rộng các route còn lại theo mức nhạy cảm.

## 9) Tiêu chí chấp nhận

- Risk engine hoạt động độc lập với rate-limit core.
- Có đủ signal ingest + score + decision + audit pipeline.
- Có thể trả 4 trạng thái `allow/throttle/cooldown/block` theo score.
- Grafana hiển thị được trend signal, score và decision theo route.

## 10) Rủi ro và giảm thiểu

- False positive khi score delta quá cao:
  - rollout theo route critical trước, tune bằng dữ liệu thật.
- Redis pressure tăng:
  - TTL ngắn, key cardinality có kiểm soát, tránh metadata quá lớn.
- Noise log cao:
  - sampling với signal phổ biến, giữ full cho `cooldown/block`.

## 10.1 Evolution boundary (v1)

- V1 không bắt buộc cross-subject behavioral correlation.
- V1 ưu tiên deterministic scoring + clear attribution để rollout an toàn.
- Reputation graph/probabilistic detection thuộc roadmap v2+.

## 11) Open questions

- Score band ban đầu cho `throttle/cooldown/block` chốt theo baseline traffic prod nào?
- Mỗi signal dùng delta tĩnh hay delta động theo route class?
- Cooldown chuẩn hóa status code mặc định là `429` hay tách `403` cho nhánh nghi ngờ cao?
