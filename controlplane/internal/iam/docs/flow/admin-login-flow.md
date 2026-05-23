# Admin Login Flow

## 1) Mục tiêu

Mô tả luồng hoạt động đăng nhập admin qua `/admin/auth/login` theo mô hình:
- `admin_api_key` + MFA (`totp|recovery_code`),
- issue runtime fragments (`admin_api_token`, `device_id`, `device_secret`),
- hỗ trợ các nhánh ngoại lệ và hành vi fail-safe.

Tài liệu này chỉ mô tả **flow runtime** và **state transitions**.

---

## 2) Actors & Systems

- **Admin Client**: gửi login request.
- **Admin Auth Handler**: transport boundary, map HTTP + set cookie.
- **Admin Auth Service**: business verify + issue runtime fragments.
- **PostgreSQL**: source-of-truth cho admin key/2FA/recovery/device binding.
- **Redis**: runtime store cho `device_secret(hash)` và lock recovery consume.

---

## 3) Main sequence (success path)

```mermaid
sequenceDiagram
    autonumber
    participant C as Admin Client
    participant H as AdminAuthHandler
    participant S as AdminAPIKeyService
    participant R as AdminAPIKeyRepo
    participant X as Redis

    C->>H: POST /admin/auth/login\n(admin_api_key,mfa_method,mfa_code,device_public_key)
    H->>S: AdminLogin(request)

    S->>S: Validate input + normalize device_public_key
    S->>S: Load active admin key (RAM cache -> DB fallback)
    S->>R: GetActiveAdminAPIKey() (cache miss)
    R-->>S: active key
    S->>S: Verify api key hash

    alt mfa_method = totp
        S->>S: Load/decrypt TOTP secret (RAM cache -> DB fallback)
        S->>R: GetAdmin2FASettings() (cache miss)
        R-->>S: secret_ciphertext
        S->>S: Validate TOTP
    else mfa_method = recovery_code
        S->>X: AcquireRecoveryConsumeLock(code_hash)
        X-->>S: lock acquired
        S->>R: ConsumeRecoveryCode(code_hash)
        R-->>S: consumed=true
    end

    S->>S: Generate device_id + device_secret + admin_api_token(JWT)
    S->>X: SetDeviceSecret(device_id, hash(device_secret), TTL)
    S->>R: UpsertAdminDeviceBinding(public_key,fingerprint,...)
    R-->>S: ok

    S-->>H: AdminLoginResult(token,device_id,device_secret,expires_at)
    H-->>C: 200 OK + Set-Cookie(admin_api_token,device_id,device_secret)
```

---

## 4) Exception sequences

### 4.1 Invalid credential / MFA fail

```mermaid
sequenceDiagram
    autonumber
    participant C as Admin Client
    participant H as AdminAuthHandler
    participant S as AdminAPIKeyService

    C->>H: POST /admin/auth/login
    H->>S: AdminLogin(request)
    S->>S: Verify credential or MFA
    S-->>H: ErrAdminLoginInvalidCredential / ErrAdminLoginMFAInvalid
    H-->>C: 401 Unauthorized (generic)
```

### 4.2 Redis runtime write fail

```mermaid
sequenceDiagram
    autonumber
    participant C as Admin Client
    participant H as AdminAuthHandler
    participant S as AdminAPIKeyService
    participant X as Redis

    C->>H: POST /admin/auth/login
    H->>S: AdminLogin(request)
    S->>X: SetDeviceSecret(...)
    X-->>S: error
    S-->>H: ErrAdminLoginDeviceBindingFailed
    H-->>C: 401 Unauthorized (generic)
```

### 4.3 Device binding DB fail after Redis set

```mermaid
sequenceDiagram
    autonumber
    participant C as Admin Client
    participant H as AdminAuthHandler
    participant S as AdminAPIKeyService
    participant R as AdminAPIKeyRepo
    participant X as Redis

    C->>H: POST /admin/auth/login
    H->>S: AdminLogin(request)
    S->>X: SetDeviceSecret(...)
    X-->>S: ok
    S->>R: UpsertAdminDeviceBinding(...)
    R-->>S: error
    S->>X: DeleteDeviceSecret(device_id) (cleanup)
    S-->>H: ErrAdminLoginDeviceBindingFailed
    H-->>C: 401 Unauthorized (generic)
```

---

## 5) State machine

```mermaid
stateDiagram-v2
    [*] --> Unauthenticated

    Unauthenticated --> LoginRequested: POST /admin/auth/login

    LoginRequested --> InputRejected: invalid payload/public_key
    LoginRequested --> CredentialRejected: invalid/expired admin_api_key
    LoginRequested --> MFARejected: invalid totp/recovery_code
    LoginRequested --> RuntimeStoreFailed: redis write/lock error
    LoginRequested --> DeviceBindingFailed: upsert binding fail
    LoginRequested --> TokenIssueFailed: sign token fail

    LoginRequested --> Authenticated: credential+MFA ok, fragments issued

    InputRejected --> Unauthenticated
    CredentialRejected --> Unauthenticated
    MFARejected --> Unauthenticated
    RuntimeStoreFailed --> Unauthenticated
    DeviceBindingFailed --> Unauthenticated
    TokenIssueFailed --> Unauthenticated

    Authenticated --> [*]
```

---

## 6) Error handling matrix

| Condition | HTTP | Side effect |
|---|---:|---|
| Invalid payload / invalid device public key | 400 | Không tạo session |
| Invalid/expired admin_api_key | 401 | Không tạo session |
| Invalid MFA | 401 | Không tạo session |
| Redis lock/runtime write fail | 401/503 theo mapping hiện hành | Không tạo session |
| DB upsert device binding fail | 401 | Cleanup runtime secret nếu đã set |
| JWT sign/auth infra fail | 500 | Cleanup runtime secret nếu đã set |

---

## 7) Security invariants

- Không log plaintext `admin_api_key`, `mfa_code`, token, `device_secret`.
- Response lỗi phải generic, không leak lý do nội bộ.
- Session admin chỉ hợp lệ khi đủ 3 fragments (`admin_api_token`, `device_id`, `device_secret`).
- Recovery code phải one-time và có lock chống race consume.
- Cache policy của admin flow: TTL-only by design (replica-local RAM cache + DB fallback).
