# Admin API Key Migration V1 - Implementation Plan

## A) Mục tiêu implement

Triển khai migration V1 cho Admin API Key theo spec:
- `admin-api-key-migration-v1-temp-spec.md`

Mục tiêu:
- có bảng singleton `admin_api_keys` (hash-only)
- có bảng `admin_action_audits` để ghi critical admin actions
- có bảng `admin_2fa_settings` cho 2FA admin riêng
- có bảng `admin_recovery_codes` cho recovery admin riêng
- enforce lifecycle TTL qua `expires_at`
- chuẩn bị nền dữ liệu cho admin auth middleware `/admin`
- `admin_2fa_challenges` theo hướng Redis-centric (không dùng DB runtime path)

---

## B) Danh sách thay đổi file cụ thể

### B.1 Migration files

1) `controlplane/internal/iam/migrations/000002_iam_tables.up.sql`
- Thêm DDL cho bảng `admin_api_keys`:
  - `id UUID PK`
  - `key_hash TEXT NOT NULL UNIQUE`
  - `created_by TEXT NULL`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `expires_at TIMESTAMPTZ NOT NULL`
- Thêm check constraint:
  - `expires_at > created_at`
- Thêm DDL cho bảng `admin_action_audits`:
  - `id UUID PK`
  - `action TEXT NOT NULL`
  - `resource_type TEXT NOT NULL`
  - `resource_id TEXT NULL`
  - `status TEXT NOT NULL` (`success|failed` ở app-level contract)
  - `request_ip TEXT NULL`
  - `request_path TEXT NOT NULL`
  - `request_method TEXT NOT NULL`
  - `error_code TEXT NULL`
  - `metadata JSONB NOT NULL DEFAULT '{}'::jsonb`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- Thêm DDL cho bảng `admin_2fa_settings`:
  - `id UUID PK`
  - `is_enabled BOOLEAN NOT NULL DEFAULT false`
  - `factor_type TEXT NOT NULL`
  - `secret_ciphertext TEXT NULL`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- Thêm DDL cho bảng `admin_recovery_codes`:
  - `id UUID PK`
  - `code_hash TEXT NOT NULL UNIQUE`
  - `used_at TIMESTAMPTZ NULL`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- Không tạo bảng `admin_2fa_challenges` trong DB migration (challenge runtime lưu Redis).

2) `controlplane/internal/iam/migrations/000003_iam_indexes.up.sql`
- Thêm singleton-row index (chốt strategy):
  - `CREATE UNIQUE INDEX ux_admin_api_keys_singleton ON schema.admin_api_keys ((true));`
- Thêm index:
  - `idx_admin_api_keys_expires_at` on `expires_at`
  - `idx_admin_action_audits_created_at`
  - `idx_admin_action_audits_action_created_at`
  - `idx_admin_action_audits_resource`
  - `idx_admin_action_audits_status_created_at`
  - `ux_admin_recovery_codes_code_hash`
  - `idx_admin_recovery_codes_created_at`
  - `idx_admin_recovery_codes_used_at`
  - `idx_admin_2fa_settings_updated_at`

3) `controlplane/internal/iam/migrations/000003_iam_indexes.down.sql`
- Drop toàn bộ index của admin_api_keys/admin_action_audits theo thứ tự phụ thuộc.

4) `controlplane/internal/iam/migrations/000002_iam_tables.down.sql`
- Drop bảng `admin_action_audits`.
- Drop bảng `admin_api_keys`.
- Drop bảng `admin_recovery_codes`.
- Drop bảng `admin_2fa_settings`.

---

### B.2 Docs/spec sync files (bắt buộc)

3) `controlplane/internal/iam/docs/spec/admin-api-key-migration-v1-temp-spec.md`
- Cập nhật mục “Migration strategy” từ abstract sang confirmed SQL strategy nếu cần:
  - singleton enforce = unique index constant expression.

4) `controlplane/internal/iam/docs/spec/admin-auth-login-flow-v1-spec.md`
- Confirm lại dependency vào migration.
- Confirm wording single-row policy + rotate replace row atomically.

---

### B.3 Bootstrap phase contract (chốt)

- V1 migration **không seed** admin key dưới bất kỳ hình thức SQL seed nào.
- Admin key sẽ được tạo ở **phase bootstrap sau** tại app-layer.
- Migration phase chỉ chịu trách nhiệm chuẩn bị schema + constraint để bootstrap ghi dữ liệu an toàn.

---

## C) SQL contract chi tiết cần đảm bảo

### 1) `admin_api_keys` singleton semantics
- Không được insert >1 row.
- Rotate phải theo transaction:
  1. delete row cũ hoặc update-replace atomically theo policy đã chốt
  2. insert row mới
- Không có dual-row window commit thành công.

### 2) `key_hash` uniqueness
- `UNIQUE (key_hash)` bắt buộc.

### 2.1) `code_hash` uniqueness
- `UNIQUE (code_hash)` cho `admin_recovery_codes` bắt buộc.

### 3) `expires_at` validity
- `CHECK (expires_at > created_at)` bắt buộc.

### 4) `admin_action_audits` queryability
- Có index đủ cho các truy vấn:
  - theo thời gian
  - theo action
  - theo resource
  - theo status

---

## D) Kiểm thử migration (chuẩn bị implement)

### D.1 Up/down migration smoke
- migrate up thành công trên DB trống.
- migrate down rollback sạch.
- migrate up lại lần 2 từ đầu thành công.

### D.2 Constraint tests
- insert row `admin_api_keys` thứ 2 phải fail vì singleton.
- insert row có `expires_at <= created_at` phải fail.
- insert duplicate `key_hash` phải fail.

### D.3 Audit table tests
- insert sample audit row thành công.
- query theo `action` + time range dùng index path.

### D.4 Admin 2FA/Recovery schema tests
- insert/update `admin_2fa_settings` thành công.
- insert duplicate `code_hash` vào `admin_recovery_codes` phải fail.
- mark `used_at` cho recovery code thành công.

### D.5 Redis-centric challenge contract tests (không DB fallback)
- challenge 2FA runtime tạo/verify bằng Redis.
- khi Redis unavailable: flow challenge fail-closed.
- không dùng bảng DB để verify challenge runtime.

---

## E) Acceptance checklist

- [ ] Có migration up/down cho `admin_api_keys` và `admin_action_audits`.
- [ ] Không có `status`, `revoked_at`, `last_used_at` trong `admin_api_keys`.
- [ ] Enforce singleton row policy ở DB layer.
- [ ] Enforce `expires_at > created_at`.
- [ ] Có index đầy đủ cho audit truy vết.
- [ ] Có bảng `admin_2fa_settings` và `admin_recovery_codes` theo spec.
- [ ] Không có bảng `admin_2fa_challenges` trong migration DB phase này.
- [ ] Challenge runtime theo Redis-centric và không fallback verify sang DB.
- [ ] Spec auth/migration không lệch nhau sau khi chốt strategy.
