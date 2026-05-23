# IAM Register Account V1 - Temporary Implementation Spec

## 1. Goal

Spec này mô tả flow `register account` cho `internal/iam` theo đúng **migration/schema IAM hiện tại**.

Mục tiêu của v1:
- open signup
- input: `username`, `email`, `password`, `re_password`, `fullname`
- tạo `users`, `user_profiles`, `password_history`, `audit_events`
- chưa auto-login
- chưa issue JWT
- chưa verify email thật
- chưa tạo tenant/workspace mặc định
- chưa gán role mặc định

Reality khóa cứng theo migration hiện tại:
- không có `user_credentials`
- không có prefix `iam_`
- không có `password_algo`
- không có `email_normalized` column
- `users.status` chỉ có: `active`, `suspended`, `disabled`
- `users.status` default hiện tại là `active`

---

## 2. Business Decision Locked

### 2.1 Register mode
- Open signup
- Không cần invite ở v1

### 2.2 Account status after register
- Account mới tạo dùng `users.status = active`
- Lý do: migration hiện tại **không có** enum `pending` cho `user_status`
- Nếu sau này business muốn verify email trước khi cho dùng account thì phải làm bằng luồng khác, ví dụ:
  - one-time token / email verification challenge
  - hoặc bổ sung schema/migration mới

### 2.3 Verification mode in v1
- Chưa verify email thật
- Chưa gửi mail thật
- Cho phép để `TODO` trong service cho phase sau:
  - generate one-time token
  - publish mail job
  - bind email verification flow

### 2.4 Bootstrap scope mode
- Chỉ tạo identity level data
- Không tạo default tenant
- Không tạo default workspace
- Không gán default role
- Không tạo OAuth / MFA / device record trong v1 register

---

## 3. Input / Output Contract

### 3.1 Endpoint
- `POST /api/v1/auth/register`

### 3.2 Request JSON
```json
{
  "username": "alice.nguyen",
  "email": "user@example.com",
  "password": "secret123",
  "re_password": "secret123",
  "fullname": "Alice Nguyen"
}
```

### 3.3 Success response
HTTP `201 Created`

```json
{
  "message": "account created"
}
```

### 3.4 Error response
- `400 bad request`
- `409 resource already exists`
- `500 internal server error`

Client message phải generic, không lộ:
- email đã tồn tại hay chưa
- SQL detail
- hash/internal detail

---

## 4. Schema Alignment Locked

Register v1 phải bám đúng migration IAM hiện tại.

### 4.1 `users`
Dùng các cột:
- `id`
- `username`
- `email`
- `phone`
- `password_hash`
- `status`
- `user_level`
- `created_at`
- `updated_at`

Rule:
- `username` được normalize lowercase ở service trước khi insert
- `email` được normalize lowercase ở service trước khi insert
- uniqueness dựa vào unique index `users_username_lower_uidx` trên `lower(username)`
- uniqueness email dựa vào unique index `users_email_lower_uidx` trên `lower(email)`
- `phone = NULL`
- `status = active`
- `user_level = 4`
- password hash lưu trực tiếp vào `users.password_hash`

### 4.2 `user_profiles`
Dùng các cột:
- `user_id`
- `fullname`
- `avatar_url`
- `bio`
- `locale`
- `timezone`
- `created_at`
- `updated_at`

Rule mapping:
- request `fullname` -> `user_profiles.fullname`
- `fullname` là bắt buộc và chỉ dùng cho hiển thị
- `avatar_url = NULL`
- `bio = NULL`
- `locale = vi-VN`
- `timezone = Asia/Ho_Chi_Minh`

### 4.3 `password_history`
Dùng các cột:
- `id`
- `user_id`
- `password_hash`
- `created_at`

Rule:
- sau khi tạo `users`, insert luôn 1 record `password_history`
- record đầu tiên chính là hash hiện tại lúc account được tạo
- mục đích là giữ invariant cho phase đổi password sau này

### 4.4 `audit_events`
Tạo 1 record audit với:
- `event_type = auth.register`
- `severity = info`
- `target_type = user`
- `target_id = users.id`
- `actor_user_id = NULL`
- `actor_admin_token_id = NULL`
- `tenant_id = NULL`
- `workspace_id = NULL`

`metadata` tối thiểu nên có:
- `email`
- `status = active`
- `source = open_signup`

### 4.5 Không được dùng
- `user_credentials`
- `username`
- `password_algo`
- bất kỳ table `iam_*`
- bất kỳ column verify-email không tồn tại trong migration hiện tại

---

## 5. Flow Summary

Flow register v1:
1. handler bind request JSON
2. handler build service input kèm request context metadata
3. service normalize email
4. service validate username, email, fullname, password, re_password
5. service hash password
6. service dựng entity `User`
7. service dựng entity `UserProfile`
8. service dựng entity `PasswordHistory`
9. service dựng entity `AuditEvent`
10. repo mở transaction
11. repo insert `users`
12. repo insert `user_profiles`
13. repo insert `password_history`
14. repo insert `audit_events`
15. repo commit
16. handler trả `201 Created`

V1 dừng ở đây, chưa:
- login session
- JWT issue
- refresh token issue
- email verification
- role assignment

---

## 6. Folder / File Direction

Spec này không ép tách file nhỏ theo từng table. Giữ grouping theo trách nhiệm như shape hiện tại của module IAM.

### 6.1 Domain entity
Ưu tiên dùng grouping hiện tại:
- `internal/iam/domain/entity/auth.go`
- `internal/iam/domain/entity/profile.go`
- `internal/iam/domain/entity/audit.go`

Có thể bổ sung struct register input/result trong `auth.go` thay vì tạo file lẻ mới.

### 6.2 Model
Ưu tiên dùng grouping hiện tại:
- `internal/iam/model/auth.go`
- `internal/iam/model/profile.go`
- `internal/iam/model/audit.go`

Model phải đi kèm hàm convert entity <-> model.

### 6.3 Repository / service / handler
Có thể thêm các file theo responsibility:
- domain repo contract cho auth/register
- service auth/register
- repository auth/register
- transport http handler auth/register

Nhưng không lạm phát file nếu chưa cần.

---

## 7. Domain Contract Draft

### 7.1 Service input
Nên dùng input rõ nghĩa ở domain/service layer:

```go
type RegisterAccountInput struct {
    Username   string
    Email      string
    Password   string
    RePassword string
    Fullname   string
    RequestID  string
    TraceID    string
    IPAddress  string
    UserAgent  string
}
```

### 7.2 Service contract
```go
type AuthService interface {
    RegisterAccount(ctx context.Context, input RegisterAccountInput) error
}
```

### 7.3 Repository contract
Register là 1 transaction nghiệp vụ, repo contract nên nhận full aggregate data cần insert:

```go
type AuthRepository interface {
    CreateRegisteredUser(
        ctx context.Context,
        user entity.User,
        profile entity.UserProfile,
        passwordHistory entity.PasswordHistory,
        auditEvent entity.AuditEvent,
    ) error
}
```

Decision:
- không pre-check email riêng ở v1
- rely vào unique index của DB để tránh race
- map unique violation -> domain error phù hợp

---

## 8. Service Rules

### 8.1 Validation
Service phải validate tối thiểu:
- username không rỗng
- username đúng pattern business
- email không rỗng
- email đúng format cơ bản
- fullname không rỗng sau trim
- password không rỗng
- `password == re_password`
- password đạt strength tối thiểu theo rule v1

### 8.2 Normalize
Service nên normalize:
- trim space email
- lower-case email trước khi persist
- trim space fullname

### 8.3 Entity build rule
`entity.User`:
- `Status = active`
- `UserLevel = 4`
- `Phone = nil`

`entity.UserProfile`:
- `Fullname = fullname`
- `AvatarURL = nil`
- `Bio = nil`
- `Locale = vi-VN`
- `Timezone = Asia/Ho_Chi_Minh`

`entity.PasswordHistory`:
- dùng chính password hash vừa tạo

`entity.AuditEvent`:
- event register thành công
- không chứa raw password
- không chứa password hash

### 8.4 Service boundaries
- service không viết SQL
- service không dùng transport DTO
- service không log
- service chỉ làm explicit business workflow

---

## 9. Repository Rules

Repository giữ toàn bộ SQL register trong 1 transaction.

Transaction flow:
1. `INSERT INTO users (...)`
2. `INSERT INTO user_profiles (...)`
3. `INSERT INTO password_history (...)`
4. `INSERT INTO audit_events (...)`
5. commit

SQL ownership:
- insert `users`
- insert `user_profiles`
- insert `password_history`
- insert `audit_events`
- begin / rollback / commit transaction
- map unique violation email -> `ErrUserAlreadyExist`

Không có SQL nào cho:
- `user_credentials`
- `username`
- `email_normalized`

---

## 10. Handler Rules

### 10.1 HTTP request DTO
```go
type RegisterRequest struct {
    Username   string `json:"username" binding:"required"`
    Email      string `json:"email" binding:"required,email"`
    Password   string `json:"password" binding:"required"`
    RePassword string `json:"re_password" binding:"required"`
    Fullname   string `json:"fullname" binding:"required"`
}
```

### 10.2 Handler flow
1. bind request DTO
2. bind fail -> log warning -> return `400`
3. build `RegisterAccountInput`
4. gọi service `RegisterAccount`
5. map error inline
6. return `201`

Input build trong handler gồm:
- `Email`
- `Password`
- `RePassword`
- `Fullname`
- `RequestID`
- `TraceID`
- `IPAddress`
- `UserAgent`

### 10.3 Handler style
- map response inline bằng `gin.H`
- log chỉ ở handler
- error mapping inline
- không giấu flow sau helper generic

---

## 11. Error Mapping Rule

### 11.1 Domain / service errors
Tối thiểu cần có:
- `ErrInvalidArgument`
- `ErrInvalidUsername`
- `ErrInvalidEmail`
- `ErrInvalidFullname`
- `ErrPasswordMismatch`
- `ErrWeakPassword`
- `ErrUserAlreadyExist`

### 11.2 Repository -> domain error
- unique violation `lower(email)` -> `ErrUserAlreadyExist`
- unknown DB error -> wrap nội bộ

### 11.3 Handler -> HTTP
- `ErrInvalidArgument` -> `400 bad request`
- `ErrInvalidUsername` -> `400 bad request`
- `ErrInvalidEmail` -> `400 bad request`
- `ErrInvalidFullname` -> `400 bad request`
- `ErrPasswordMismatch` -> `400 bad request`
- `ErrWeakPassword` -> `400 bad request`
- `ErrUserAlreadyExist` -> `409 resource already exists`
- unknown -> `500 internal server error`

Response message phải generic.

---

## 12. Audit Event Rule

Register thành công phải tạo `audit_events` record với:
- `event_type = auth.register`
- `severity = info`
- `target_type = user`
- `target_id = newly created user id`
- `actor_user_id = NULL`
- `actor_admin_token_id = NULL`

`metadata` tối thiểu:
```json
{
  "username": "alice.nguyen",
  "email": "user@example.com",
  "status": "active",
  "source": "open_signup"
}
```

Không được ghi vào metadata:
- raw password
- password hash
- secret nội bộ

---

## 13. Explicit Non-Goals In V1

V1 này chưa làm:
- email verification state machine
- one-time token issuance
- login flow
- JWT issue flow
- refresh token flow
- device registration
- MFA bootstrap
- role assignment
- tenant/workspace bootstrap

---

## 14. Note For Next Phase

Nếu phase sau cần business rule kiểu:
- register xong nhưng chưa usable
- cần verify email trước khi login

thì current schema chưa biểu đạt trực tiếp bằng `users.status = pending` được.

Khi đó có 2 hướng:
1. thêm migration để mở rộng `user_status`
2. hoặc dùng flow verify/email token tách riêng mà vẫn giữ `users.status = active`, sau đó gate login theo policy khác

Spec v1 này **khóa theo schema hiện tại**, nên chọn hướng an toàn là:
- register thành công -> `users.status = active`
