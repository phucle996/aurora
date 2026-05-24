# Bối cảnh

Controlplane đã có rate limit theo route và fail-closed khi Redis lỗi. Tuy nhiên nếu chỉ chặn theo IP thì sẽ gây false positive cao trong mạng NAT (nhiều người dùng chung IP), và chống recon phân tán chưa hiệu quả.

## Code Survey

- `controlplane/internal/http/middleware/ratelimiter.go` (middleware rate-limit hiện tại)
- `controlplane/internal/security/ratelimit/bucket.go` (token bucket + fail-open/fail-closed)
- `controlplane/internal/security/ratelimit/stacked.go` (stacked rule evaluator)
- `controlplane/internal/security/ratelimit/keys.go` (key scopes)
- `controlplane/internal/iam/route.go` (gắn middleware theo route)
- `controlplane/internal/app/app.go` (`SetFailOpen(false)`)

# Ý tưởng chính

Giữ middleware per-route như hiện tại, nhưng đổi chiến lược rate limit sang **multi-dimensional + progressive enforcement**:
1) IP là guard thô với ngưỡng cao,
2) enforcement chính xác ưu tiên `ip+tracking_device_id` và `user_id` khi có,
3) action tăng dần: `allow -> throttle -> cooldown -> block`.

# Mục tiêu

- Giảm chặn oan nhiều user trong cùng mạng NAT.
- Tăng khả năng chống probing/recon phân tán.
- Không phá flow hiện tại; chỉ nâng logic trong middleware rate-limit.

# Phạm vi

## Trong phạm vi

- Nâng `RateLimit` hiện tại thành stacked multi-dimensional evaluator.
- Thêm contract progressive enforcement theo severity.
- Chuẩn hóa observability theo `rule_scope` và `decision`.

## Ngoài phạm vi

- Không thêm middleware function mới.
- Không đổi nghiệp vụ IAM handlers/services.
- Không đưa score engine đầy đủ vào spec này (đã có spec riêng).

# Luồng nghiệp vụ dự kiến

1. Route gắn `RateLimitPreAuth(...)` như hiện tại.
2. Middleware thu runtime input: `ip`, `route`, `tracking_device_id` (nếu có), `user_id` (nếu có).
3. Build stack rules theo thứ tự:
   - `ip` (ngưỡng cao, chống flood thô),
   - `route` (burst theo endpoint),
   - `ip+tracking_device_id` (ưu tiên enforcement khi có),
   - `user_id` (optional, nếu đã auth).
4. Evaluate stack và lấy rule fail đầu tiên.
5. Áp action theo mức độ:
   - nhẹ: `throttle`,
   - lặp lại: `cooldown`,
   - nghiêm trọng: `block`.
6. Trả response generic + headers chuẩn, emit metrics/log.

# Deep reasoning tổng hợp ý tưởng rời rạc

Nhu cầu “linh hoạt hơn để không chặn cả mạng” và “vẫn chặn tốt abuse” yêu cầu chuyển từ IP-only sang identity-aware rate limiting. IP vẫn cần để bảo vệ hạ tầng trước volumetric flood, nhưng nếu để IP làm tiêu chí chính sẽ tạo false positive cao trong môi trường NAT. Vì vậy mô hình đúng là IP guard thô + identity guard chính xác (device/user) + progressive action để tránh block cứng sớm.

# Xác minh tính khả thi theo codebase hiện tại

- **Kết luận: FEASIBLE_WITH_REFACTOR**

- **Bằng chứng code: file/path liên quan**
  - Có sẵn token bucket Redis Lua: `internal/security/ratelimit/bucket.go`.
  - Có sẵn stacked evaluator: `internal/security/ratelimit/stacked.go`.
  - Có key builders đa scope: `internal/security/ratelimit/keys.go`.
  - Middleware route-level hiện hữu: `internal/http/middleware/ratelimiter.go`.
  - Fail-closed đã bật: `internal/app/app.go`.

- **Gap chính cần xử lý**
  1. `RateLimit` hiện tại chưa build/evaluate stacked rules đa chiều.
  2. Chưa có contract action `throttle/cooldown/block` theo severity trong middleware rate-limit.
  3. Chưa có metric/log theo `rule_scope` để thấy vì sao bị chặn.

# Ràng buộc và giả định

- Redis là runtime store bắt buộc cho limiter.
- Fail-closed giữ nguyên khi Redis evaluate lỗi.
- `tracking_device_id` lấy từ runtime session context/cookie hợp lệ, không dùng DB device PK.

# Tiêu chí hoàn thành

- `RateLimit` vẫn dùng callsite hiện tại nhưng xử lý đa chiều.
- Giảm phụ thuộc IP-only; ưu tiên identity-based enforcement khi có context.
- Có quan sát được block/throttle theo `rule_scope`.
- Không tăng đáng kể false positive trong mạng NAT so với baseline IP-only.

# Câu hỏi mở

1. Threshold mặc định cho từng rule scope (`ip`, `route`, `ip+device`, `user`) là bao nhiêu?
2. Rule escalation từ `throttle` sang `cooldown/block` dùng cửa sổ thời gian nào?
3. Có bật `user_id` rule ngay phase 1 hay rollout sau khi auth context ổn định?
