# IAM Device Tracking - Full Idea (V1)

## Requirement ID
- `REQ-IAM-DEVICE-TRACKING-V1`

## Single Source of Truth
- Device trust and lifecycle SoT: `IAM Device Registry` (IAM domain only).
- Session validity SoT: `Session + Refresh Token state`.
- Audit SoT: `IAM audit events`.

## Problem Context
IAM cần theo dõi thiết bị theo từng phiên đăng nhập để:
- phát hiện đăng nhập bất thường,
- cho phép người dùng tự quản lý thiết bị,
- hỗ trợ revoke theo thiết bị,
- cung cấp audit trail cho điều tra bảo mật.

Hiện thiếu chuẩn chung về trạng thái thiết bị, quyền thao tác, và policy xử lý khi thiết bị rủi ro.

## Stakeholders & User Segments
- End User (account owner)
- IAM Security/Admin Ops
- Compliance/Audit
- IAM Engineering (owner module IAM)

## In Scope (V1)
- Định danh thiết bị ở mức business (device envelope + fingerprint hash).
- Vòng đời thiết bị: `new -> recognized -> trusted -> suspicious -> revoked`.
- Gắn sự kiện login/refresh/logout/revoke vào timeline theo device.
- User tự xem/revoke thiết bị của chính mình.
- Admin tra cứu audit theo device trong phạm vi được phân quyền.

## Out of Scope (V1)
- Device fingerprint anti-fraud nâng cao (canvas/audio/WebGL).
- ML risk scoring.
- Adaptive MFA đa nhà cung cấp theo thời gian thực.

## Functional Requirements
1. Hệ thống ghi nhận và resolve thiết bị trên mỗi lần login thành công.
2. Hệ thống cập nhật `last_seen_at`, IP gần nhất, user-agent snapshot cho thiết bị đã biết.
3. Hệ thống gắn trạng thái rủi ro và state transition theo rule catalog.
4. User chỉ được xem/revoke thiết bị thuộc chính tài khoản của mình.
5. Revoked device không được tiếp tục refresh phiên hiện hữu.
6. Mọi transition phải tạo audit event có correlation id.

## Non-Functional Requirements
- Security: không lưu raw token/secret; generic error không lộ thông tin định danh nhạy cảm.
- Auditability: đủ dữ liệu để truy vết actor, action, device, time, result.
- Reliability: upsert device idempotent khi có concurrent login cùng thiết bị.
- Performance: device resolve không làm suy giảm rõ rệt login latency.

## User Stories
- `US-001` As an end user, I want to view signed-in devices, so that I can detect unknown access.
- `US-002` As an end user, I want to revoke a specific device, so that I can block suspicious access.
- `US-003` As IAM system, I want to classify device trust state on login, so that policy can enforce risk control.
- `US-004` As security admin, I want to query device activity timeline, so that incident investigation is faster.

## Acceptance Criteria
### AC-001 (Happy Path: New Device)
- Given thông tin xác thực hợp lệ và device envelope hợp lệ
- When user login thành công
- Then IAM tạo mới device record state=`new` và ghi audit `device_detected`.

### AC-002 (Happy Path: Known Device)
- Given device đã tồn tại và chưa `revoked`
- When user login thành công
- Then IAM không tạo record mới, chỉ cập nhật `last_seen_at` và audit `device_recognized`.

### AC-003 (Permission Boundary)
- Given user A và device thuộc user B
- When user A yêu cầu revoke device của user B
- Then hệ thống từ chối theo policy quyền và trả lỗi generic.

### AC-004 (Negative: Invalid Envelope)
- Given thiếu trường bắt buộc trong device envelope
- When xử lý login
- Then hệ thống áp degraded policy, gắn risk flag `low_confidence`, vẫn ghi audit failure context.

### AC-005 (Expiry / Time Window)
- Given refresh token đã hết hạn
- When refresh từ bất kỳ device nào
- Then từ chối refresh, đánh dấu session invalid, ghi audit `refresh_denied_expired`.

### AC-006 (State Transition: Revoked)
- Given device state=`revoked`
- When client dùng refresh/session cũ của device đó
- Then từ chối tiếp tục phiên và yêu cầu re-auth.

## Business Rules
- `BR-001` Device Identity Rule
  - Statement: Device identity dựa trên `device_id` stable key + fingerprint hash.
  - Owner: System
  - Enforcement: Service/Policy

- `BR-002` Trust State Rule
  - Statement: Thiết bị chỉ chuyển `trusted` khi đạt điều kiện policy (challenge pass hoặc tín hiệu ổn định theo ngưỡng cấu hình).
  - Owner: Business + Security
  - Enforcement: Policy

- `BR-003` Revocation Precedence
  - Statement: `revoked` có ưu tiên cao nhất, ghi đè trạng thái trust khác.
  - Owner: Security
  - Enforcement: Service

- `BR-004` Data Minimization
  - Statement: Cấm lưu raw secret/token trong device registry.
  - Owner: Compliance
  - Enforcement: Data/Repository

- `BR-005` Audit Mandatory
  - Statement: Mọi state transition của device bắt buộc có audit event.
  - Owner: Security
  - Enforcement: Service

- `BR-006` Generic Error Policy
  - Statement: Không trả thông tin cho phép suy luận user/device tồn tại ngoài quyền gọi.
  - Owner: Security
  - Enforcement: Transport/Handler contract

## Flow Docs
### Main Flow
1. User login success.
2. IAM parse device envelope.
3. Resolve existing device theo SoT registry.
4. Apply policy và xác định state transition.
5. Issue/deny session theo policy.
6. Persist updates + emit audit.

### Exception Flow
- E1: Device envelope malformed -> degraded policy + risk flag + audit.
- E2: Device bị revoked -> deny refresh/session continuation + re-auth required.
- E3: Concurrent upsert conflict -> idempotent retry theo unique identity.

### Edge Cases
- Edge-1: IP thay đổi liên tục nhưng fingerprint ổn định -> tăng risk nhẹ, không auto revoke.
- Edge-2: Browser update làm fingerprint drift nhẹ -> giữ recognized nếu drift nằm trong ngưỡng policy.
- Edge-3: Missing optional signals (timezone/language) -> không fail cứng, chỉ giảm confidence.

## Dependencies
- Existing IAM login/refresh/logout flows.
- IAM audit event pipeline.
- Config source cho risk thresholds và trust TTL.

## Assumptions
- Có sẵn model refresh/session để liên kết device context.
- Có cơ chế permission check cho self-service vs admin operation.

## Open Questions / Decision Log
1. Degraded mode khi envelope lỗi: allow-with-risk hay deny-by-default theo tenant?
2. Trusted device TTL mặc định bao nhiêu?
3. Revoke device có terminate toàn bộ active sessions theo device ngay lập tức không?
4. Scope admin tra cứu/revoke hộ user trong V1 có bật không?

## Handoff Contract
- requirement_id: `REQ-IAM-DEVICE-TRACKING-V1`
- actors/roles: end_user, security_admin, iam_system
- rule_and_exception: `BR-001..BR-006`, exception `E1..E3`
- acceptance_criteria: `AC-001..AC-006`
- open_questions_assumptions: mục `Open Questions / Decision Log` + `Assumptions`
