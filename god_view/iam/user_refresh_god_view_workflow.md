<!-- markdownlint-disable MD033 -->
# End-User Session Refresh & Sliding Session - Workflow God View

> [!NOTE]
> Tài liệu này đóng vai trò là **Source of Truth (SoT) / God View** cho luồng gia hạn và khôi phục phiên chạy (Session Refresh) của End-User.
> Mọi thay đổi về code liên quan đến luồng gia hạn/phục hồi phiên tại Frontend hoặc Controlplane phải tuân thủ nghiêm ngặt đặc tả này.

---

## 🗺️ 1. Giới Thiệu

### 👥 Tài Liệu Này Dành Cho Ai?

Tài liệu dành cho đội ngũ kỹ sư Frontend phát triển HTTP Client (fetcher), và Backend IAM chịu trách nhiệm về cơ chế bảo mật phiên và tối ưu trải nghiệm người dùng (UX) không bị gián đoạn.

### ❓ Phân Hệ Session Refresh là gì?

Hệ thống áp dụng mô hình quản lý phiên hai tầng (Multi-tier session renewal) để cân bằng giữa bảo mật và trải nghiệm người dùng:

1. **Kiểu 1 — Trinity Refresh (Sliding Session)**:
   - Khi người dùng đang hoạt động tích cực, nếu thời gian hết hạn của phiên hoạt động còn lại $\le 900$ giây, hệ thống sẽ thực hiện hoán đổi bộ thông tin xác thực cũ lấy bộ thông tin mới (Xoay vòng JWT, Access Key, Access Secret) để trượt cửa sổ phiên dài thêm 30 phút.
2. **Kiểu 2 — Opaque Refresh Token (Session Recovery)**:
   - Dành cho các thiết bị tin cậy (`TrustDevice = true`). Khi phiên hoạt động (Access Session) đã chết hoàn toàn (nhận HTTP 401), Client tự động dùng token đục dài hạn lưu trong cơ sở dữ liệu để phục hồi phiên hoạt động mới mà không buộc người dùng đăng nhập lại từ đầu.

### 🌐 Sơ đồ Kiến trúc Tổng quan (System Architecture)

Sơ đồ dưới đây mô tả cấu trúc các thành phần tham gia vào luồng Refresh và cách chúng tương tác qua các kết nối đồng bộ (đường nét liền) và bất đồng bộ/tuần tự (đường nét đứt):

```mermaid
graph TD
    classDef client fill:#332244,stroke:#8844ff,stroke-width:2px;
    classDef gateway fill:#112233,stroke:#3388ff,stroke-width:2px;
    classDef edgeService fill:#113322,stroke:#33cc88,stroke-width:2px;
    classDef storage fill:#333311,stroke:#cccc33,stroke-width:2px;
    classDef control fill:#331111,stroke:#ff5555,stroke-width:2px;

    Client["💻 Client (Browser)"]:::client
    Envoy["🛡️ Envoy Ingress Gateway"]:::gateway
    ACL["🦀 Rust ACL (ext_authz)"]:::edgeService
    CP["⚙️ Controlplane IAM (Go)"]:::control
    Vault["🔑 HashiCorp Vault"]:::control
    Redis[("⚡ Redis L2 (Runtime Sessions)")]:::storage
    DB[("🗄️ PostgreSQL (Refresh Tokens SoT)")]:::storage

    Client -- "1. Request bất kỳ" --> Envoy
    Envoy -- "2. ext_authz check" --> ACL
    ACL -- "3a. Verify JWT & Session" --> Redis
    ACL -- "3b. TTL thấp → Sign JWT mới" --> Vault
    ACL -- "3c. Rotate session (SETNX lock)" --> Redis
    ACL -. "3d. Verify Opaque Token (gRPC)" .-> CP
    ACL -- "4. OK + Set-Cookie (nếu rotate/recover)" --> Envoy
    Envoy -- "5. Forward to upstream" --> CP
    Client -- "6. POST /api/v1/auth/refresh (khi 401)" --> Envoy
    Envoy -- "7. Bypass ext_authz" --> CP
    CP -- "8. Rotate Token (Mark Used & INSERT)" --> DB
```

### 🔑 Mô hình Token (Trinity Credentials)

Chi tiết cấu trúc, vai trò và hành vi của các thông tin xác thực/cookies khi thực hiện quá trình gia hạn phiên:

| Tên Token/Cookie | Loại/Định dạng | Nơi Lưu Trữ Gốc (Server) | Hành động khi Refresh | Mô tả & Vai trò |
| :--- | :--- | :--- | :--- | :--- |
| **`access_token`** | JWT (Vault signed) | Không lưu (Verify stateless) | **Xoay vòng** (Cấp JWT mới khi refresh thành công) | Chứa các định danh claims (`sub`, `role`, `lvl`, `tenant_id`, `zone_id`) và `access_key`. |
| **`access_key`** | UUIDv7 (Plain) | **Redis L2** (Làm khóa phiên) | **Xoay vòng** (Thay thế khóa phiên cũ trên L2 và cập nhật cookie) | Định danh phiên làm việc, dùng để đối chiếu trực tiếp dữ liệu phiên runtime tại lớp L2 Redis. |
| **`access_secret`** | Secure Random String (Plain) | **Redis L2** (Lưu băm `ash`) | **Xoay vòng** (Thay thế khóa bí mật cũ trên L2 và cập nhật cookie) | Khóa bí mật thô giúp client/Envoy kiểm tra tính toàn vẹn phiên làm việc nhanh chóng. |
| **`refresh_token`** | Opaque String | **PostgreSQL** (Lưu băm SHA-256) | **Xoay vòng** (Mark used & INSERT mới với old expiry - Kiểu 2) | Token dài hạn dùng để phục hồi phiên Access Session mới khi phiên cũ đã hết hạn. |
| **`client_device_id`** | UUID (Plain) | **PostgreSQL** | **GIỮ NGUYÊN** (Không thay đổi) | Định danh duy nhất của thiết bị phục vụ kiểm tra bảo mật và liên kết session. |

### 🗃️ Các Khóa Lưu Trữ & Bộ Nhớ Được Tương Tác (Storage & Cache Keys Registry)

Dưới đây là toàn bộ các khóa lưu trữ tại mọi phân tầng (L1 Cookies, L2 Redis Cache, DB PostgreSQL) mà luồng Refresh này trực tiếp tương tác và xử lý:

| Phân Tầng Lưu Trữ | Tên Khóa / Bảng | Kiểu Dữ Liệu | Hành Động Xử Lý | Chi Tiết & Vai Trò |
| :--- | :--- | :--- | :--- | :--- |
| **L1: Browser Cookies** | `access_token` | HTTP Cookie (JWT) | **Xoay vòng** (Cập nhật mới) | Xoá/Cấp mới cookie token truy cập tại client trình duyệt khi hết hạn/gia hạn. |
| **L1: Browser Cookies** | `access_key` | HTTP Cookie (UUIDv7) | **Xoay vòng** (Cập nhật mới) | Xoá/Cấp mới cookie khóa truy cập dùng để tra cứu phiên tại L2. |
| **L1: Browser Cookies** | `access_secret` | HTTP Cookie (String) | **Xoay vòng** (Cập nhật mới) | Xoá/Cấp mới cookie secret dùng để xác thực và mã hóa phiên. |
| **L1: Browser Cookies** | `refresh_token` | HTTP Cookie (Opaque) | **Xoay vòng** (Chỉ ở Kiểu 2) | Xoá/Cấp mới cookie refresh_token khi xoay vòng token trên DB. |
| **L1: Browser Cookies** | `client_device_id` | HTTP Cookie (UUID) | **GIỮ NGUYÊN** (Không thay đổi) | Giữ nguyên định danh thiết bị để phục vụ kiểm tra bảo mật và lưu vết thiết bị. |
| **L2: Redis Cache** | `iam:user_access_session:<UserID>:<AccessKey>` | String (Protobuf) | **Gia hạn / Chuyển tiếp** | Tạo session key mới (TTL 30 phút), session key cũ giảm TTL xuống 5 giây (Grace Period - hardcoded). |
| **L2: Redis Cache** | `iam:user_access_index:<UserID>` | Set | **Cập nhật tập hợp (SADD & SREM)** | Thêm `AccessKey` mới và loại bỏ `AccessKey` cũ khỏi danh sách quản lý phiên của người dùng. |
| **L2: Redis Cache** | `iam:lock:refresh:<OldAccessKey>` | String | **Khóa phân tán (SET NX EX / DEL)** | Tạo khóa Mutex Lock tồn tại 5s để chống race condition khi nhiều widget gọi refresh đồng thời. |
| **DB: PostgreSQL** | Bảng `iam.refresh_tokens` | Hàng dữ liệu (Row) | **Token Rotation (Mark used & INSERT)** | Đánh dấu token cũ là used_at = now() và tạo token mới với old_expiry kế thừa thời hạn tuyệt đối. |

### 🚧 Biên Và Ràng Buộc (Boundaries & Constraints)

- **Bảo mật và Định tuyến**: Luồng Sliding Session (Kiểu 1) được xử lý transparent tại Rust ACL (`ext_authz`) trên Envoy Gateway — Client không cần gọi endpoint riêng. Luồng Session Recovery (Kiểu 2) gọi trực tiếp `/api/v1/auth/refresh` qua Envoy. Envoy Gateway chuyển tiếp nguyên trạng HTTP Headers (bao gồm `Set-Cookie`) giữa client và backend.
- **XSSI Prefix**: Mọi API trả về từ Controlplane đều đính kèm tiền tố `)]}',\n` ngăn chặn CSRF đọc trộm dữ liệu JSON. Frontend Client (fetcher) phải tự động stripping tiền tố này trước khi parse JSON.

---

## 🏛️ 2. Chi Tiết Thực Thi Nghiệp Vụ & Sơ Đồ Trình Tự

Quy trình Refresh được chia thành hai nhánh độc lập tùy thuộc vào điều kiện trạng thái phiên làm việc hiện tại của Client:

### Sơ đồ nhánh 1 — Transparent Trinity Refresh (Sliding Session tại ACL)

Áp dụng tự động khi phiên làm việc hiện tại vẫn còn hiệu lực nhưng chuẩn bị hết hạn (thời gian còn lại $\le 900$ giây). Rust ACL (ext_authz) tự động phát hiện JWT sắp hết hạn, xoay vòng Trinity Credentials và inject `Set-Cookie` header vào phản hồi Envoy để trình duyệt tự động cập nhật cookies mà Client hoàn toàn không cần quan tâm.

**Các file mã nguồn liên quan (Code References):**

- [acl/src/service/rotate.rs](../../acl/src/service/rotate.rs): Hàm `handle_session_rotation` phát hiện JWT TTL thấp, sinh bộ trinity mới và inject Set-Cookie.
- [acl/src/service/ext_authz.rs](../../acl/src/service/ext_authz.rs): Hàm `check` gọi `handle_session_rotation` sau khi xác thực thành công.
- [acl/src/core/session.rs](../../acl/src/core/session.rs): Hàm `try_rotate_session` thực hiện xoay vòng Redis L2 có bảo vệ bằng Distributed Lock (SETNX).

```mermaid
sequenceDiagram
    autonumber
    participant UI as Browser Client
    participant Envoy as Envoy Ingress Gateway
    participant ACL as Rust ACL (ext_authz)
    participant Vault as HashiCorp Vault
    participant RDS as Redis L2 Cluster

    Note over UI, ACL: Luồng transparent — Client không cần gọi endpoint riêng
    UI->>Envoy: Request API thông thường (GET /api/v1/...)
    Envoy->>ACL: ext_authz Check (Cookies: access_token, access_key, access_secret)
    ACL->>ACL: Verify JWT (claims.exp - now = remaining_ttl)
    ACL->>RDS: GET "iam:user_access_session:{UserID}:{AccessKey}"
    RDS-->>ACL: Trả về session metadata (ash, tdid, lsa)
    ACL->>ACL: Verify Access Secret Hash khớp
    ACL->>RDS: Update Last Seen At (Throttled ghi nếu now - lsa >= 30s)
    
    alt remaining_ttl <= 900s (Cần Sliding Refresh)
        ACL->>RDS: SETNX "iam:lock:refresh:{OldAccessKey}" (TTL 5s — Distributed Lock)
        RDS-->>ACL: Lock acquired (true)
        ACL->>ACL: Sinh New Access Key (UUIDv7) & Access Secret
        ACL->>Vault: Sign new JWT Access Claims
        Vault-->>ACL: Trả signed JWT mới
        ACL->>RDS: Pipeline: SET New Session + EXPIRE Old Session (Grace 5s)
        RDS-->>ACL: Thành công
        ACL->>RDS: DEL lock key (giải phóng sớm)
        ACL-->>Envoy: OK + Set-Cookie headers (access_token, access_key, access_secret)
    else remaining_ttl > 900s (Session còn đủ TTL)
        ACL-->>Envoy: OK (không rotate)
    end
    
    Envoy-->>UI: Response + Set-Cookie (nếu có) → Trình duyệt tự cập nhật cookies
```

---

### Sơ đồ nhánh 2 — Opaque Refresh Token (Session Recovery tại ext_authz với Redis Distributed Lock/Cache)

Áp dụng tự động khi phiên làm việc hiện tại (Trinity Credentials) không hợp lệ hoặc đã hết hạn, nhưng Client vẫn giữ `refresh_token` cookie hợp lệ. Khi đó, Envoy chuyển tiếp yêu cầu xác thực đến Rust ACL (ext_authz).

Để bảo vệ hệ thống và đảm bảo ngữ cảnh nhất quán, **Client bắt buộc phải cung cấp thông tin zone_code** (qua cookie `zone_code` hoặc custom header `x-zone-code` / `X-Zone-Code`). Hệ thống chỉ phân giải từ `zone_code` ra `zone_id`, không hỗ trợ phân giải ngược từ header `x-zone-id` do client gửi lên. Nếu không cung cấp `zone_code` hợp lệ, ACL từ chối ngay lập tức (400 Bad Request).

Để chống hiện tượng **Thundering Herd** (nhiều requests đồng thời từ client kích hoạt hàng loạt cuộc gọi gRPC và truy vấn DB song song gây tải cho hệ thống - Blood Request) cũng như chống spam các zone code không tồn tại, ACL kết hợp cơ chế **Distributed Singleflight** qua Redis L2 và **Negative Caching (bad_codes)** tại L1:

1. **Kiểm tra và Phân giải Zone (Fail-Fast)**: ACL trích xuất `zone_code` từ request. Nếu thiếu, trả lỗi 400 Bad Request ngay.
   - Nếu `zone_code` nằm trong danh sách đen `bad_codes` (blacklist cache trong RAM L1), từ chối ngay lập tức bằng 400 Bad Request mà không gọi gRPC/CP.
   - Nếu miss L1 cache, ACL thực hiện Single-Flight gọi gRPC `GetZoneList` sang Controlplane để cập nhật danh sách zone.
   - Nếu sau khi gọi gRPC vẫn không tìm thấy, `zone_code` đó được đưa vào cache `bad_codes` (blacklist với TTL 5 phút) để chặn spam ở các request sau.
   - Nếu tìm thấy, kiểm tra trạng thái hoạt động (active). Nếu status != "active", trả lỗi 403 Forbidden.
2. **Kiểm tra Cache**: ACL kiểm tra cache kết quả hồi phục `iam:recovery_cache:<token_hash>` trong Redis L2. Nếu có (cache hit), trả về ngay bộ Trinity Credentials mới đã được ký gắn liền với `zone_id` đã phân giải.
3. **Chiếm Lock**: Nếu chưa có, ACL thử chiếm lock phân tán `iam:lock:recovery:<token_hash>` (TTL 5s).
   - **Leader (giành được lock)**: Thực hiện cuộc gọi gRPC `VerifyOpaqueRefreshToken` sang Controlplane (gRPC không còn trả về `zone_id`), kiểm tra phân quyền nâng cao (ví dụ: cấm user thường vào zone global), ký JWT mới có chứa `zone_id` đã phân giải qua Vault, đăng ký session mới trong Redis, lưu kết quả vào `iam:recovery_cache:<token_hash>` (TTL 5s) và giải phóng lock.
   - **Follower (không giành được lock)**: Polling Redis cache `iam:recovery_cache:<token_hash>` mỗi 100ms (tối đa 3 giây) cho đến khi leader ghi kết quả thành công thì lấy ra sử dụng.

**Các file mã nguồn liên quan (Code References):**

- [acl/src/service/ext_authz.rs](../../acl/src/service/ext_authz.rs): Hàm `check` bắt lỗi xác thực Trinity, gọi hoặc chuyển giao tiếp nhận cho module recovery.
- [acl/src/service/recovery_session.rs](../../acl/src/service/recovery_session.rs): Module chính thực hiện kiểm tra sự tồn tại của `zone_code`, phân giải zone, điều phối phân tán singleflight, gRPC client và Vault signing.
- [acl/src/core/session.rs](../../acl/src/core/session.rs): Các hàm thao tác Redis L2 (cache, try_lock_recovery, is_recovery_locked, release_recovery_lock).
- [controlplane/internal/iam/transport/rpc/handler/auth.go](../../controlplane/internal/iam/transport/rpc/handler/auth.go): Handler gRPC tiếp nhận `VerifyOpaqueRefreshToken` và gọi Service để kiểm tra token.
- [controlplane/internal/iam/service/session_refresh_service.go](../../controlplane/internal/iam/service/session_refresh_service.go): Hàm core `VerifyOpaqueRefreshToken` thực hiện băm và đối chiếu trạng thái token trong PostgreSQL.

#### Sơ đồ Phase 1 — Ingress Auth Gate & Distributed Singleflight (Xử lý tại Rust ACL)

```mermaid
sequenceDiagram
    autonumber
    participant UI as Browser Client (Requests Song Song)
    participant Envoy as Envoy Ingress Gateway
    participant ACL as Rust ACL (ext_authz)
    participant RDS as Redis L2 Cluster
    participant CP as Controlplane (Go)
    participant Vault as HashiCorp Vault

    Note over UI, Envoy: 1. Các request song song từ trình duyệt gửi lên khi Trinity hết hạn
    UI->>Envoy: Request 1 & Request 2 đồng thời (Kèm refresh_token & zone_code)
    Envoy->>ACL: ext_authz Check (Thiếu Trinity cookies, có refresh_token)
    
    Note over ACL: 2. Kiểm tra Zone Context & Fail-Fast (Không phân giải ngược zone_id)
    alt Khách hàng thiếu cookie zone_code hoặc x-zone-code header
        ACL-->>Envoy: DENIED 400 Bad Request (Missing zone context)
        Envoy-->>UI: HTTP 400 Bad Request
    else Có zone_code
        ACL->>ACL: Tra cứu L1 Cache RAM (valid zones)
        alt L1 Cache Hit (Phần lớn trường hợp)
            Note over ACL: Tìm thấy Zone -> Đi tiếp tới kiểm tra Cache & Phân bổ Lock Session
        else L1 Cache Miss (Trường hợp hiếm)
            alt zone_code nằm trong bad_codes (blacklist L1 cache)
                ACL-->>Envoy: DENIED 400 Bad Request (Zone code cached as invalid)
                Envoy-->>UI: HTTP 400 Bad Request
            else Không nằm trong blacklist L1 cache
                ACL->>CP: gRPC GetZoneList()
                alt Vẫn không tìm thấy zone_code trong danh sách mới
                    ACL->>ACL: Lưu zone_code vào bad_codes (blacklist cache, TTL 5m)
                    ACL-->>Envoy: DENIED 400 Bad Request (Requested zone code not found)
                    Envoy-->>UI: HTTP 400 Bad Request
                else Tìm thấy
                    ACL->>ACL: Cập nhật L1 Cache
                end
            end
        end
        alt Zone status != "active"
            ACL-->>Envoy: DENIED 403 Forbidden (Zone inactive)
            Envoy-->>UI: HTTP 403 Forbidden
        end
    end

    Note over ACL, RDS: 3. Kiểm tra Cache & Phân bổ Leader / Follower
    ACL->>RDS: GET iam:recovery_cache:<token_hash> (Không tìm thấy)
    ACL->>RDS: SET iam:lock:recovery:<token_hash> EX 5 NX
    
    alt Request 1: Giành được Lock (Leader)
        RDS-->>ACL: Thành công (True)
        ACL->>CP: gRPC VerifyOpaqueRefreshToken(refresh_token) (Gửi sang Phase 2)
        CP-->>ACL: VerifyOpaqueRefreshTokenResponse (Trả về kết quả kiểm tra)
        
        alt TH1.a: Token không hợp lệ / Hết hạn
            ACL->>RDS: DEL iam:lock:recovery:<token_hash> (Giải phóng Lock)
            ACL-->>Envoy: DENIED 401 Unauthorized + Clear Cookies
            Envoy-->>UI: HTTP 401 Unauthorized (Clear Cookies)
            Note over UI: UI xóa auth cache & redirect sang /signin
        else TH1.b: Token Hợp Lệ
            Note over ACL: Thực hiện kiểm tra quyền truy cập Zone đối với User
            alt User thường truy cập Zone Global ("global" / nil UUID)
                ACL->>RDS: DEL iam:lock:recovery:<token_hash> (Giải phóng Lock)
                ACL-->>Envoy: DENIED 403 Forbidden (Global zone restricted)
                Envoy-->>UI: HTTP 403 Forbidden
            else Hợp Lệ
                ACL->>ACL: Sinh Trinity Credentials mới
                ACL->>Vault: Ký Access Claims mới (Chứa zone_id vừa phân giải)
                Vault-->>ACL: Trả signed JWT Access Token
                ACL->>RDS: Đăng ký session mới (register_session)
                ACL->>RDS: Ghi cache kết quả (set_recovery_cache & DEL Lock)
                ACL-->>Envoy: OK + Set-Cookie Trinity & Zone mới + Injected Headers (x-zone-id)
                Envoy-->>UI: Trả kết quả thành công + Cookie mới cho Request 1
            end
        end
    else Request 2: Không giành được Lock (Follower)
        RDS-->>ACL: Thất bại (False)
        loop Polling (Mỗi 100ms, tối đa 3 giây)
            ACL->>RDS: GET iam:recovery_cache:<token_hash>
            alt Tìm thấy kết quả Cache
                RDS-->>ACL: Trả về RecoverySessionCache
                ACL-->>Envoy: OK + Set-Cookie Trinity & Zone mới + Injected Headers (x-zone-id) (Bypass gRPC/Vault/Redis registration)
                Envoy-->>UI: Trả kết quả thành công + Cookie mới cho Request 2 (Hoàn thành hồi phục transparent)
            else Không tìm thấy
                Note over ACL, RDS: Tiếp tục chờ hoặc tự khôi phục nếu Lock bị giải phóng mà không có cache
            end
        end
    end
```

#### Sơ đồ Phase 2 — Core Opaque Verification & Token Rotation (Xử lý tại Go Controlplane)

```mermaid
sequenceDiagram
    autonumber
    participant ACL as Rust ACL (ext_authz)
    participant Handler as Go RPC Handler
    participant Service as Go Auth Service
    participant Repo as Go Token Repo
    participant DB as PostgreSQL Database

    ACL->>Handler: gRPC VerifyOpaqueRefreshToken(refresh_token)
    Handler->>Service: VerifyOpaqueRefreshToken(ctx, refresh_token)
    
    Service->>Service: Băm SHA-256 Refresh Token thô
    Service->>Repo: GetTokenByHash(ctx, hash)
    Repo->>DB: SELECT ... FROM refresh_tokens WHERE token_hash = <hash>
    
    alt TH2.a: Token không tồn tại hoặc đã hết hạn
        DB-->>Repo: Không tìm thấy / Hết hạn
        Repo-->>Service: Trả về None / Error (Expired/Not Found)
        Service-->>Handler: Trả về lỗi xác thực
        Handler-->>ACL: VerifyOpaqueRefreshTokenResponse (valid=false)
    else TH2.b: Token đã được sử dụng trước đó (Replay Attack)
        DB-->>Repo: Trả về bản ghi Token (used_at IS NOT NULL)
        Repo-->>Service: Trả về TokenRecord
        Service->>Service: Phát hiện used_at != null (Replay Attack)
        Service->>Repo: RevokeRefreshTokensByDeviceIDAndUserID(user_id, device_id)
        Repo->>DB: DELETE FROM refresh_tokens WHERE user_id = <user_id> AND device_id = <device_id>
        DB-->>Repo: Thành công
        Repo-->>Service: Hoàn thành thu hồi phiên
        Service-->>Handler: Trả về lỗi xác thực (Invalid Session)
        Handler-->>ACL: VerifyOpaqueRefreshTokenResponse (valid=false)
    else TH2.c: Token đã bị thu hồi từ trước
        DB-->>Repo: Trả về bản ghi Token (revoked_at IS NOT NULL)
        Repo-->>Service: Trả về TokenRecord
        Service-->>Handler: Trả về lỗi xác thực (Invalid Session)
        Handler-->>ACL: VerifyOpaqueRefreshTokenResponse (valid=false)
    else TH2.d: Token Hợp Lệ (used_at và revoked_at đều NULL)
        DB-->>Repo: Trả về bản ghi Token (Hợp lệ)
        Repo-->>Service: Trả về TokenRecord
        
        Service->>Service: Thực hiện Token Rotation (Phương án 2)
        Service->>Repo: RotateRefreshToken(current, next)
        
        Note over Repo,DB: Bắt đầu Transaction
        Repo->>DB: UPDATE refresh_tokens SET used_at = now() WHERE id = <id> AND used_at IS NULL AND revoked_at IS NULL
        Repo->>DB: INSERT INTO refresh_tokens (id, user_id, device_id, token_hash, tenant_id, issued_at, expires_at) VALUES (..., <old_expiry>)
        Note over Repo,DB: Commit Transaction
        DB-->>Repo: Thành công
        
        Repo-->>Service: Trả về kết quả xoay vòng thành công
        Service-->>Handler: Trả về dữ liệu phiên (valid=true, user_id, role, level, tenant_id)
        Handler-->>ACL: VerifyOpaqueRefreshTokenResponse (valid=true, ...)
    end
```

---

### 🌐 Chi tiết Phân giải & Chuyển đổi Ngữ cảnh Zone (Zone Context & Switch Workflow)

Toàn bộ quy trình phân giải Zone, các ràng buộc bảo mật đối với người dùng (không cho phép user thường truy cập zone global hay zone inactive), và cơ chế chuyển đổi ngữ cảnh Zone tường minh (Explicit Zone Switch via `/api/v1/zone/go-to-zone`) được mô tả chi tiết tại:

👉 [Tài liệu Thiết kế Zone Context & Switch Workflow](user_zone_context_god_view_workflow.md)

---

## 📊 3. Giám Sát Và Truy Vết (Observability & Distributed Tracing)

Hệ thống hoạt động trên môi trường Cloud-Native và High Availability (HA), yêu cầu giám sát toàn diện thông qua 3 trụ cột Observability: Tracing (OpenTelemetry), Metrics (Prometheus) và Structured Logs (VictoriaLogs/Loki).

### 1. Truy Vết Phân Tán (OpenTelemetry Distributed Tracing)

Mọi request đi qua API Gateway đều được gắn một mã định danh duy nhất (`Trace ID`) thông qua tiêu chuẩn **W3C Trace Context** (`traceparent`). Mã này được truyền tải xuyên suốt các dịch vụ:
`UI (Browser) -> Envoy -> Rust ACL (ext_authz) -> Go Controlplane (VerifyOpaqueRefreshToken) -> CSDL (PostgreSQL/Redis)`.

#### A. Thuộc tính Trace (Span Attributes) chuẩn hóa cho IAM

Để phục vụ điều tra và phân tích hành vi, các Spans liên quan đến phiên làm việc bắt buộc phải được đính kèm các thuộc tính:

- `iam.user.id`: UUID của người dùng.
- `iam.device.id`: UUID của thiết bị (TDID).
- `iam.tenant.id`: UUID của Tenant (nếu có).
- `iam.session.access_key`: Key tra cứu session trong Redis L2.
- `iam.token.id`: ID định danh phiên Refresh Token trong DB.
- `iam.auth.outcome`: Kết quả xác thực (`success`, `invalid_token`, `session_expired`, `internal_error`).

#### B. Ánh xạ Trace ID vào Log

Trong Rust ACL (`logger.rs`) và Go Controlplane, Trace ID luôn được trích xuất từ Context hiện tại và ghi trực tiếp vào cấu trúc Log JSON:

```json
{
  "timestamp": "2026-06-23T05:12:00Z",
  "level": "info",
  "trace_id": "8a3f9d2c1e4b8f0a3c2d1e0f9a8b7c6d",
  "service": "aurora-acl",
  "message": "Transparent session recovery successful for user_id=d3b07384-d113-4c91-9e59-00f723821033",
  "iam": {
    "user_id": "d3b07384-d113-4c91-9e59-00f723821033",
    "device_id": "f5a04384-213c-4a34-a4f2-10f723828941"
  }
}
```

---

### 2. Chỉ Số Giám Sát (Prometheus Metrics & Dashboard)

Hệ thống theo dõi các chỉ số quan trọng (Golden Signals) để đưa ra cảnh báo sớm về hiệu năng và bảo mật.

#### A. Các chỉ số Prometheus chính

- `iam_auth_requests_total{type="trinity"|"opaque", outcome="success"|"failed"|"error"}`: Tổng số lượt xác thực phân loại theo Trinity Credentials hoặc Opaque Refresh Token.
- `iam_grpc_client_duration_seconds`: Thời gian phản hồi gRPC từ ACL sang Go Controlplane (VerifyOpaqueRefreshToken).
- `iam_redis_operation_duration_seconds{op="get_session"|"register_session"}`: Độ trễ thao tác đọc/ghi trên Redis L2.
- `iam_vault_sign_duration_seconds`: Độ trễ ký khóa JWT tại HashiCorp Vault.

#### B. PromQL Dashboard & Alerting Rules (Sử dụng cho Grafana Alert)

##### 🚨 Cảnh báo Tỷ Lệ Lỗi Hệ Thống Lớn (Infrastructure Failure Rate > 1%)

Cảnh báo kích hoạt nếu các lỗi hạ tầng (Vault/Redis chết) chiếm hơn 1% tổng request trong 2 phút liên tiếp:

```promql
sum(rate(iam_auth_requests_total{outcome="error"}[2m])) 
/ 
sum(rate(iam_auth_requests_total[2m])) * 100 > 1
```

##### 🚨 Cảnh báo Phát Hiện Tấn Công Sử Dụng Lại Token (Opaque Token Reuse Detection)

Số lượng token cũ được gửi lên bất thường chỉ thị nguy cơ bị rò rỉ và replay token:

```promql
sum(rate(iam_auth_requests_total{type="opaque", outcome="token_reuse_detected"}[5m])) > 0
```

##### 🚨 Cảnh Báo Độ Trễ gRPC Verify Opaque Token Quá Cao (p99 > 200ms)

Gây nghẽn tại Gateway do Controlplane phản hồi chậm hoặc DB PostgreSQL quá tải:

```promql
histogram_quantile(0.99, sum(rate(iam_grpc_client_duration_seconds_bucket{op="VerifyOpaqueRefreshToken"}[5m])) by (le)) > 0.200
```

---

### 3. Truy Vấn Nhật Ký Cấu Trúc (LogQL / VictoriaLogs Runbook)

Dùng để truy vết sự cố nhanh khi xảy ra lỗi trên môi trường phân tán HA.

##### A. Truy vết vòng đời của một Session (End-to-End Trace) qua Trace ID

Khi một API của người dùng bị lỗi, copy `trace_id` nhận được từ Header response và thực hiện câu truy vấn:

```logsql
trace_id: "8a3f9d2c1e4b8f0a3c2d1e0f9a8b7c6d"
```

##### B. Phát hiện các cuộc gọi Opaque Refresh thất bại liên tục từ cùng 1 IP (Nguy cơ brute force / dò quét token)

```logsql
"ext_authz.recovery_session" AND "invalid" | "denied"
| stats count() by client_ip
| filter count > 20
```

##### C. Thống kê lỗi hệ thống hạ tầng (Vault / Redis không phản hồi)

```logsql
"ext_authz.recovery_session" AND "Failed to" AND "Vault" OR "Redis"
```

---
*Tài liệu kết thúc.*
<!-- markdownlint-enable MD033 -->
