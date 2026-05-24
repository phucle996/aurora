# Bối cảnh

Controlplane đã có nền tảng chống abuse cơ bản: rate limit theo route qua middleware, fail-closed khi Redis lỗi, và nhiều guard cho luồng admin/auth. Tuy nhiên trước bài toán anti-recon và DDoS phân tán, cơ chế rate-limit tĩnh theo route chưa đủ vì thiếu khả năng tổng hợp tín hiệu đa nguồn để ra quyết định theo rủi ro thực tế.

## Code Survey

- `controlplane/internal/security/ratelimit/bucket.go` (`Bucket.Allow`, fail-open/fail-closed)
- `controlplane/internal/security/ratelimit/stacked.go` (`Stacked.Allow`)
- `controlplane/internal/security/ratelimit/keys.go` (scope key builders: `ip/user/device/tenant`)
- `controlplane/internal/http/middleware/ratelimiter.go` (middleware rate-limit hiện tại)
- `controlplane/internal/iam/route.go` (gắn middleware theo route)
- `controlplane/internal/app/app.go` (`ratelimiter.SetFailOpen(false)`)
- `controlplane/internal/http/middleware/observability.go` (điểm tích hợp quan sát ở HTTP layer)
- `controlplane/internal/iam/metrics/` (nền tảng metrics module-level sẵn có)

# Ý tưởng chính

Thêm một risk engine trong `internal/security` để:
1) thu thập security signals từ middleware/auth flows,
2) tính risk score theo subject,
3) ra quyết định policy động (`allow` / `throttle` / `cooldown` / `block`),
4) audit đầy đủ lên metrics/log để hiển thị trên Grafana.

# Mục tiêu

- Tăng khả năng phát hiện recon và abuse phân tán (không chỉ burst cục bộ).
- Giảm bypass khi attacker thay IP hoặc phân tán traffic thấp tần.
- Có audit trail rõ ràng để vận hành: biết rule/signal nào dẫn đến decision nào.

# Phạm vi

## Trong phạm vi

- Tạo `internal/security/riskengine` (signal, scoring, decision).
- Tích hợp evaluate risk trong middleware per-route sau khi có request context.
- Lưu state/counter ngắn hạn bằng Redis để scoring theo cửa sổ thời gian.
- Emit metrics/log chuẩn hóa để dashboard Grafana theo route/subject/decision.

## Ngoài phạm vi

- Không thay thế toàn bộ rate-limit middleware hiện tại.
- Không thêm WAF/IDS ngoài hệ thống controlplane.
- Không redesign IAM business flows.

# Luồng nghiệp vụ dự kiến

1. Request vào route đã gắn middleware bảo vệ.
2. Middleware thu thập input runtime (`ip`, `tracking_device_id`, `user_id`, `route`, `status signal`).
3. Middleware gửi signal vào risk engine.
4. Risk engine cập nhật score theo subject (ưu tiên `ip+tracking_device_id`).
5. Risk engine trả decision hiện thời:
   - `allow`: cho qua,
   - `throttle`: áp rate-limit chặt hơn,
   - `cooldown`: chặn tạm thời TTL ngắn,
   - `block`: chặn mạnh TTL dài hơn.
6. Middleware áp decision và trả response envelope hiện có (`429/403/503` theo policy).
7. Emit metrics/log decision để Grafana/Loki hiển thị realtime và truy vết.

# Deep reasoning tổng hợp ý tưởng rời rạc

Nhu cầu “anti recon”, “chống DDoS phân tán”, “vận hành quan sát được trên Grafana” không nên giải bằng một lớp rate-limit cố định duy nhất. Cách phù hợp với codebase hiện tại là giữ middleware per-route (đang chạy ổn), bổ sung risk engine làm lớp trí tuệ phía sau để kết hợp nhiều tín hiệu và đưa decision động. Hướng này tránh phá boundary IAM/service, tận dụng được Redis và metrics hiện hữu, đồng thời giúp tuning theo dữ liệu thực tế thay vì hardcode ngưỡng tĩnh.

# Xác minh tính khả thi theo codebase hiện tại

- **Kết luận: FEASIBLE_WITH_REFACTOR**

- **Bằng chứng code: file/path liên quan**
  - Có sẵn token-bucket + Redis Lua để làm nền throttling: `internal/security/ratelimit/bucket.go`.
  - Có sẵn stacked abstraction để phối nhiều rule: `internal/security/ratelimit/stacked.go`.
  - Có key scopes để định danh subject đa chiều: `internal/security/ratelimit/keys.go`.
  - Middleware đang là điểm chặn theo route rõ ràng: `internal/http/middleware/ratelimiter.go`, `internal/iam/route.go`.
  - Runtime đã chốt fail-closed cho limiter: `internal/app/app.go`.
  - Hạ tầng metrics đã tồn tại ở IAM/module để mở rộng audit: `internal/iam/metrics/`.

- **Gap chính cần xử lý**
  1. Chưa có model signal chuẩn hóa và scoring engine tập trung trong `internal/security`.
  2. Chưa có decision contract thống nhất giữa risk engine và middleware.
  3. Chưa có metric set chuyên biệt cho security decision (score, reason, action, ttl).
  4. Chưa có policy tuning loop theo môi trường (staging/prod) để giảm false positive.

# Ràng buộc và giả định

- Redis tiếp tục là store runtime chính cho counter/score TTL.
- Fail-closed giữ nguyên: Redis evaluate lỗi thì chặn theo policy bảo mật.
- Subject ưu tiên là `ip+tracking_device_id` (runtime Redis), không dùng DB device PK.
- Rollout theo route critical trước (`/admin/auth/*`, `/api/v1/auth/*`), sau đó mở rộng dần.

# Tiêu chí hoàn thành

- Có risk engine nhận signal và trả decision policy động dùng được trong middleware.
- Có audit metrics/log đủ để dựng dashboard Grafana cho decision và top offenders.
- Có thể phân biệt tối thiểu 4 action: `allow`, `throttle`, `cooldown`, `block`.
- Không phá semantics nghiệp vụ hiện tại; chỉ tăng lớp chặn bảo mật ở edge.

# Câu hỏi mở

1. Score threshold ban đầu cho từng action nên khác nhau theo route class nào?
2. TTL mặc định cho `cooldown` và `block` theo từng nhóm route là bao nhiêu?
3. Decision `throttle` sẽ map thành profile rate-limit nào (capacity/refill/period) cho từng policy type?
4. Grafana dashboard chuẩn chốt theo Prometheus-only hay kết hợp Loki để xem reason chi tiết?
