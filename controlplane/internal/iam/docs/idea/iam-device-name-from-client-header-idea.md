---
id: iam-device-name-from-client-header
owner: iam team
approver: system-architecture
version: 0.3 (draft)
effective_date: TBD
next_review: TBD
status: idea
---

# Idea: Device naming + persistent client device id

## 1. Problem context
Hiện trạng:
- `device_name` đang hardcode:
  - admin login: `"admin-login-device"` (`controlplane/internal/iam/service/admin_api_key_service.go`).
  - user login: `"Web session"` (`controlplane/internal/iam/service/auth_service.go`).
- Identity của thiết bị giữa các lần đăng nhập đang không có khoá nào ổn định client-side:
  - server đoán bằng `(user_id, user_agent)` → sai khi nhiều máy cùng UA.
  - không thể tránh tạo record mới mỗi lần đăng nhập lại.
- Hệ quả:
  - `iam.devices` phình rác.
  - `GET /me/devices` hiển thị nhiều bản ghi không phân biệt được.
  - Audit/forensic và "logout other devices" hoạt động không tin cậy.

Mục tiêu (v1):
- Cho client gửi **hostname** qua HTTP header để server dùng làm `device_name` (UI/audit).
- Cho client gửi **persistent client device id** qua header để server dùng làm khoá identify khi login lại.
- Thiếu/invalid → fallback an toàn:
  - `device_name = "unknown device"`.
  - `client_device_id` thiếu → server sinh và bootstrap về client.

## 2. Stakeholders
- End user (cloud-ui).
- Admin (admin-ui, CLI).
- Security/audit ops.
- Backend IAM.

## 3. In scope / Out of scope

### In scope
- Login flow user và admin:
  - `POST /api/v1/auth/login`
  - `POST /admin/auth/login`
- Server-side validate + sanitize hostname.
- Persistent client device id (opaque), bootstrap nếu thiếu.
- Schema delta nhỏ trong `iam.devices`, `iam.admin_devices`.
- Fallback `device_name = "unknown device"` khi không có hint.

### Out of scope (idea này)
- Reverse DNS lookup từ IP server-side.
- WebAuthn/keypair (`public_key_fingerprint` thực) — sẽ là idea v2.
- API user-rename device (`PATCH /me/devices/:id`).
- UI rendering chi tiết ở cloud-ui/admin-ui (idea này chỉ định contract dữ liệu).

## 4. Functional requirements
- FR-1: Login flow đọc header `X-Device-Hostname` (chính), `X-Device-Name` (alias) cho hostname.
- FR-2: Login flow đọc header `X-Client-Device-Id` cho khoá identify thiết bị.
- FR-3: Sanitize hostname theo BR-002.
- FR-4: Identify thiết bị theo BR-007 (dùng `client_device_id`, KHÔNG dùng `device_name`).
- FR-5: Nếu `client_device_id` thiếu → server sinh UUID mới, bootstrap về client (BR-008).
- FR-6: Persist:
  - `iam.devices.client_device_id`, `iam.devices.device_name`.
  - `iam.admin_devices.client_device_id`, `device_name`.
- FR-7: Không log full hostname / client_device_id ở mức INFO (BR-005).

## 5. Non-functional requirements
- NFR-1 Privacy: hostname là claim từ client; không kèm PII bổ sung.
- NFR-2 Security: server không tin tuyệt đối hostname; chỉ dùng làm metadata UI/audit.
- NFR-3 Performance: header parse + sanitize O(1), không thêm DB call ngoài upsert hiện tại.
- NFR-4 Compatibility: client cũ không gửi header → fallback đúng `"unknown device"` + `client_device_id` được server cấp; không 4xx.
- NFR-5 Observability: metric label suy từ giá trị (vd `device_name=unknown` => server-default), KHÔNG persist source xuống DB. `client_device_id_provenance=client|server-bootstrap` chỉ ở metric label, không xuống DB.
- NFR-6 Anti-abuse: cap số `client_device_id` per user (BR-009).

## 6. Business rules

- BR-001 Header naming
  - Hostname header chính: `X-Device-Hostname`.
  - Alias chấp nhận: `X-Device-Name`.
  - Nếu cả hai có, ưu tiên `X-Device-Hostname`.

- BR-002 Sanitize hostname
  - Trim leading/trailing whitespace.
  - Loại ký tự control (`<0x20`, `=0x7F`).
  - Allowlist: `[A-Za-z0-9._-]`.
  - Max length: 64.
  - Nếu sau sanitize ngắn hơn 2 ký tự → invalid.

- BR-003 Hostname fallback
  - Hostname invalid/missing → `device_name = "unknown device"`.
  - Hostname valid → `device_name = sanitized` (theo BR-002).

- BR-004 Naming inference (no DB column)
  - V1 KHÔNG persist `device_name_source` xuống DB.
  - Suy luận trực tiếp từ giá trị: `device_name == "unknown device"` ⇒ server-default; ngược lại ⇒ client-supplied.
  - Khi v2 có user-rename, mới thêm cột `device_name_source` để phân biệt `user-edited`.

- BR-005 Privacy & log
  - Không log full hostname/client_device_id ở INFO.
  - Audit DB lưu hostname; client_device_id KHÔNG được log dạng plain (chỉ hash 8 ký tự cho debug).

- BR-006 Update naming
  - Khi đăng nhập lại với cùng `client_device_id` mà hostname thay đổi → update `device_name`.
  - Không tạo record mới chỉ vì đổi tên.

- BR-007 Device identity SoT (v1)
  - Khoá identify thiết bị = `(user_id, client_device_id)`.
  - KHÔNG dùng `device_name` để identify.
  - KHÔNG expose `iam.devices.id` (DB id) ra client/header.
  - Khi `public_key_fingerprint` được FE cung cấp thật ở v2, identity ưu tiên fingerprint, `client_device_id` còn lại làm soft-fallback.

- BR-008 Client device id bootstrap
  - Nếu request KHÔNG có `X-Client-Device-Id`:
    - Server sinh UUIDv7 → dùng làm `client_device_id` cho record vừa tạo.
    - Trả lại cho client qua:
      - cookie `client_device_id` (HttpOnly=false, Path="/", Secure=runtime.TLS, SameSite=Lax, MaxAge ~ 1 năm).
      - header response `X-Client-Device-Id` (để CLI/script đọc).
    - Provenance = `server-bootstrap`.
  - Client SHOULD lưu thêm vào `localStorage` (web) / file (CLI) để bền hơn cookie.

- BR-009 Cap per user
  - Mỗi user giữ tối đa 50 `client_device_id` đang active (status != revoked).
  - Khi vượt cap, evict device cũ nhất theo `last_seen_at`.
  - Evict = revoke + DEL runtime + insert audit `device.evicted_capacity`.

- BR-010 Identity precedence (v2 readiness)
  - Khi v2 có `public_key_fingerprint` thật:
    - Khoá identify = fingerprint.
    - `client_device_id` còn lại để bootstrap UI và làm soft-fallback khi fingerprint chưa có.

## 7. User stories
- US-1: Là user, tôi muốn thấy hostname máy thật trong "My devices".
- US-2: Là admin, tôi muốn thấy hostname admin client trong session log.
- US-3: Là client (CLI/script) → login vẫn thành công khi không gửi header nào.
- US-4: Là security ops, tôi muốn biết tên thiết bị do client cung cấp hay server tự gán.
- US-5: Là user, đăng nhập lại trên cùng máy không tạo record mới trong "My devices".
- US-6: Là user clear cookies/localStorage → coi là máy mới (acceptable tradeoff).

## 8. Acceptance criteria

### AC-1 Hostname happy path
- Given login với `X-Device-Hostname: GF63-Thin-11UC` và `X-Client-Device-Id: <known>`.
- Then `device_name="GF63-Thin-11UC"`.

### AC-2 Hostname missing
- Given login không có `X-Device-Hostname`/`X-Device-Name`.
- Then `device_name="unknown device"`.

### AC-3 Hostname invalid
- Given header `X-Device-Hostname: "../etc/passwd; rm -rf"` hoặc rỗng sau sanitize.
- Then `device_name="unknown device"`.

### AC-4 Hostname truncate
- Given header 200 ký tự valid → sau sanitize > 64.
- Then `device_name` = 64 ký tự đầu.

### AC-5 Admin alias
- Given admin login với `X-Device-Name: ops-laptop-01` và không có `X-Device-Hostname`.
- Then `admin_devices.device_name="ops-laptop-01"`.

### AC-6 Cả 2 hostname header
- Given client gửi cả `X-Device-Hostname: A` và `X-Device-Name: B`.
- Then `device_name="A"`.

### AC-7 Login lại với cùng client_device_id (identify đúng)
- Given device đã có record với `client_device_id=cdid-1`.
- When user login lại với `X-Client-Device-Id: cdid-1` và `X-Device-Hostname: laptop-01-renamed`.
- Then KHÔNG tạo record mới.
- And `device_name` được update sang `"laptop-01-renamed"`.
- And `iam.devices.id` (tracked_device_id) không đổi.

### AC-8 Login thiếu client_device_id (bootstrap)
- Given request không có `X-Client-Device-Id`.
- Then server sinh `client_device_id` mới và:
  - set cookie `client_device_id` (HttpOnly=false, Secure, SameSite=Lax, MaxAge ~ 1 năm).
  - set header response `X-Client-Device-Id`.
- And `iam.devices.client_device_id` = giá trị server vừa sinh.
- And metric label `client_device_id_provenance="server-bootstrap"` (không persist xuống DB).

### AC-9 Login với client_device_id của user khác (security)
- Given attacker gửi `X-Client-Device-Id` đang thuộc user khác.
- Then server không reuse record của user khác.
- And tạo record mới gắn với `(attacker_user_id, attacker_client_device_id)`; KHÔNG nhắc tới user kia trong response.

### AC-10 Client device id rotate (compromise)
- Given user yêu cầu rotate (admin/user trigger ở v2).
- Then server sinh `client_device_id` mới cho record cũ và clear cookie cũ; record giữ `iam.devices.id` không đổi.
- (V1: scope chỉ bootstrap, rotate là follow-up.)

### AC-11 Cap per user
- Given user đã có 50 device active.
- When login từ device thứ 51 (mới).
- Then evict device cũ nhất (`last_seen_at` cũ nhất, status != revoked) → revoke + DEL runtime; tạo record mới cho device thứ 51.

## 9. Flows

### 9.1 Main flow (login → identify + naming + bootstrap)
1. Client gọi `POST /api/v1/auth/login` (hoặc `/admin/auth/login`).
2. Handler đọc header:
   - `X-Device-Hostname` (chính), `X-Device-Name` (alias).
   - `X-Client-Device-Id`.
3. Service `sanitizeHostname()` áp BR-002.
4. Service identify device:
   - Nếu có `client_device_id` → lookup `(user_id, client_device_id)`:
     - Hit → reuse `tracked_device_id`, update `device_name` (nếu hostname valid), `last_seen_*`.
     - Miss → tạo record mới gắn `client_device_id` đó.
   - Nếu thiếu → sinh UUIDv7 `client_device_id` mới, tạo record mới, set provenance `server-bootstrap`.
5. Apply BR-003/BR-004 cho device_name + source.
6. Apply BR-009 cap per user (evict nếu vượt).
7. Set cookie + header trả `client_device_id` khi vừa bootstrap (BR-008).
8. Tiếp tục flow login token/runtime như spec hiện tại (`iam-device-runtime-v2-spec.md`).

### 9.2 Exception flow
- Header chứa control bytes / non-UTF8 → coi như invalid → BR-003.
- DB lỗi update tên (record đã tồn tại) → giữ tên cũ, login vẫn thành công, audit `device_name_update_failed`.
- DB lỗi insert (record mới) → login fail giữ semantic hiện tại.
- `client_device_id` quá dài (>128 ký tự) hoặc ký tự lạ → coi như thiếu → bootstrap mới.

### 9.3 Edge cases
- User clear cookies/localStorage → request mới không có `client_device_id` → tạo device mới (acceptable, BR-009 sẽ evict device cũ khi đủ ngưỡng).
- Browser đa tab cùng login lần đầu (cùng user, cùng UA) đồng thời → 2 request bootstrap song song:
  - Server cấp 2 `client_device_id` khác nhau → 2 record. Acceptable cho v1; v2 có thể merge dựa trên fingerprint thật.
- Reverse proxy strip header → cần allowlist `X-Device-Hostname`, `X-Device-Name`, `X-Client-Device-Id` trong proxy.
- Attacker gửi giá trị `client_device_id` phỏng đoán → AC-9 bảo vệ bằng scope `(user_id, client_device_id)`.

## 10. Schema delta đề xuất
- `iam.devices`:
  - `client_device_id TEXT NULL`.
  - Unique constraint: `UNIQUE(user_id, client_device_id) WHERE client_device_id IS NOT NULL`.
- `iam.admin_devices`:
  - `client_device_id TEXT NULL`, scope theo admin (key `(admin_id, client_device_id)`).
- KHÔNG thêm cột source/provenance ở v1 (suy được từ giá trị; metric label đủ cho observability).
- Migration backward-compatible: cột nullable.

## 11. Dependencies / assumptions
- A1: Reverse proxy/edge cho phép forward 3 header trên.
- A2: Schema mở rộng được như mục 10.
- A3: Client (web/CLI) sẵn sàng lưu cookie `client_device_id` HttpOnly=false (web) hoặc file (CLI).
- D1: Spec runtime `iam-device-runtime-v2-spec.md` không thay đổi; idea này chỉ chạm DB registry layer + bootstrap cookie.

## 12. Open questions
- Q1: FE có cần expose nguồn naming/provenance không?  
  Recommend: NO ở v1 (FE tự suy `device_name == "unknown device"` để hiển thị badge "auto-named" nếu cần).
- Q2: Có cho unicode hostname ở v1 không?  
  Recommend: NO (ASCII allowlist BR-002 v1; v2 review lại).
- Q3: Có endpoint user-edit hostname (`PATCH /me/devices/:id`) ở giai đoạn này không?  
  Recommend: NO ở v1.
- Q4: Cookie domain cho `client_device_id` lấy từ `cfg.App.PublicDomain` đúng cho cả cloud-ui và admin-ui?  
  Recommend: YES cho v1, follow đúng pattern cookie hiện tại.
- Q5: TTL cookie `client_device_id` 1 năm có ổn không hay 6 tháng?  
  Recommend: 365 ngày v1 để giảm rebootstrap.

## 13. Traceability map
- US-1, US-2, US-4 → AC-1..AC-6 → BR-001..BR-005 → flow 9.1.
- US-3 → AC-2, AC-8 → BR-003, BR-008 → flow 9.1.
- US-5 → AC-7 → BR-007 → flow 9.1.
- US-6 → AC-8, AC-11 → BR-008, BR-009 → flow 9.1, 9.3.
- AC-9 → BR-007.
- AC-11 → BR-009.

## 14. Handoff fields (BA → next role)
- requirement_id: `iam-device-name-from-client-header`
- actors: end-user, admin, security-ops, system
- rules: BR-001..BR-010
- acceptance: AC-1..AC-11
- open_questions: Q1..Q5
- assumptions: A1..A3
- dependencies: D1
- next role:
  1. `system-architecture` xác nhận:
     - schema delta + cookie/header contract,
     - identity SoT (BR-007, BR-010),
     - cap per user (BR-009),
     - log policy (BR-005).
  2. `backend-developer` lên spec + implement: header read, sanitize util, repo upsert by `(user_id, client_device_id)`, migration, evict cap, bootstrap response cookie+header, test theo AC-1..AC-11.
