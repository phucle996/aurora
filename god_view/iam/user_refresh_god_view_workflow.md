<!-- markdownlint-disable MD033 -->
# End-User Session Refresh & Sliding Session - Workflow God View

> [!NOTE]
> Tài liệu này đóng vai trò là **Source of Truth (SoT) / God View** cho luồng gia hạn và khôi phục phiên chạy (Session Refresh) của End-User.
> Mọi thay đổi về code liên quan đến luồng gia hạn/phục hồi phiên tại BFF, Frontend hoặc Controlplane phải tuân thủ nghiêm ngặt đặc tả này.

---

## 🗺️ 1. Giới Thiệu

### 👥 Tài Liệu Này Dành Cho Ai?
Tài liệu dành cho đội ngũ kỹ sư Frontend phát triển HTTP Client (fetcher), kỹ sư BFF, và Backend IAM chịu trách nhiệm về cơ chế bảo mật phiên và tối ưu trải nghiệm người dùng (UX) không bị gián đoạn.

### ❓ Phân Hệ Session Refresh là gì?
Hệ thống áp dụng mô hình quản lý phiên hai tầng (Multi-tier session renewal) để cân bằng giữa bảo mật và trải nghiệm người dùng:
1. **Kiểu 1 — Trinity Refresh (Sliding Session)**:
   - Khi người dùng đang hoạt động tích cực, nếu thời gian hết hạn của phiên hoạt động còn lại $\le 900$ giây, hệ thống sẽ thực hiện hoán đổi bộ thông tin xác thực cũ lấy bộ thông tin mới (Xoay vòng JWT, Access Key, Access Secret) để trượt cửa sổ phiên dài thêm 30 phút.
2. **Kiểu 2 — Opaque Refresh Token (Session Recovery)**:
   - Dành cho các thiết bị tin cậy (`TrustDevice = true`). Khi phiên hoạt động (Access Session) đã chết hoàn toàn (nhận HTTP 401), Client tự động dùng token đục dài hạn lưu trong cơ sở dữ liệu để phục hồi phiên hoạt động mới mà không buộc người dùng đăng nhập lại từ đầu.

### 📍 Các Biên Công Nghệ Hoạt Động
- **Frontend Cloud UI**: [fetcher.ts](file:///home/phucle/Desktop/New/cloud-ui/src/lib/api/fetcher.ts) (Kiểm soát Semaphore và tự động trigger cuộc gọi gia hạn).
- **Next.js BFF Proxy**: [route.ts](file:///home/phucle/Desktop/New/cloud-ui/src/app/bff/auth/[action]/route.ts) (Nhận cuộc gọi refresh từ browser, ẩn API endpoint của Go và đóng gói cookie).
- **Controlplane HTTP Handler**: [refresh_token_handler.go](file:///home/phucle/Desktop/New/controlplane/internal/iam/transport/http/handler/refresh_token_handler.go).
- **IAM Core Service**: `RefreshUserTrinity` và `RefreshUserOpaque` trong [session_refresh_service.go](file:///home/phucle/Desktop/New/controlplane/internal/iam/service/session_refresh_service.go).
- **Database (PostgreSQL)**: Bảng `iam.refresh_tokens` và `iam.devices`.
- **Cache Engine (Redis L2)**: Kiểm tra thông tin phiên, xoá cũ ghi mới (`iam:user_access_session:<UserID>:<AccessKey>`).

---

## 🏛️ 2. Sơ Đồ Hệ Thống & Ranh Giới Phase

```mermaid
graph TD
    Client["💻 Client (Browser)"]
    BFF["🌐 Next.js BFF"]
    Envoy["🛡️ Envoy Ingress Gateway"]
    CP["⚙️ Controlplane IAM (Go)"]
    Vault["🔑 HashiCorp Vault"]
    Redis[("⚡ Redis L2 (Runtime Sessions)")]
    DB[("🗄️ PostgreSQL (Refresh Tokens SoT)")]

    Client -- "1. POST /bff/auth/trinity-refresh OR /bff/auth/refresh" --> BFF
    BFF -- "2. Forward sanitized route" --> Envoy
    Envoy -- "3. Call Go API" --> CP
    CP -- "4. Sign claims" --> Vault
    CP -- "5. Update session payload" --> Redis
    CP -- "6. Check & rotate token family" --> DB
```

### 🚧 Biên Và Ràng Buộc (Boundaries & Constraints)
- **Bắt buộc qua BFF**: Tuyệt đối không cho phép client gọi trực tiếp API Refresh đến Controlplane. BFF đóng vai trò trung gian xử lý thiết lập cookie bảo mật (`HttpOnly`, `Secure`).
- **XSSI Prefix**: Mọi API trả về từ Controlplane đều đính kèm tiền tố `)]}',\n` ngăn chặn CSRF đọc trộm dữ liệu JSON. BFF/Frontend phải stripping tiền tố này trước khi parse.

---

## 🔍 3. Chi Tiết Thực Thi Nghiệp Vụ & Ánh Xạ

### 🔄 Trực Quan Luồng Sequence Diagram

```mermaid
sequenceDiagram
    autonumber
    participant UI as Browser Client (fetcher.ts)
    participant BFF as Next.js BFF Proxy
    participant Handler as CP HTTP Handler
    participant Service as Refresh Service
    participant Repo as PostgreSQL Repo
    participant Vault as HashiCorp Vault
    participant RDS as Redis L2 Cluster
    participant DB as PostgreSQL Database

    Note over UI, Handler: KỊCH BẢN 1: TRINITY REFRESH (SLIDING SESSION)
    UI->>Handler: Thực hiện API request thông thường
    Handler->>RDS: GET "iam:user_access_session:<UserID>:<OldAccessKey>"
    RDS-->>Handler: Trả về session metadata (ash, tdid, lsa)
    Note over Handler: Phát hiện phiên còn lại <= 900 giây (ví dụ: 800s)
    Handler-->>UI: Trả kết quả + Header "X-Session-Expires-In: 800"
    
    Note over UI: Client phát hiện sắp hết hạn -> Chiếm Semaphore khóa refresh
    UI->>BFF: POST /bff/auth/trinity-refresh (Old Cookies)
    BFF->>Handler: Forward POST /api/v1/auth/trinity-refresh
    
    Handler->>Service: RefreshUserTrinity(ctx, oldAccessKey, clientDeviceID, userIdentity)
    
    Service->>RDS: GET "iam:user_access_session:<UserID>:<OldAccessKey>"
    RDS-->>Service: Trả về session metadata (ash, tdid, lsa)
    Service->>Service: Verify Access Secret Hash khớp thông tin
    
    Service->>Vault: Yêu cầu ký Access Claims mới
    Vault-->>Service: Trả signed JWT Access Token mới
    
    Service->>Service: Sinh New Access Key (UUIDv7) & Access Secret
    
    Service->>RDS: TxPipeline: SET New Session (Protobuf) & DEL Old Session
    RDS-->>Service: Ghi nhận thành công
    
    Service-->>Handler: Trả về new trinity credentials
    Handler-->>BFF: Trả HTTP 204 + Set Cookies mới + Header "X-Session-Expires-In: 1800"
    BFF-->>UI: Cập nhật cookies trình duyệt thành công, giải phóng Semaphore

    Note over UI, Handler: KỊCH BẢN 2: OPAQUE REFRESH (SESSION RECOVERY)
    UI->>Handler: Thực hiện API request (Khi access session đã chết hoàn toàn)
    Handler-->>UI: Trả HTTP 401 Unauthorized
    
    Note over UI: Tự động bắt lỗi 401 -> Trigger khôi phục phiên bằng Refresh Token
    UI->>BFF: POST /bff/auth/refresh
    BFF->>Handler: Forward POST /api/v1/auth/refresh (Gửi kèm refresh_token cookie)
    
    Handler->>Service: RefreshUserOpaque(ctx, rawOldRefreshToken)
    
    Service->>Service: Băm SHA-256 Refresh Token thô
    Service->>Repo: GetRefreshTokenByHash(ctx, hashedToken)
    Repo->>DB: SELECT * FROM iam.refresh_tokens WHERE token_hash = ?
    DB-->>Repo: Trả về db model (iamModel.RefreshToken)
    Note over Repo: Chuyển DB model sang Entity:<br/>tokenEntity = RefreshTokenModelToEntity(dbModel)
    Repo-->>Service: Trả về tokenEntity (iamEntity.RefreshToken)
    
    alt Token không tồn tại / Device bị thu hồi
        Service-->>Handler: Return ErrInvalidSession
        Handler-->>BFF: Trả HTTP 401 Unauthorized
        BFF-->>UI: Redirect người dùng về trang Đăng Nhập (Login)
    else Hợp Lệ (Thực hiện Token Rotation)
        Service->>Service: Sinh New Opaque Refresh Token & New Access Credentials
        Service->>Vault: Ký Access Claims mới
        Vault-->>Service: Trả signed JWT Access Token
        
        Service->>Repo: RotateRefreshToken(ctx, oldTokenID, newTokenEntity)
        Note over Repo: Chuyển Entity sang DB model:<br/>dbToken = RefreshTokenEntityToModel(newTokenEntity)
        Repo->>DB: Transaction: DELETE old token ID, INSERT dbToken
        DB-->>Repo: Commit thành công
        Repo-->>Service: Return success
        
        Service->>RDS: Đóng gói & Lưu trữ session mới vào Redis L2
        Service-->>Handler: Trả về new credentials
        Handler-->>BFF: Trả HTTP 204 + Set Cookies mới
        BFF-->>UI: Trả 204 thành công -> Client tự động thử lại request cũ bị lỗi 401
    end
```

---

## 📋 4. Đặc Tả Hợp Đồng & Cơ Sở Dữ Liệu

### 1. Cookies Attribute Set-Cookie của Refresh Handler
Các cookies được BFF ghi nhận tại trình duyệt sau khi refresh thành công:
- `access_token`: JWT Token mới (HttpOnly=true, Secure=true, SameSite=Lax, Path=/).
- `refresh_token`: Opaque Refresh Token mới (HttpOnly=true, Secure=true, SameSite=Lax, Path=/).
- `access_key`: Access Key UUIDv7 mới (HttpOnly=false, Secure=true, SameSite=Lax, Path=/).
- `access_secret`: Access Secret mới (HttpOnly=true, Secure=true, SameSite=Lax, Path=/).

### 2. Service Domain Entities (`iamEntity`)
Các Entity hoạt động tại tầng nghiệp vụ logic (Service layer):

##### RefreshToken Entity
```go
type RefreshToken struct {
    ID            uuid.UUID
    UserID        uuid.UUID
    DeviceID      *uuid.UUID
    TokenHash     string
    TokenFamilyID uuid.UUID
    TenantID      *uuid.UUID
    IssuedAt      time.Time
    ExpiresAt     time.Time
}
```

##### RefreshTokenSession Entity
```go
type RefreshTokenSession struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	DeviceID      *uuid.UUID
	TokenHash     string
	TokenFamilyID uuid.UUID
	TenantID      *uuid.UUID
	ExpiresAt     time.Time
}
```

### 3. Repository DB Models (`iamModel`)
Mô hình ánh xạ trực tiếp cơ sở dữ liệu PostgreSQL:

##### RefreshToken DB Model
```go
type RefreshToken struct {
    ID            uuid.UUID  `db:"id"`
    UserID        uuid.UUID  `db:"user_id"`
    DeviceID      *uuid.UUID `db:"device_id"`
    TokenHash     string     `db:"token_hash"`
    TokenFamilyID uuid.UUID  `db:"token_family_id"`
    TenantID      *uuid.UUID `db:"tenant_id"`
    IssuedAt      time.Time  `db:"issued_at"`
    ExpiresAt     time.Time  `db:"expires_at"`
}
```

### 4. Converter & Mapping Layer (Service $\leftrightarrow$ Repository)
Tại ranh giới giữa Service và Repository, hệ thống thực hiện chuyển dịch kiểu dữ liệu thông qua các hàm converter trước khi thực hiện ghi/đọc xuống/từ CSDL:

```go
func RefreshTokenEntityToModel(input iamEntity.RefreshToken) RefreshToken {
    return RefreshToken{
        ID:            input.ID,
        UserID:        input.UserID,
        DeviceID:      input.DeviceID,
        TokenHash:     input.TokenHash,
        TokenFamilyID: input.TokenFamilyID,
        TenantID:      input.TenantID,
        IssuedAt:      input.IssuedAt,
        ExpiresAt:     input.ExpiresAt,
    }
}

func RefreshTokenModelToEntity(input RefreshToken) iamEntity.RefreshToken {
    return iamEntity.RefreshToken{
        ID:            input.ID,
        UserID:        input.UserID,
        DeviceID:      input.DeviceID,
        TokenHash:     input.TokenHash,
        TokenFamilyID: input.TokenFamilyID,
        TenantID:      input.TenantID,
        IssuedAt:      input.IssuedAt,
        ExpiresAt:     input.ExpiresAt,
    }
}
```

### 5. gRPC Verification Contract (gọi nội bộ từ ACL Service)
Dưới đây là giao thức gRPC để ACL Service xác thực Opaque Refresh Token và phân giải vai trò theo Scope:

```protobuf
service AuthService {
  rpc VerifyOpaqueRefreshToken (VerifyOpaqueRefreshTokenRequest) returns (VerifyOpaqueRefreshTokenResponse);
}

message VerifyOpaqueRefreshTokenRequest {
  string refresh_token = 1;
  string scope = 2;
}

message VerifyOpaqueRefreshTokenResponse {
  bool valid = 1;
  string user_id = 2;
  string tenant_id = 3;
  string role = 4;
  int32 level = 5;
  string zone_id = 6;
  string error_message = 7;
}
```

### 5. PostgreSQL Table Schema

##### Bảng Quản Lý Phiên Refresh `iam.refresh_tokens`
```sql
CREATE TABLE IF NOT EXISTS iam.refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,
    device_id UUID NOT NULL REFERENCES iam.devices(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,             -- SHA-256 Hash của opaque refresh token
    token_family_id UUID NOT NULL,        -- Dùng để phát hiện tấn công Replay (Token reuse)
    tenant_id UUID NULL,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_hash ON iam.refresh_tokens (token_hash);
```

---

## 🛡️ 5. Xử Lý Concurrency & Race Condition

### 1. Client-Side Semaphore (Chống Race Request Gia Hạn)
- **Rủi Ro**: Trên Dashboard, 5 widgets thực hiện gọi API đồng thời lúc token gần hết hạn. Cả 5 request đều nhận thấy TTL thấp và gửi yêu cầu `trinity-refresh` song song. Server sẽ xoay vòng liên tục 5 lần, làm 4 trong 5 credentials bị xoá ngay lập tức trên Redis L2, dẫn đến lỗi 401 ngẫu nhiên ở các widget sau.
- **Giải Pháp**:
  - Tầng Frontend HTTP client sử dụng một biến Promise dùng chung làm Semaphore khóa luồng:
    ```typescript
    let trinityRefreshPromise: Promise<void> | null = null;
    ```
  - Khi bắt đầu gọi gia hạn, request đầu tiên gán `trinityRefreshPromise = runRefresh()`.
  - Các request tiếp theo thấy biến này khác NULL sẽ thực hiện `await trinityRefreshPromise` mà không gửi thêm bất kỳ request nào lên server.

### 2. Phát Hiện Tấn Công Sử Dụng Lại Token Cũ (Token Reuse Detection / Replay Attack)
- **Rủi Ro**: Kẻ tấn công đánh cắp được Refresh Token cũ đã qua sử dụng và gửi yêu cầu cấp phiên mới.
- **Giải Pháp**:
  - Hệ thống áp dụng **Token Family**. Mỗi lần Opaque Refresh thành công, token cũ bị xoá và một token mới được sinh ra cùng một `token_family_id`.
  - Nếu server nhận được yêu cầu refresh bằng một token cũ đã bị xoá khỏi DB nhưng Family của nó vẫn còn hoạt động $\rightarrow$ Xác nhận bị rò rỉ hoặc tấn công Replay.
  - Hành động xử lý tức thời: Thu hồi (`Revoke`) lập tức toàn bộ các Refresh Tokens cùng nhóm Family ID đó, buộc tất cả các phiên chạy trên thiết bị liên quan đăng xuất lập tức, triệt tiêu nguy cơ chiếm đoạt phiên.

---

## 📊 6. Giám Sát Và Truy Vết - Grafana Runbook

### 1. Prometheus Metrics Cảnh Báo

- **Service Outcomes**: `iam_service_calls_total{op="refresh_user_opaque" | "refresh_user_trinity", outcome}`
- **PostgreSQL Rotate Latency**: `iam_downstream_call_duration_seconds{op="refresh_user_opaque", kind="repo", destination="RotateRefreshToken"}`

#### 📈 PromQL Cần Thiết
##### A. Tần suất xoay vòng sliding session (Trinity Refresh):
```promql
sum(rate(iam_service_calls_total{op="refresh_user_trinity"}[5m]))
```

##### B. Phát hiện lỗi tấn công sử dụng lại token (Token Reuse / Potential Attack):
```promql
sum(rate(iam_service_calls_total{op="refresh_user_opaque", outcome="invalid_session"}[5m]))
```

### 2. LogsQL (VictoriaLogs) Giám Sát

##### Tìm kiếm các trường hợp thu hồi toàn bộ token family do nghi ngờ replay:
```logsql
"token reuse detected" OR "revoking token family" level="warn"
```

---
*Tài liệu kết thúc.*
<!-- markdownlint-enable MD033 -->
