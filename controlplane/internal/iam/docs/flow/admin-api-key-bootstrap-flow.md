# Admin API Key Bootstrap Flow

Tài liệu này mô tả **flow bootstrap admin API key hiện tại** của IAM module theo implementation thực tế.

## 1) Scope

Flow này bao gồm:
- bootstrap admin API key khi startup app,
- bootstrap admin TOTP secret + recovery codes,
- advisory lock cho môi trường HA,
- Telegram notify với retry,
- rollback batch bootstrap nếu notify final-fail.

Flow này không bao gồm:
- admin runtime auth/verify,
- admin challenge runtime,
- device trust flow.

---

## 2) Entry point runtime

App startup gọi bootstrap theo chuỗi:
1. `internal/app/app.go` tạo `bootstrapCtx` với timeout `20s`.
2. `RunModuleBootstraps(bootstrapCtx, modules)` trong `internal/app/bootstrap.go`.
3. `modules.IAM.Bootstrap(ctx)` trong `internal/iam/module.go`.
4. `AdminAPIKeyService.Bootstrap(ctx, actor)` trong `internal/iam/service/admin_api_key_service.go`.

Nếu fail tại IAM bootstrap:
- caller log system bằng `logger.SysError("iam.bootstrap.apitoken", err.Error())`,
- trả lỗi lên app bootstrap path,
- app cleanup qua `App.Stop()`.

---

## 3) Service flow (AdminAPIKeyService)

`AdminAPIKeyService.Bootstrap(ctx, actor)` chạy tuần tự:

1. Acquire lock:
   - gọi `repo.AcquireBootstrapLock(ctx)`.
   - fail -> `ErrAdminBootstrapLockFailed`.

2. Precondition:
   - gọi `repo.GetActiveAdminAPIKey(ctx)`.
   - nếu đã có key hợp lệ -> `ErrAdminBootstrapNotAllowed`.
   - lỗi DB -> `ErrAdminBootstrapPreconditionFailed`.

3. Generate materials:
   - sinh admin API key plaintext (`GenerateToken(48)`),
   - hash key bằng `HashTokenSHA256`,
   - sinh TOTP (`GenerateTOTP`),
   - encrypt TOTP secret (`EncryptSecret`),
   - sinh 8 recovery codes (`GenerateRecoveryCode(24)`),
   - hash recovery code (`HashRecoveryCode`).

4. Persist bootstrap batch:
   - gọi `repo.Bootstrap(ctx, payload)`.
   - fail -> `ErrAdminBootstrapPersistFailed`.

5. Telegram notify:
   - gửi message chứa bootstrap material theo policy vận hành,
   - retry backoff `1s -> 2s -> 4s` (tối đa 3 lần).

6. Final-fail handling:
   - nếu fail cả 3 lần: gọi `repo.RollbackBootstrap(ctx, payload)`,
   - rollback fail -> `ErrAdminBootstrapRollbackFailed`,
   - rollback ok -> `ErrAdminBootstrapNotifyFailed`.

Lưu ý:
- service **không log**,
- service chỉ trả error contract.

---

## 4) Repository flow

### 4.1 Acquire lock
- Dùng Postgres advisory lock (`pg_try_advisory_lock`) với key cố định.
- Lock giữ trên dedicated DB connection cho tới khi `Release`.

### 4.2 Bootstrap persist
`repo.Bootstrap(ctx, payload)` mở transaction và ghi:
- `admin_api_keys`: insert key hash + expires,
- `admin_2fa_settings`: upsert singleton config,
- `admin_recovery_codes`: xóa batch cũ, insert batch mới,
- `admin_action_audits`: insert action `admin_bootstrap_succeeded`.

### 4.3 Rollback scoped batch
`repo.RollbackBootstrap(ctx, payload)` mở transaction và xóa **đúng batch vừa tạo** theo payload:
- recovery codes theo `created_at = payload.GeneratedAt`,
- 2FA settings theo `updated_at + secret_ciphertext`,
- api key theo `key_hash + created_at`,
- audit success record theo action + timestamp + request path/method.

---

## 5) Data contract chính

`AdminBootstrapPayload` (domain entity) đang dùng:
- `Actor`
- `KeyHash`
- `ExpiresAt`
- `RecoveryCodeHashes`
- `GeneratedAt`
- `SecretCiphertext`
- `RequestPath`
- `RequestMethod`

`AdminAPIKeyService` contract hiện tại:
- `Bootstrap(ctx context.Context, actor string) error`

---

## 6) Error mapping (service)

- `ErrAdminBootstrapLockFailed`
- `ErrAdminBootstrapPreconditionFailed`
- `ErrAdminBootstrapNotAllowed`
- `ErrAdminBootstrapPersistFailed`
- `ErrAdminBootstrapRollbackFailed`
- `ErrAdminBootstrapNotifyFailed`

Caller chịu trách nhiệm log system và quyết định lifecycle app khi bootstrap fail.

---

## 7) Observability/metrics

- Flow bootstrap admin API key **không thêm metric/trace riêng** ở phase hiện tại.
- Logging nằm ở caller app bootstrap (`internal/app/bootstrap.go`).

---

## 8) HA & timeout

- HA guard: advisory lock trong DB.
- Bootstrap context timeout: `20s` từ `NewApplication`.
- Khi timeout/cancel, các call theo `ctx` phải fail-closed và bubble error lên caller.

---

## 9) File map

- `internal/app/app.go`
- `internal/app/bootstrap.go`
- `internal/iam/module.go`
- `internal/iam/domain/service/admin_api_key_service.go`
- `internal/iam/service/admin_api_key_service.go`
- `internal/iam/domain/repo/admin_api_key_repo.go`
- `internal/iam/repository/admin_api_key_repo.go`
- `internal/iam/errorx/error.go`
