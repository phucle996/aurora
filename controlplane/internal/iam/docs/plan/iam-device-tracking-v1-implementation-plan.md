# IAM Device Tracking V1 - Implementation Plan (Contract-First, Full Layer)

## 1) Mục tiêu triển khai

Triển khai đầy đủ Device Tracking V1 theo spec:
- `controlplane/internal/iam/docs/spec/iam-device-tracking-v1-spec.md`
- `controlplane/internal/iam/docs/idea/iam-device-tracking-full-idea.md`

Single Source of Truth:
- Device lifecycle/trust state: bảng `devices` (IAM schema).
- Session validity: `refresh_tokens` + user status.
- Audit history: `audit_events`.

Done definition:
- Có **device contract theo layer** (entity/repo/service/handler) rõ input/output/state/error.
- Có API user để xem/quản lý thiết bị.
- Có API logout đa thiết bị.
- Có contract để system can thiệp (`suspicious/revoked`) theo policy.
- Có audit event payload chuẩn để user/admin/system truy vết.

---

## 2) Scope lock

### 2.1 Must change
- Device state model + DB compatibility trên bảng `devices` hiện có.
- Domain contracts cho device management và session revocation.
- Repository queries cho list/detail/revoke/revoke-all/touch-last-seen.
- Service orchestration cho user-management + system-intervention + logout đa thiết bị.
- Handler + route cho APIs quản lý thiết bị.
- Audit event schema usage chuẩn hóa cho device actions.

### 2.2 Must not change
- Không tạo bảng `devices` mới.
- Không đưa SQL ra khỏi repository.
- Không thêm auth factor mới ngoài scope (MFA/challenge engine mới).
- Không thay đổi behavior endpoint không liên quan IAM device/session.

### 2.3 Acceptance gates
- User có thể liệt kê thiết bị và revoke đúng ownership.
- Logout đa thiết bị revoke đúng sessions theo policy.
- Refresh flow chặn continuation khi device `revoked`.
- System có đường can thiệp trạng thái thiết bị (`suspicious`, `revoked`) có audit.
- Error semantics generic, không lộ tồn tại resource ngoài quyền.

---

## 3) Device Contract by Layer (bắt buộc)

## 3.1 Domain Entity Contract

**File (SỬA)**
- `controlplane/internal/iam/domain/entity/device_auth.go`

**Contract**
- `DeviceStatus`: `new | recognized | trusted | suspicious | revoked`
- `Device`:
  - identity: `id`, `user_id`, `public_key_fingerprint`
  - lifecycle: `status`, `trusted_at`, `revoked_at`, `risk_flags`
  - activity: `last_seen_ip`, `last_seen_user_agent`, `last_seen_at`
- `DeviceActionActor` (THÊM): `user | security_admin | system`
- `DeviceActionReason` (THÊM): `user_revoke | bulk_logout | risk_policy | incident_response | manual_admin_action`

## 3.2 Repository Contract

**File (THÊM)**
- `controlplane/internal/iam/domain/repo/device_repo.go`

**Interface**
- `ListDevicesByUserID(ctx, userID, limit, offset) ([]Device, error)`
- `GetDeviceByIDAndUserID(ctx, deviceID, userID) (*Device, error)`
- `GetDeviceByID(ctx, deviceID) (*Device, error)` (system/admin path)
- `UpdateDeviceState(ctx, deviceID, fromStates, toState, reason, actor) error`
- `TouchDeviceLastSeen(ctx, deviceID, ip, userAgent, seenAt) error`
- `RevokeDevicesByUserID(ctx, userID, excludeDeviceID, reason, actor) (affected int64, error)`
- `CountActiveDevicesByUserID(ctx, userID) (int64, error)`

## 3.3 Service Contract

**File (THÊM)**
- `controlplane/internal/iam/domain/service/device_service.go`

**Interface**
- `ListMyDevices(ctx, userID, paging) (DeviceListResult, error)`
- `RevokeMyDevice(ctx, userID, deviceID, reason) error`
- `LogoutOtherDevices(ctx, userID, currentDeviceID) (revokedSessions int64, revokedDevices int64, error)`
- `LogoutAllDevices(ctx, userID) (revokedSessions int64, revokedDevices int64, error)`
- `MarkDeviceSuspicious(ctx, deviceID, reason, actorMeta) error` (system/admin intervention)
- `MarkDeviceRevoked(ctx, deviceID, reason, actorMeta) error` (system/admin intervention)

## 3.4 Handler/API Contract

**File (THÊM)**
- `controlplane/internal/iam/transport/http/handler/device_handler.go`

**User APIs**
- `GET /api/v1/me/devices`
  - 200: danh sách device + state + last_seen + risk_flags sanitized
  - 401/403/500: generic semantics
- `POST /api/v1/me/devices/:device_id/revoke`
  - 200/204: revoked
  - 404/403: generic denied/not-found semantics (không leak ownership)
- `POST /api/v1/me/devices/logout-others`
  - revoke toàn bộ session/device khác current device
- `POST /api/v1/me/devices/logout-all`
  - revoke mọi device + mọi refresh sessions user

**System/Admin APIs (phase-gated, optional route flag)**
- `POST /api/v1/admin/devices/:device_id/suspicious`
- `POST /api/v1/admin/devices/:device_id/revoke`

---

## 4) DB + Migration plan (sửa file hiện có, không thêm file mới)

## 4.1 Enums/Tables/Indexes

**Files (SỬA)**
- `controlplane/internal/iam/migrations/000001_iam_enums.up.sql`
- `controlplane/internal/iam/migrations/000002_iam_tables.up.sql`
- `controlplane/internal/iam/migrations/000003_iam_indexes.up.sql`
- down files tương ứng

**DB Contract**
- `devices.status` dùng state mới.
- `devices.risk_flags` lưu JSONB metadata rủi ro (không chứa secret/token raw).
- Reuse indexes hiện có, chỉ delta khi query pattern mới cần.

## 4.2 Query ownership
- Device list/filter/state update: chỉ repo layer.
- Session revocation theo device/user: repo refresh-token + device repo phối hợp trong tx boundary.

---

## 5) Repository implementation map

**Files (THÊM/SỬA)**
- `controlplane/internal/iam/repository/device_repo.go` (THÊM)
- `controlplane/internal/iam/repository/refresh_token_repo.go` (SỬA)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `ListDevicesByUserID` | THÊM | Chưa có user device list contract | Query pageable theo owner | User quản lý thiết bị |
| `UpdateDeviceState` | THÊM | Chưa có state transition repo contract | CAS-style update với `fromStates` guard | Tránh race transition |
| `RevokeDevicesByUserID` | THÊM | Chưa có bulk revoke device contract | Revoke nhiều device theo owner | Logout đa thiết bị |
| `DeleteRefreshTokensByUserAndDeviceScope` | THÊM | Chưa có bulk revoke session contract | Xóa refresh tokens theo scope | Chặn continuation |
| `GetRefreshTokenByHash` | SỬA | Chưa load đầy đủ device context | Include `device_id` for enforcement | Enforce AC-006 |

---

## 6) Service implementation map

**Files (THÊM/SỬA)**
- `controlplane/internal/iam/service/device_service.go` (THÊM)
- `controlplane/internal/iam/service/refresh_token_service.go` (SỬA)
- `controlplane/internal/iam/service/auth_service.go` (SỬA)

| Function | Change | Before | After | Impact |
|---|---|---|---|---|
| `ListMyDevices` | THÊM | Chưa có | Trả view model cho user | User visibility |
| `RevokeMyDevice` | THÊM | Chưa có | Owner revoke + revoke sessions + audit | Device self-management |
| `LogoutOtherDevices` | THÊM | Chưa có | Revoke all except current device | Multi-device logout |
| `LogoutAllDevices` | THÊM | Chưa có | Revoke all devices + all sessions | Emergency account cleanup |
| `MarkDeviceSuspicious` | THÊM | Chưa có | System/admin can thiệp trạng thái | Security intervention |
| `MarkDeviceRevoked` | THÊM | Chưa có | System/admin revoke flow | Incident response |
| `Refresh` | SỬA | Check session/user chủ yếu | Check cả device revoked/suspicious policy | Runtime parity |
| `Login` | SỬA | Chưa chuẩn hóa device touch/state transition | Touch last seen + transition `new->recognized` policy-based | Lifecycle completeness |

---

## 7) Handler + Route implementation map

**Files (THÊM/SỬA)**
- `controlplane/internal/iam/transport/http/handler/device_handler.go` (THÊM)
- `controlplane/internal/iam/route.go` (SỬA)
- `controlplane/internal/iam/module.go` (SỬA)

| Route | Handler | Auth boundary | Response semantics |
|---|---|---|---|
| `GET /api/v1/me/devices` | `DeviceHandler.ListMyDevices` | user session required | 200 + list |
| `POST /api/v1/me/devices/:device_id/revoke` | `DeviceHandler.RevokeMyDevice` | owner-only via service | 200/204 |
| `POST /api/v1/me/devices/logout-others` | `DeviceHandler.LogoutOtherDevices` | user session required | 200 + summary |
| `POST /api/v1/me/devices/logout-all` | `DeviceHandler.LogoutAllDevices` | user session required | 200 + summary |
| `POST /api/v1/admin/devices/:device_id/suspicious` | `DeviceHandler.MarkSuspicious` | admin RBAC | 200 |
| `POST /api/v1/admin/devices/:device_id/revoke` | `DeviceHandler.MarkRevoked` | admin RBAC | 200 |

---

## 8) Audit contract (user/admin/system lấy gì để quản lý/can thiệp)

Audit event template (ghi vào `audit_events`):
- `event`: `device.listed | device.revoked | device.marked_suspicious | device.logout_others | device.logout_all | device.refresh_denied_revoked`
- `actor_user_id`: user/admin id (nullable cho system job)
- `target_type`: `device | session_family | user_device_set`
- `target_id`: `device_id` hoặc `user_id` theo action
- `severity`: `info|warning|critical`
- `metadata`:
  - `device_status_before`
  - `device_status_after`
  - `reason`
  - `affected_refresh_tokens`
  - `affected_devices`
  - `request_id` / correlation id

User dùng audit để:
- xem lịch sử can thiệp thiết bị trong profile security page.

Admin/System dùng audit để:
- điều tra incident, xác định ai revoke khi nào, và phạm vi ảnh hưởng.

---

## 9) Trình tự implement (critical path)

1. Chốt domain contracts (entity/repo/service interface).
2. Implement repo layer cho device + refresh-token bulk revoke scope.
3. Implement service layer cho list/revoke/logout-others/logout-all/system-intervention.
4. Wire refresh/login enforcement với device lifecycle.
5. Implement handler + route + module wiring.
6. Đồng bộ docs flow (`login-flow.md`, `refresh-token-flow.md`, thêm `device-management-flow.md` nếu cần).

---

## 10) Security + Error semantics

- Generic errors cho unauthorized/not-found ownership-sensitive actions.
- Không trả thông tin khiến user A suy luận device của user B.
- Không log secret/token raw; `risk_flags` phải sanitize.
- `revoked` có precedence cao nhất cho continuation flow.

---

## 11) Known risks & mitigations

- Risk: bulk logout race với refresh đồng thời.
  - Mitigation: tx boundary + delete-by-hash/id guard + idempotent semantics.
- Risk: state transition conflict do concurrent intervention.
  - Mitigation: `fromStates` guard trong `UpdateDeviceState`.
- Risk: audit thiếu correlation gây khó điều tra.
  - Mitigation: bắt buộc request id/correlation trong metadata.

---

## 12) Open decisions cần chốt trước khi full rollout

1. `suspicious` có block refresh ngay hay chỉ step-up/challenge?
2. `logout-all` có revoke current device/session ngay lập tức hay trừ current request window?
3. Admin can thiệp device có bật ngay V1 hay phase-gated sau user self-service?
