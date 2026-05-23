# Admin Logout Flow

## 1) Mục tiêu

Mô tả luồng runtime cho `POST /admin/auth/logout` trong kênh `/admin`:
- xác thực session hiện tại qua `AdminAPIKeyAuth`,
- cleanup runtime secret theo `device_id`,
- clear đầy đủ session fragments phía client.

Tài liệu này chỉ mô tả flow vận hành, ngoại lệ, state machine, error handling và security invariants.

---

## 2) Actors & Systems

- **Admin Client**: gọi logout endpoint.
- **RateLimit middleware**: giới hạn tần suất gọi logout theo bucket route.
- **AdminAPIKeyAuth middleware**: verify runtime fragments (`admin_api_token`, `device_id`, `device_secret`).
- **AdminAuthHandler**: transport boundary, gọi service và clear cookies.
- **AdminAPIKeyService**: business cleanup runtime secret.
- **Redis**: runtime store cho `device_secret(hash)` theo `device_id`.

---

## 3) Main sequence

```mermaid
sequenceDiagram
    autonumber
    participant C as Admin Client
    participant RL as RateLimit
    participant M as AdminAPIKeyAuth
    participant H as AdminAuthHandler
    participant S as AdminAPIKeyService
    participant X as Redis

    C->>RL: POST /admin/auth/logout + cookies
    RL->>M: pass
    M->>M: Verify token + device_id + device_secret
    M-->>H: auth ok

    H->>H: Read device_id cookie
    H->>S: AdminLogout(ctx, device_id)
    S->>X: DeleteDeviceSecret(device_id)
    X-->>S: ok
    S-->>H: nil

    H->>H: Clear cookies(admin_api_token, device_id, device_secret)
    H-->>C: 204 No Content
```

---

## 4) Exception sequences

### 4.1 Session fragments thiếu hoặc invalid

```mermaid
sequenceDiagram
    autonumber
    participant C as Admin Client
    participant M as AdminAPIKeyAuth

    C->>M: POST /admin/auth/logout
    M->>M: Verify fragments/token/device binding
    M-->>C: 401 Unauthorized (generic)
```

### 4.2 Dependency verify path lỗi (secret provider/Redis verify)

```mermaid
sequenceDiagram
    autonumber
    participant C as Admin Client
    participant M as AdminAPIKeyAuth

    C->>M: POST /admin/auth/logout
    M->>M: Verify session with dependency
    M-->>C: 503 Service Unavailable
```

### 4.3 Runtime cleanup fail trong service

```mermaid
sequenceDiagram
    autonumber
    participant C as Admin Client
    participant H as AdminAuthHandler
    participant S as AdminAPIKeyService
    participant X as Redis

    C->>H: POST /admin/auth/logout (after middleware pass)
    H->>S: AdminLogout(ctx, device_id)
    S->>X: DeleteDeviceSecret(device_id)
    X-->>S: error
    S-->>H: error
    H-->>C: 500 Internal Server Error
```

---

## 5) State machine

```mermaid
stateDiagram-v2
    [*] --> Authenticated

    Authenticated --> LogoutRequested: POST /admin/auth/logout

    LogoutRequested --> RejectedByRateLimit: over limit
    LogoutRequested --> RejectedByAuth: missing/invalid fragments
    LogoutRequested --> DependencyUnavailable: verify dependency fail
    LogoutRequested --> CleanupFailed: redis delete fail

    LogoutRequested --> LoggedOut: verify ok + cleanup ok + cookies cleared

    RejectedByRateLimit --> Authenticated
    RejectedByAuth --> Authenticated
    DependencyUnavailable --> Authenticated
    CleanupFailed --> Authenticated

    LoggedOut --> [*]
```

---

## 6) Error handling matrix

| Condition | HTTP/result | Side effects |
|---|---:|---|
| Rate limit reject | 429 | Không vào auth middleware/handler |
| Missing cookie fragments | 401 | Không gọi service, không cleanup |
| Invalid token/device binding | 401 | Không gọi service, không cleanup |
| Verify dependency fail (middleware) | 503 | Fail-closed, không vào handler |
| `device_id` rỗng nhưng đã vào service | 204 | Service no-op cleanup, handler vẫn clear cookies |
| Redis `DeleteDeviceSecret` fail | 500 | Không xác nhận logout thành công |
| Success path | 204 | Xóa runtime secret theo `device_id` + clear 3 cookies |

---

## 7) Security invariants

- `POST /admin/auth/logout` luôn đi qua `AdminAPIKeyAuth` (không bypass).
- Session admin chỉ hợp lệ khi đủ 3 fragments (`admin_api_token`, `device_id`, `device_secret`).
- Dependency verify path lỗi thì fail-closed (`503`), không cho pass tạm.
- Không log plaintext token hoặc `device_secret`.
- Logout success phải đồng thời:
  - cleanup runtime secret server-side theo `device_id`,
  - clear đầy đủ cookie fragments phía client.
