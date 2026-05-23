# Admin API Key Migration V1 (Temp Spec)

## 1) Mục tiêu

Đặc tả migration dữ liệu cho cơ chế Admin API Key V1:
- lưu admin API key theo dạng hash-only
- tách biệt hoàn toàn khỏi user auth flow
- enforce chính sách single key toàn hệ thống
- hỗ trợ TTL/rotation theo `config.Security.AdminAPITokenTTL`

Tài liệu này là spec migration boundary, không mô tả changelist file/code.

---

## 2) Scope V1

Trong scope:
- thêm bảng singleton lưu admin API key credential
- thêm bảng 2FA riêng cho admin
- thêm bảng recovery code riêng cho admin
- index phục vụ verify key nhanh
- cột thời gian phục vụ lifecycle (`created_at`, `expires_at`)

Ngoài scope:
- không có `status`
- không có `revoked_at`
- không có `last_used_at`
- không thêm device binding/request signing schema
- không thêm bảng `admin_2fa_challenges` trong DB (challenge lưu Redis)

---

## 3) Bảng `admin_api_keys` (bắt buộc)

Mục đích:
- lưu credential admin API key cho auth middleware `/admin`
- chỉ lưu hash, không lưu plaintext key/token
- toàn hệ thống chỉ tồn tại 1 row key hiện hành

Columns V1:
- `id` (uuid v7, PK)
- `key_hash` (text, not null, unique)
- `created_by` (text, nullable)
- `created_at` (timestamptz, not null default now())
- `expires_at` (timestamptz, not null)

Ghi chú:
- Không có cột trạng thái trong V1.
- Key hết hạn được xác định bằng `expires_at <= now()` ở app layer.

---


## 4) Bảng `admin_action_audits` (bắt buộc)

Mục đích:
- lưu audit trail cho các hành động quản trị quan trọng trên kênh `/admin`
- phục vụ truy vết sự cố, điều tra bảo mật, và compliance

Columns V1:
- `id` (uuid v7, PK)
- `action` (text, not null) — tên hành động, ví dụ: `admin_key_rotated`, `zone_deleted`
- `resource_type` (text, not null) — loại tài nguyên, ví dụ: `admin_api_key`, `zone`
- `resource_id` (text, nullable) — định danh tài nguyên liên quan
- `status` (text, not null) — `success` / `failed`
- `request_ip` (text, nullable)
- `request_path` (text, not null)
- `request_method` (text, not null)
- `error_code` (text, nullable)
- `metadata` (jsonb, not null default `'{}'::jsonb`)
- `created_at` (timestamptz, not null default now())

Ghi chú:
- Không lưu secret/token plaintext vào metadata.
- `metadata` chỉ chứa thông tin an toàn để debug/audit.

---

## 5) Bảng `admin_2fa_settings` (bắt buộc)

Mục đích:
- lưu cấu hình 2FA riêng cho admin channel
- tách biệt với user 2FA flow

Columns V1 (mức migration boundary):
- `id` (uuid, PK)
- `is_enabled` (boolean, not null, default false)
- `factor_type` (text, not null) — ví dụ `totp` (giá trị cụ thể chốt ở auth spec)
- `secret_ciphertext` (text, nullable) — secret material ở dạng đã bảo vệ
- `created_at` (timestamptz, not null default now())
- `updated_at` (timestamptz, not null default now())

Ghi chú:
- không lưu plaintext 2FA secret
- đây là bảng singleton theo admin principal của hệ thống

---

## 6) Bảng `admin_recovery_codes` (bắt buộc)

Mục đích:
- lưu recovery codes riêng cho admin
- dùng one-time cho recovery flow

Columns V1:
- `id` (uuid, PK)
- `code_hash` (text, not null, unique)
- `used_at` (timestamptz, nullable)
- `created_at` (timestamptz, not null default now())

Ghi chú:
- chỉ lưu hash của recovery code
- code đã dùng được xác định qua `used_at`

---

## 7) `admin_2fa_challenges` lưu Redis (không tạo bảng DB)

Chính sách V1:
- challenge 2FA lưu ở Redis để TTL ngắn và verify nhanh
- không tạo bảng `admin_2fa_challenges` trong migration SQL phase này

Redis record tối thiểu (mức khái niệm):
- challenge id
- purpose
- expires_at (TTL)
- attempts/status

---

## 8) Ràng buộc dữ liệu bắt buộc

### 8.1 Singleton key policy

Chính sách V1:
- chỉ có **duy nhất 1 row** trong `admin_api_keys`.

Enforce:
- singleton row constraint (một trong các cách):
  - check/trigger enforce count <= 1, hoặc
  - unique index trên biểu thức constant để chỉ cho 1 row.

### 8.2 Hash uniqueness

- `key_hash` unique.

- `code_hash` của `admin_recovery_codes` unique.

### 8.3 Expiry constraint

- `expires_at > created_at`.

---

## 9) Index strategy V1

Bắt buộc:
- PK index `admin_api_keys(id)`
- unique index `admin_api_keys(key_hash)`
- index `admin_api_keys(expires_at)`
- PK index `admin_action_audits(id)`
- index `admin_action_audits(created_at)`
- index `admin_action_audits(action, created_at)`
- index `admin_action_audits(resource_type, resource_id)`
- index `admin_action_audits(status, created_at)`
- unique index `admin_recovery_codes(code_hash)`
- index `admin_recovery_codes(created_at)`
- index `admin_recovery_codes(used_at)`
- index `admin_2fa_settings(updated_at)`

---

## 10) Lifecycle contract (gắn với auth spec)

Khi tạo/rotate key:
- `expires_at = created_at + AdminAPITokenTTL`
- Secret dùng để ký admin token cũng dùng TTL bằng `AdminAPITokenTTL` để đồng bộ lifecycle ký/xác minh.

Khi rotate:
- replace row trong transaction atomically (không có multi-key window)

Middleware verify:
- hash match
- `expires_at > now()`

---

## 11) Migration strategy V1

### 8.1 Up migration
- thêm bảng `admin_api_keys`
- thêm bảng `admin_action_audits`
- thêm bảng `admin_2fa_settings`
- thêm bảng `admin_recovery_codes`
- thêm check constraint thời gian
- thêm index/unique index như mục 6
- thêm singleton-row constraint
- không tạo bảng `admin_2fa_challenges` (dùng Redis)

### 8.2 Down migration
- drop index phụ thuộc
- drop bảng `admin_action_audits`
- drop bảng `admin_api_keys`
- drop bảng `admin_recovery_codes`
- drop bảng `admin_2fa_settings`

---

## 12) Security contract

- Không lưu plaintext key/token trong DB.
- Không lưu plaintext 2FA secret/recovery code trong DB.
- Hash compare phải theo cơ chế time-safe ở app layer.
- Không log giá trị raw key/token trong migration/seed/runtime.
- Token/key material dùng cho admin phải đạt độ dài/entropy an toàn production (khuyến nghị >= 256-bit random trước khi hash lưu DB).

---

## 13) Acceptance criteria

- Có bảng `admin_api_keys` theo đúng schema V1.
- Không có `status`, `revoked_at`, `last_used_at`.
- Enforce singleton key row.
- Có unique `key_hash`.
- Có `expires_at` để phục vụ TTL/rotation theo `AdminAPITokenTTL`.
- Có bảng `admin_action_audits` + index đủ cho truy vết hành động quan trọng của admin.
- Có bảng `admin_2fa_settings` cho admin 2FA riêng.
- Có bảng `admin_recovery_codes` cho recovery riêng của admin.
- `admin_2fa_challenges` không nằm trong DB migration, được xử lý bằng Redis.

---

## 14) Quan hệ spec

- Spec migration này là **source-of-truth về schema** cho Admin API key V1.
- `admin-auth-login-flow-v1-spec.md` phụ thuộc vào migration spec này ở tầng dữ liệu.
