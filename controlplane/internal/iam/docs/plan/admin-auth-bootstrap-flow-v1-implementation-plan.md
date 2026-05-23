# Admin Auth Bootstrap Flow V1 - Implementation Plan

## A) Mục tiêu implement

Triển khai flow bootstrap admin auth theo spec:
- `internal/iam/docs/spec/admin-auth-bootstrap-flow-temp-spec.md`

Mục tiêu:
- bootstrap nằm trong contract admin API key (không tách module contract riêng)
- dùng DB advisory lock để đảm bảo singleton execution trong HA
- bootstrap chỉ chạy khi không có admin API key hợp lệ
- tạo dữ liệu bootstrap: `admin_api_keys`, `admin_2fa_settings`, `admin_recovery_codes`
- ghi `admin_action_audits` khi bootstrap success
- gửi thông tin bootstrap qua Telegram; retry 3 lần
- nếu Telegram final-fail: rollback sạch dữ liệu bootstrap vừa tạo rồi shutdown app (fail-closed)

---

## B) Danh sách thay đổi file cụ thể

### B.0 File manifest (thêm / sửa / xóa)

Sửa file (Update):
- `internal/iam/domain/entity/admin.go`
- `internal/iam/domain/entity/audit.go`
- `internal/iam/errorx/error.go`
- `internal/iam/module.go`
- `internal/iam/route.go`
- `infra/telegram/telegram.go`

Thêm file (Add):
- `internal/iam/domain/service/admin_api_key_service.go`
- `internal/iam/domain/repo/admin_api_key_repo.go`
- `internal/iam/repository/admin_api_key_repo.go`
- `internal/iam/service/admin_api_key_service.go`
- `internal/iam/test/svc_test/admin_bootstrap_service_test.go`
- `internal/iam/test/repo_test/admin_bootstrap_repo_test.go`

Xóa file (Delete):
- Không xóa file nào trong phase này.

---

### B.1 Domain contracts & entities

1) `internal/iam/domain/service/admin_api_key_service.go`
- **Add func**
  - `Bootstrap(ctx context.Context, actor string) error`
- **Update func**: không có
- **Delete func**: không có

2) `internal/iam/domain/repo/admin_api_key_repo.go`
- **Add func**
  - `AcquireBootstrapLock(ctx context.Context) (BootstrapLock, error)`
  - `GetActiveAdminAPIKey(ctx context.Context) (*entity.AdminAPIKey, error)`
  - `Bootstrap(ctx context.Context, payload entity.AdminBootstrapPayload) (bootstrappedAt time.Time, err error)`
  - `RollbackBootstrap(ctx context.Context, payload entity.AdminBootstrapPayload) error`
- **Update func**: không có
- **Delete func**: không có

3) `internal/iam/domain/entity/admin.go`
- **Update struct/function**
  - cập nhật entity theo schema `admin_api_keys` (hash-only, singleton semantics)
- **Add struct**
  - `AdminBootstrapPayload` để gom tham số bootstrap cho repo, gồm tối thiểu:
    - `Actor string`
    - `KeyHash string`
    - `ExpiresAt time.Time`
    - `RecoveryCodeHashes []string` (batch cố định 8 hash)
    - `GeneratedAt time.Time`
  - (optional nếu cần đẩy audit metadata từ caller):
    - `RequestPath string`
    - `RequestMethod string`
- **Add func**: không có
- **Delete field/func**
  - bỏ fields cũ của `AdminAPIToken` không còn trong migration

4) `internal/iam/domain/entity/audit.go`
- **Add const**
  - `admin_bootstrap_started`
  - `admin_bootstrap_succeeded`
  - `admin_bootstrap_failed`
  - `admin_bootstrap_notify_failed`
  - `admin_bootstrap_rollback_succeeded`
  - `admin_bootstrap_rollback_failed`
- **Update struct/function**
  - dùng `AdminActionAudit` cho `admin_action_audits`
- **Delete func**: không có

---

### B.2 Repository implementation

5) `internal/iam/repository/admin_api_key_repo.go`
- **Add func**
  - `AcquireBootstrapLock(...)`
  - `GetActiveAdminAPIKey(...)`
  - `Bootstrap(...)`
  - `RollbackBootstrap(...)`
  - `releaseBootstrapLock(...)`
- **Update func**: không có (file mới)
- **Delete func**: không có

Hành vi cần làm trong các func trên:
- lock bằng `pg_try_advisory_lock` + dedicated DB connection
- precondition check key hợp lệ theo `expires_at > now()`
- `Bootstrap(...)` mở transaction ở repo và persist data-path:
  - persist key + 2FA singleton + recovery batch 8 + audit success
- `RollbackBootstrap(...)` mở transaction ở repo và rollback-clean dữ liệu bootstrap của lần đó:
  - `admin_api_keys`
  - `admin_2fa_settings`
  - `admin_recovery_codes`
  - success record `admin_action_audits` (nếu đã ghi)

---

### B.3 Service implementation

6) `internal/iam/service/admin_api_key_service.go`
- **Add func**
  - `Bootstrap(ctx context.Context, actor string) error`
- **Update func**: không có (file mới)
- **Delete func**: không có

Hành vi:
1. acquire DB lock
2. validate preconditions
3. generate admin key + hash
4. generate 8 recovery codes (`A-Z0-9`, length 24) + hash
5. tạo `entity.AdminBootstrapPayload` và gọi repo `Bootstrap(...)`
6. generate recovery codes inline trong `Bootstrap(...)` (8 code `A-Z0-9`, length 24), hash và đưa vào payload
7. Telegram notify retry 3 lần (1s/2s/4s) inline trong `Bootstrap(...)`
8. final-fail: gọi repo `RollbackBootstrap(...)` rồi trả lỗi cho caller shutdown app

Rule:
- service không log, chỉ return error
- không tách helper function riêng cho recovery generation/telegram retry trong phase này
- generation/hash recovery code gọi trực tiếp `controlplane/internal/security/mfa.go`

---

### B.4 App/bootstrap caller orchestration

7) `internal/iam/module.go` / `internal/iam/route.go` / entry bootstrap caller hiện hữu
- **Update func**
  - wire `AdminAPIKeyService` mới vào module
  - gọi `AdminAPIKeyService.Bootstrap(ctx, actor)` tại bootstrap caller
- **Add func**
  - nếu chưa có: hàm runner bootstrap IAM (ví dụ `runIAMBootstrap(...)`)
- **Delete func**: không có

Caller behavior:
- log bằng `pkg/logger/logger.go`
- cụ thể: `logger.SysError("iam.bootstrap.apitoken", err.Error())`
- nếu nhánh final-fail (tele retry fail 3x + rollback done): graceful shutdown 10s -> `os.Exit(1)`
- chặn trigger bootstrap mới khi đã vào final-fail branch

---

### B.5 Infra integration & error contract

8) `infra/telegram/telegram.go`
- **Add/Update func**
  - `SendAdminBootstrap(ctx context.Context, payload AdminBootstrapNotifyPayload) error` (hoặc adapter tương đương)
- **Delete func**: không có

9) `internal/iam/errorx/error.go`
- **Add var**
  - `ErrAdminBootstrapNotAllowed`
  - `ErrAdminBootstrapPreconditionFailed`
  - `ErrAdminBootstrapPersistFailed`
  - `ErrAdminBootstrapAuditFailed`
  - `ErrAdminBootstrapNotifyFailed`
  - `ErrAdminBootstrapLockFailed`
  - `ErrAdminBootstrapLockLost`
  - `ErrAdminBootstrapRollbackFailed`
- **Update/Delete**: không có

---

## C) Hành vi kỹ thuật bắt buộc

### C.1 HA lock behavior
- Backend lock: DB advisory lock.
- Acquire: non-blocking try-lock.
- Lock bound to dedicated DB connection.
- Mất lock/connection giữa chừng: abort flow + rollback.
- Release lock bằng `defer/finalizer`.

### C.2 Bootstrap eligibility
- Cho phép bootstrap nếu:
  - không có row `admin_api_keys`, hoặc
  - key hiện có đã hết hạn.
- Nếu key hợp lệ tồn tại => deny bootstrap.

### C.3 Recovery codes policy
- Mỗi batch: 8 code.
- Format code: uppercase alphanumeric (`A-Z0-9`), length 24.
- Bootstrap thành công mới commit batch mới.
- Bootstrap lại thành công: invalidate batch cũ và thay batch mới.
- Cơ chế generate/hash recovery code dùng trực tiếp từ `controlplane/internal/security/mfa.go`.

### C.4 Telegram final-fail policy
- Retry tối đa 3 lần.
- Nếu fail cả 3 lần:
  - rollback sạch dữ liệu bootstrap vừa tạo trong 1 transaction,
  - caller log bằng logger system,
  - shutdown toàn app (fail-closed).

---

## D) Kiểm thử triển khai

### D.1 Unit tests (service)
- Không có key hợp lệ -> bootstrap success path.
- Có key hợp lệ -> bootstrap denied.
- Key hết hạn -> bootstrap allowed.
- Telegram retry success ở lần 2/3.
- Telegram fail 3 lần -> service trả lỗi final-fail branch.
- Lock lost giữa chừng -> abort + lỗi đúng contract.

### D.2 Repository/integration tests
- DB advisory lock: chỉ 1 process acquire được.
- Đồng thời 2 request bootstrap: chỉ 1 request đi qua.
- Rollback-clean final-fail xóa sạch dữ liệu bootstrap vừa tạo.
- Recovery code batch đúng 8 record và đúng format/hash contract.

### D.3 App/bootstrap orchestration tests
- Caller log cause/error đúng nhánh lỗi (qua system logger).
- Nhánh final-fail trigger graceful shutdown path.
- Không yêu cầu metric/trace cho flow bootstrap ở phase này.

---

## E) Acceptance checklist

- [ ] Bootstrap method nằm trong contract admin API key hiện hữu.
- [ ] DB advisory lock hoạt động đúng trong môi trường HA.
- [ ] Precondition bootstrap dựa trên admin API key hợp lệ.
- [ ] Tạo đủ dữ liệu bootstrap: key + 2FA settings + recovery codes.
- [ ] Recovery batch đúng 8 code, format `A-Z0-9`, length 24, lưu hash.
- [ ] Recovery batch generate/hash qua `controlplane/internal/security/mfa.go`.
- [ ] Có audit success record cho bootstrap thành công.
- [ ] Telegram retry đúng 3 lần policy.
- [ ] Final-fail rollback sạch trong 1 transaction.
- [ ] Final-fail được caller log và shutdown toàn app.
- [ ] Service không log, chỉ return error contract.
