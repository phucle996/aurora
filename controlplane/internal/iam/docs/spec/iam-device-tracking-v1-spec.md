# IAM Device Tracking V1 - Behavior Spec

## Requirement Source
- Primary requirement: `REQ-IAM-DEVICE-TRACKING-V1`
- SoT: `IAM Device Registry` for trust/lifecycle, `Session+Refresh` for validity.

## Scope
- Quy định hành vi nhận diện, theo dõi, phân loại và revoke thiết bị trong IAM.
- Áp dụng cho login/refresh/self-service device management.

## Non-Goals
- Không định nghĩa anti-fraud fingerprint nâng cao.
- Không bao gồm ML risk scoring.
- Không thay đổi auth factor architecture hiện tại.

## Actors / Roles and Permission Boundary
- `end_user`
  - Được xem/revoke thiết bị của chính mình.
  - Không được thao tác lên thiết bị của user khác.
- `security_admin`
  - Được tra cứu timeline theo policy RBAC.
  - Quyền revoke thay user là optional theo rollout flag.
- `iam_system`
  - Resolve identity thiết bị, evaluate policy, enforce transition.

## Domain States
- `new`: thiết bị mới phát hiện.
- `recognized`: thiết bị đã biết, chưa đạt trusted.
- `trusted`: thiết bị đạt điều kiện trust policy.
- `suspicious`: thiết bị có tín hiệu bất thường.
- `revoked`: thiết bị bị thu hồi, không được tiếp tục phiên.

## State Transition Rules
- `new -> recognized`: lần đăng nhập hợp lệ tiếp theo và không có risk blocker.
- `recognized -> trusted`: đạt trust policy threshold/challenge requirement.
- `* -> suspicious`: xuất hiện risk condition vượt ngưỡng policy.
- `* -> revoked`: user/admin action revoke hoặc security action.
- `revoked` là terminal cho continuation flow; chỉ có thể quay lại qua re-enroll policy (ngoài scope V1).

## Main Behavior Flow
1. Authentication success (credential phase pass).
2. Collect device envelope (device_id, ua snapshot, ip, optional hints).
3. Validate envelope tối thiểu.
4. Resolve device record:
   - chưa có -> create state `new`.
   - đã có -> update heartbeat fields.
5. Evaluate risk/trust policy.
6. Enforce:
   - allow session/refresh issuance, hoặc
   - require re-auth/challenge theo policy.
7. Persist transition + audit event.

## Exception Flow
- EX-001 Invalid Device Envelope
  - Behavior: không crash flow; áp degraded policy, gắn `low_confidence`.
  - User-visible: generic auth/device message (không lộ rule nội bộ).
  - Audit: bắt buộc lưu failure context.

- EX-002 Revoked Device Continuation
  - Behavior: chặn refresh/session continuation từ device `revoked`.
  - User-visible: yêu cầu đăng nhập lại.
  - Audit: `device_continuation_denied_revoked`.

- EX-003 Permission Violation
  - Behavior: user không thể thao tác device không thuộc ownership.
  - User-visible: generic forbidden/denied.
  - Audit: ghi access denied event.

## Edge Cases
- ED-001 Concurrent login cùng device identity
  - Expectation: idempotent upsert, không duplicate device.
- ED-002 Fingerprint drift nhẹ do browser update
  - Expectation: policy-based tolerance, không auto revoke.
- ED-003 Missing optional device hints
  - Expectation: giảm confidence, không fail cứng.

## Error / Response Semantics (Behavior-level)
- Generic error principle cho các case permission/existence.
- Không trả thông tin giúp suy luận user/device tồn tại ngoài quyền.
- Revoked continuation phải có response semantic rõ: session cannot continue + re-auth required.

## Business Rule References
- `BR-001` Device identity rule.
- `BR-002` Trust state promotion rule.
- `BR-003` Revocation precedence.
- `BR-004` Data minimization (no raw secret/token).
- `BR-005` Mandatory audit on every transition.
- `BR-006` Generic error policy.

## Acceptance Mapping (Traceability)
- `AC-001` -> Main Flow steps 2-4, BR-001, BR-005.
- `AC-002` -> Main Flow steps 4 & 7, BR-005.
- `AC-003` -> EX-003, BR-006.
- `AC-004` -> EX-001, BR-002, BR-005.
- `AC-005` -> EX-002 (expired variant), session validity SoT.
- `AC-006` -> EX-002, BR-003.

## Downstream Handoff Fields
- requirement_id: `REQ-IAM-DEVICE-TRACKING-V1`
- actor_role_matrix: `end_user`, `security_admin`, `iam_system`
- rule_exception_pack: `BR-001..BR-006`, `EX-001..EX-003`, `ED-001..ED-003`
- acceptance_pack: `AC-001..AC-006`
- open_questions_assumptions:
  - trusted TTL value
  - revoke immediate-kill semantics
  - admin delegated revoke scope
  - degraded mode default policy
