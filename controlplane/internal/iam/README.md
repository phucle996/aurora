# IAM (Identity & Access Management) Module

Module **IAM** chịu trách nhiệm cho toàn bộ quy trình Xác thực (Authentication), Phân quyền (RBAC), Quản lý thiết bị (Device-Bound Auth), Đa yếu tố (MFA), và Đồng bộ Sự kiện (Billing Outbox / Event-Driven Messaging) trong hệ thống Cloud Native Aurora.

---

## 📁 Cấu trúc Thư mục (Directory Structure)

```text
internal/iam/
├── bootstrap.go         # Bootstrapping hệ thống & khởi tạo background workers
├── domain/              # DDD Core Layer (Entities, Interfaces)
│   ├── entity/          # Cấu trúc dữ liệu bất biến (User, Session, MFA, Role)
│   ├── repo/            # Repository Interfaces (AuthRepo, MfaRepo, UserRepo, RBACRepo)
│   └── service/         # Service Interfaces
├── migration.go         # Tích hợp Go embedFS runner cho SQL migrations
├── migrations/          # 6-step SQL Migration Baseline (000001 -> 000006)
│   ├── 000001_iam_enums.up.sql    # Shared ENUM types (user_status, billing_owner_type, role_scope_type)
│   ├── 000002_iam_tables.up.sql   # DDL các bảng chính thức (users, devices, mfa_settings, billing_outbox,...)
│   ├── 000003_iam_indexes.up.sql  # Indexes tối ưu hóa truy vấn & uniqueness partial constraints
│   ├── 000004_iam_funcs.up.sql    # Stored procedures & utility functions
│   ├── 000005_iam_triggers.up.sql # Auto-update updated_at triggers
│   └── 000006_iam_seeds.up.sql    # Bootstrapping System Users, Roles & Permissions
├── model/               # Internal data models & GORM/SQL scanning helpers
├── module.go            # Dependency Injection wiring (Chỗ khởi tạo Repo -> Svc -> Handler)
├── repository/          # PostgreSQL (pgx) + Redis persistence implementations
│   ├── auth_repo.go     # Thao tác bảng users, refresh_tokens, devices
│   ├── mfa_repo.go      # Thao tác bảng mfa_settings, mfa_recovery_codes (Replay prevention)
│   ├── rbac_repo.go     # Thao tác bảng roles, permissions, user_role (Protobuf binary list_perm)
│   └── user_repo.go     # Thao tác thông tin user profiles & status
├── route.go             # Đăng ký HTTP Gin Routes & Middleware Authorization
├── service/             # Business Logic Layer
│   ├── auth_service.go  # Xử lý đăng nhập, xoay vòng refresh token, logout, device binding
│   ├── mfa_service.go   # Xử lý đăng ký MFA TOTP, sinh mã QR, verify recovery codes
│   ├── rbac_service.go  # Kiểm tra quyền 5 cấp, phân cấp role hierarchy fence
│   └── user_service.go  # Quản lý tài khoản, thay đổi thông tin người dùng
├── taxonomy/            # Định nghĩa lỗi tĩnh, constants, và permission codes
├── test/                # Bộ IAM Test Suite chuẩn khép kín
│   ├── e2e/             # E2E Workflow Tests ánh xạ 1-1 với God View SOT
│   ├── fixtures/        # Mock Seed Users, Keypairs, Tokens
│   ├── integration/     # Integration tests (TOTP Replay attack prevention, RBAC Binary evaluation)
│   ├── mocks/           # Mock Kafka Publisher & Redis Cache Engine
│   └── unit/            # Unit tests độc lập cho Domain & Handlers
└── transport/           # API Interface Adapters Layer
    ├── http/            # Gin HTTP Handlers & DTOs
    ├── pubsub/          # Redis / Kafka PubSub Event Handlers
    └── rpc/             # gRPC Protobuf definitions & handlers
```

Toàn bộ IAM test code nằm dưới `internal/iam/test` và được phân theo boundary:

- `unit/` kiểm thử service/model/mocks qua public contract.
- `integration/` kiểm thử migration, middleware proof, RBAC và replay fence.
- `e2e/` kiểm thử các workflow IAM đầu-cuối theo God View.
- `fixtures/` và `mocks/` chỉ chứa test support, không phải business source.

Chạy bộ test IAM:

```bash
go test ./internal/iam/...
go test ./internal/iam/... -covermode=atomic -coverprofile=/tmp/iam.cover
go tool cover -func=/tmp/iam.cover
```

Coverage của repository/handler cần chạy thêm profile integration với PostgreSQL và
Redis test thật; các test unit không được coi là đã cover database side effect.

---

## 🏗️ Phân Vùng các Nhánh Nghiệp Vụ (Feature Domains & Components)

Module IAM bao gồm 7 phân vùng nghiệp vụ chính chạy xuyên suốt qua các tầng Architecture (Repository -> Service -> Handler):

1. **Authentication (`auth`)**:
   * Quản lý đăng nhập mật khẩu (`Argon2id`), xác thực JWT, xoay vòng Refresh Token (Token Rotation), thu hồi phiên làm việc, và Logout.
2. **Multi-Factor Authentication (`mfa`)**:
   * Quản lý kích hoạt 2FA TOTP (sinh secret `AES-256-GCM`, mã QR), chống Replay Attack (`last_accepted_totp_step`), và tiêu thụ mã khôi phục khẩn cấp (Recovery Codes).
3. **User Management (`user`)**:
   * Quản lý đăng ký tài khoản, xác thực Email token, trạng thái tài khoản (`pending-active`, `active`, `suspended`, `disabled`), cập nhật Profile, và chống tái sử dụng lịch sử mật khẩu (`password_history`).
4. **RBAC & Authorization (`rbac`)**:
   * Quản lý danh mục quyền tĩnh 3 cấp (`<module>:<object>:<behavior>`), biên dịch danh sách quyền tĩnh thành dạng nhị phân Protobuf (`list_perm`), phân định phạm vi Platform/Tenant/Workspace, và kiểm soát rào cản phân cấp quản trị (`role_level` hierarchy fence).
5. **Device-Bound Auth (`device`)**:
   * Đăng ký thiết bị đăng nhập, lưu trữ Khóa công khai Ed25519 & Fingerprint, theo dõi `client_device_id`, và thu hồi thiết bị đáng ngờ.
6. **External Identity & SSO (`external_identity`)**:
   * Tích hợp đăng nhập qua OAuth 2.0 / OIDC Providers (Google, GitHub), ánh xạ duy nhất `provider_subject`, và quản lý liên kết tài khoản.
7. **Billing Outbox Relay (`billing_outbox`)**:
   * Quản lý Transactional Outbox Pattern để đẩy các sự kiện nghiệp vụ sang domain Billing một cách tin cậy, hỗ trợ nhiều Pod Relay xử lý đồng thời với `SKIP LOCKED`.

---

## 🎨 Quy chuẩn Viết Code (Code Style & Guidelines)

### 1. Quy định về Database Migration (Baseline Clean Standard)
* **Giới hạn tuyệt đối 6 bước Migration**: Thư mục `migrations/` chỉ được phép duy trì đúng **6 cặp file migration** (`000001` -> `000006_iam_seeds`).
* **Không tạo thêm file migration mới**: Khi phát triển feature mới hoặc bổ sung DDL/Seeds, **bắt buộc phải cập nhật trực tiếp (update)** vào 6 file migration hiện có (ví dụ: bổ sung bảng/cột vào `000002_iam_tables.up.sql`, bổ sung index vào `000003_iam_indexes.up.sql`, bổ sung seed vào `000006_iam_seeds.up.sql`). **Tuyệt đối không tạo file migration thứ 7 (`000007_...`)**.

### 2. Comment xen kẽ trong Code (`// [COMMENT]: ...`)
* **Rải comment rà bắt Step**: Rải các comment dạng `// [COMMENT]: ...` xen kẽ trực tiếp bên trong thân hàm/câu SQL để đánh dấu từng bước thực thi (execution step).
* **Mục đích**: Giúp người đọc code dễ dàng nắm bắt hàm đang thực hiện những bước gì (Validate payload -> Check Cache -> Query DB -> Transaction Commit) mà không cần sa đà vào chi tiết cú pháp phức tạp.
```go
// [COMMENT]: Kiểm tra tài khoản người dùng có tồn tại trong hệ thống hay không
exists, err := s.repo.UserExists(ctx, userID)
if err != nil {
    return nil, err
}

// [COMMENT]: Sinh chuỗi bí mật TOTP ngẫu nhiên và mã hóa bằng Runtime Master Key
totpResult, err := security.GenerateTOTP("Aurora", userID.String())
```

### 3. Kiểm tra Nil Strict (Fail-Fast Dependency Injection)
* **Check nil tập trung tại `module.go`**: Mọi dependency (Repository, Service, Handler, Publisher, Cache) bắt buộc phải được kiểm tra `nil` ngay lập tức tại file `module.go` khi vừa khởi tạo để **Fail-Fast** (dừng khởi chạy app ngay lập tức nếu thiếu dependency).
* **Không check nil dư thừa bên trong hàm**: Khi các dependency đã được bảo đảm non-nil từ bước khởi tạo tại `module.go`, bên trong thân các phương thức/hàm xử lý nghiệp vụ **tuyệt đối không kiểm tra nil lại nữa** để tránh làm rác code và giảm hiệu năng.

### 4. Nguyên tắc Single Boundary Validation (Xác thực dữ liệu tại ranh giới tiếp nhận)
* **Handler chịu trách nhiệm Validate**: Nơi tiếp nhận dữ liệu đầu tiên (Transport/Handler Layer - HTTP Request DTOs, Path Params, JSON Payload) là nơi duy nhất thực hiện xác thực và kiểm tra định dạng dữ liệu (validation).
* **Service và Repository tin tưởng dữ liệu (Trust Policy)**: Tầng Service và Repository tin tưởng tuyệt đối vào dữ liệu đã được lọc và xác thực từ Handler, **tuyệt đối không lặp lại câu lệnh validate dư thừa** ở tầng dưới.

### 5. Thiết kế theo Workflow-Driven & Cô lập Luồng (Workflow Isolation)
* **Khép kín 1 Workflow**: Mỗi luồng nghiệp vụ (Workflow) được thiết kế khép kín và rõ ràng contract thông qua bộ 3 tương ứng: **1 Handler -> 1 Service -> 1 Repository method**.
* **Dùng CTE phòng chống truy vấn N+1**: Tầng Repository thực thi SQL phức tạp sử dụng **CTE (Common Table Expressions)** trong 1 query duy nhất để gom toàn bộ thao tác ghi/đọc dữ liệu liên quan, triệt tiêu hoàn toàn vấn đề truy vấn lặp `N+1`.
* **Ưu tiên Cô lập Luồng hơn Trùng lặp Code (Isolation > DRY)**: Việc trùng lặp code (Duplicate code) giữa các luồng hoàn toàn được chấp nhận. **Quan trọng nhất là Luồng A không dùng chung code thi hành với Luồng B** để tránh phụ thuộc chéo (coupling), giúp thay đổi hoặc bảo trì Luồng A không bao giờ gây lỗi tác động phụ (side-effect) sang Luồng B.

### 6. Quy tắc Không Logging tại Tầng Service (No-Logging in Service Layer)
* **Service Không Log**: Tầng Service tuyệt đối **không ghi log** (`logger.SysError`, `logger.Error`,...). Service chỉ tập trung xử lý Business Logic và trả lỗi (`return err` / `return fmt.Errorf(...)`) lên tầng trên.
* **Handler chịu trách nhiệm Log**: Tầng Transport / Handler (hoặc Middleware) là nơi duy nhất thực hiện việc bắt lỗi và ghi log hệ thống (`logger.HandlerError`).

### 7. Quy tắc Entity Phẳng & Luồng Dữ liệu Duy nhất (Single Business Entity Pipeline)
* **1 Workflow = 1 Entity Phẳng (Flattened Entity)**: Mỗi quy trình nghiệp vụ (Workflow) sở hữu DTO/Entity riêng biệt dạng phẳng (`string`, `int`, `time.Time`, `uuid.UUID`), **tuyệt đối không nhúng struct trong struct (`struct` in `struct`)** hay dùng chung Entity giữa các luồng.
* **Luồng luân chuyển dữ liệu chuẩn (Clean Data Pipeline Flow)**:
  1. **Handler (Transport Layer)**: Tiếp nhận Request DTO -> Thực hiện **Validate** dữ liệu tại ranh giới -> Chuyển đổi (Map) DTO thành **duy nhất 1 Entity Nghiệp vụ** (Business Entity).
  2. **Service (Business Layer)**: Nhận **1 Entity Nghiệp vụ** từ Handler để xử lý logic -> Nếu luồng cần ghi log/bắn sự kiện, Service tạo thêm **Outbox Entity** (Activity Event / Billing Outbox Event).
  3. **Repository (Persistence Layer)**: Tiếp nhận **duy nhất 1 Entity Nghiệp vụ** và các **Entity Outbox** đi kèm (nếu có) -> Thực thi lưu trữ PostgreSQL trong 1 câu SQL CTE / Transaction duy nhất.
* **Nguyên tắc cốt lõi (Core Rule)**: Trong mọi workflow, **chỉ tồn tại duy nhất 1 Entity Nghiệp vụ**. Nếu xuất hiện Entity thứ 2 trong cùng 1 luồng, đó **bắt buộc phải là Outbox Entity**.



