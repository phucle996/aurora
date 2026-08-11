# Aurora Control Plane Architecture & Development Guide

Tài liệu này định nghĩa cấu trúc kiến trúc, quy ước đặt tên (Naming Convention), và mô hình thiết kế của các Module nghiệp vụ trong dự án `controlplane`. Tất cả các nhà phát triển bắt buộc phải tuân thủ hướng dẫn này để đảm bảo tính đồng bộ, bảo mật và khả năng mở rộng (HA/Scale) của hệ thống.

---

## 1. Tổng quan Kiến trúc Control Plane

`controlplane` đóng vai trò là bộ não điều phối trung tâm của nền tảng Cloud Native. Nó chịu trách nhiệm nhận yêu cầu quản trị, xác thực phân quyền, quản lý trạng thái nghiệp vụ chuẩn hóa (Source of Truth) lưu tại PostgreSQL, và phân phối chỉ thị cho các Dataplane hoặc Agent chạy ở biên.

Môi trường Docker development được vận hành bên ngoài module bằng hai stack
Central/Zone độc lập; xem [dev/README.md](../dev/README.md) để biết boundary,
thứ tự start/stop và quy tắc giữ data volume.

### Nguyên tắc Luồng Dữ liệu (Strict Layered Data Flow)
Luồng đi của dữ liệu là **luồng một chiều nghiêm ngặt** từ ngoài vào trong:
```
Client Request ──> [Envoy/ACR] ──> [HTTP Handler] ──> [Service Layer] ──> [Repository Layer] ──> [PostgreSQL]
```
* **Không đi ngược chiều phụ thuộc**: Các tầng bên dưới tuyệt đối không được tham chiếu hay biết đến sự tồn tại của các tầng bên trên (ví dụ: Repository không được import Service, Service không được import Handler/HTTP).
* **Cô lập Context**: Tầng Service và Repository hoàn toàn tách biệt khỏi HTTP Framework (`gin.Context`, HTTP Headers, Status Code). `gin.Context` chỉ được tồn tại trong tầng HTTP Handler.

---

## 2. Cấu trúc Chuẩn của một Module Nghiệp vụ

Mỗi module nghiệp vụ (nằm dưới thư mục `internal/<module_name>/`) phải tuân thủ cấu trúc phân lớp như sau:

```
internal/<module_name>/
├── bootstrap.go             # Khởi tạo Dependency Injection, compose repository, service của module
├── module.go                # Định nghĩa struct Module chứa các interface handler, service, repo xuất ra ngoài
├── route.go                 # Định nghĩa và đăng ký các HTTP API endpoint vào các Router Group tương ứng
├── migration.go             # Trình quản lý chạy các file migrations của module
├── migrations/              # Thư mục chứa các tệp SQL up/down migration cho Database
├── domain/                  # Lớp Core Domain (Độc lập, không phụ thuộc framework)
│   ├── entity/              # các struct và enum Thực thể nghiệp vụ không có tag json/map/db...
│   ├── repo/                # Định nghĩa interface cho Repository layer
│   └── service/             # Định nghĩa interface cho Service layer
├── model/                   # struct chứa tag db ánh xạ các bảng trong db 
├── repository/              # implementation interface domain/repo, map 1 vài lỗi nghiệp vụ thông thường sang taxonomy, còn lại return error raw để handler trả internal error 
├── service/                 # implementation interface domain/service
├── taxonomy/                # Phân loại và định nghĩa các mã lỗi (Errors Taxonomy)
├── metrics/                 # Chứa các bộ đo lường (OTel Instruments) riêng của module
├── transport/               # Lớp Giao vận nhận/trả request
│   ├── http/
│   │   ├── dto/             # Data Transfer Objects (Request/Response JSON Binding Structs)
│   │   └── handler/         # HTTP Handlers (Nhận gin.Context, map error, trả JSON)
│   └── rpc/                 # Hiện thực hóa các cổng gRPC/gRPC-Web (nếu có)
```

### Bảng Quy Tắc Thiết Kế Chi Tiết Cho Từng Layer

| Tầng (Layer) | Trách nhiệm chính (Responsibility) | Quy tắc Triển khai (Implementation Rules) & Các Package Hỗ trợ |
| :--- | :--- | :--- |
| **HTTP Handler** | Tiếp nhận Request, làm sạch dữ liệu, gọi Service, map kết quả và trả về Client. | <ul><li>**Trích xuất HTTP Headers**: Sử dụng các helper có sẵn trong package `pkg/constant` để lấy header </li><li>**Xử lý thô dữ liệu (Sanitization & Validation)**: Tiến hành `strings.TrimSpace`, lowercase mã code, convert/normalize dữ liệu đầu vào. Bind JSON payload thông qua struct DTO thích hợp trong thư mục `dto/req`.</li><li>**Logging**: Dùng pkg/logger để ghi log tương ứng.</li><li>**Phản hồi (Response)**: response bằng pkg/apires - không leak thông tin qua response  .</li></ul> |
| **Service Layer** | Thực thi logic nghiệp vụ, phối hợp điều phối dữ liệu giữa các Repository và Invalidation Bus. | <ul><li>**Độc lập hạ tầng mạng**: Không tham chiếu tới `gin.Context`, HTTP status, hay HTTP headers. Chỉ nhận dữ liệu sạch thông qua Go struct (Entity/DTO) và `context.Context`.</li><li>**Đo lường (Metrics Telemetry)**: Trước khi thực hiện gọi xuống tầng Repository hoặc các hệ thống ngoài (downstream/DB), bắt buộc phải ghi nhận latency thông qua OpenTelemetry bằng service call và downstream call của module.</li><li>**Xử lý lỗi**: Kiểm tra điều kiện nghiệp vụ và trả về các lỗi định danh thuộc Errors Taxonomy của module đó khi phát hiện bất thường.</li></ul> |
| **Repository Layer** | Thực hiện các câu lệnh truy vấn cơ sở dữ liệu PostgreSQL | <ul><li>**Chuyển đổi thực thể (Mapping)**: Đầu vào nhận Core Entity (`domain/entity/x.go`). Chuyển đổi (map) trường dữ liệu từ Entity sang Database Model (`model/x.go`) có chứa tags db (`db:"field"`).</li><li>**Chạy câu lệnh (Execute)**: Thực thi các truy vấn SQL thô hoặc CTE (Common Table Expressions) bằng thư viện `pgxpool`.</li><li>**Chuyển đổi đầu ra**: Scan kết quả trả về từ database vào Database Model (`model/x.go`), sau đó ánh xạ ngược lại Core Entity trước khi return về tầng Service.</li><li>**Xử lý lỗi cơ sở dữ liệu**: Lọc các lỗi vi phạm ràng buộc khóa ngoại, trùng khóa hoặc thiếu dòng (`ErrNoRows`) để map sang lỗi nghiệp vụ Taxonomy tương ứng (e.g. `coreTaxonomy.ErrZoneNotFound`). Trả các lỗi truy vấn DB thô (raw error) khác về để Handler tự động phản hồi `500 Internal Server Error`.</li></ul> |
| **API Routing** | Định tuyến yêu cầu HTTP đến các HTTP Handlers tương ứng. | <ul><li>**Cấu trúc URL chuẩn Scope (Route Rewrite)**: Phân tách rõ ràng giữa 3 phạm vi hoạt động theo mẫu: `/api/v1/[platform\|tenant\|personal]/[module]/[object]/[behavior]` (Ví dụ: `/api/v1/personal/hierarchy/workspaces/catalog`).</li><li>**Phân quyền truy cập (Authorize Middleware)**: Bảo vệ tài nguyên bằng `middleware.Authorize` đi kèm quyền tương ứng định dạng `<module>:<object>:<behavior>` (Ví dụ: `hierarchy:workspace:read`) cùng phân cấp Level yêu cầu.</li><li>**Phân hoạch 3 Scope**:<ul><li>`platform`: Dành cho Platform Admin vận hành hệ thống (check Authn + Authz).</li><li>`tenant`: Dành cho Tenant (Doanh nghiệp) quản lý nhóm tài nguyên cô lập (check Authn + Authz).</li><li>`personal`: Dành cho các tài nguyên cá nhân của người dùng (Ví dụ: Workspace cá nhân dưới `/api/v1/personal/...`), check Authn + Authz.</li></ul></li><li>**Cổng thông tin cá nhân `/api/v1/me`**: Yêu cầu xác thực danh tính từ biên bảo mật (ACR check authn hợp lệ) để lấy thông tin phiên làm việc hiện tại của user, nhưng **không kiểm tra quyền hạn** qua Authorize Middleware.</li><li>**Cổng xác thực chưa đăng nhập `/auth`**: Các API phục vụ đăng nhập, đăng ký hoặc cấp lại token. Biên bảo mật (ACR) và Control Plane đều **bỏ qua hoàn toàn việc kiểm tra xác thực lẫn phân quyền** (bypass authn & authz).</li><li>**Ngoại lệ Hot Path (Bypass Authorize)**: Các đường dẫn hot-path phục vụ lấy catalog (nhằm lấy workspace ID ban đầu) không sử dụng Authorize Middleware để tránh xung đột vòng lặp (chicken-and-egg). An toàn được đảm bảo bằng việc xác thực session danh tính từ Edge (Envoy/ACR) và tự động lọc dữ liệu thuộc sở hữu ở tầng Service/Repository.</li></ul> |

---

## 3. Quy ước Đặt tên Hàm (Naming Conventions)

Để tăng tính nhất quán và dễ đọc hiểu, tên hàm trong toàn bộ Control Plane được đặt tên theo các quy tắc ngữ nghĩa nghiêm ngặt sau:

### 3.1. Tầng HTTP Handler: Quy tắc `BehaviorObjectScope`
Tên hàm tại HTTP Handler bắt buộc phải phản ánh đầy đủ: **Hành vi (Behavior)** + **Đối tượng tác động (Object)** + **Phạm vi ngữ cảnh (Scope)**.

$$\text{Handler Function Name} = \text{Behavior} + \text{Object} + \text{Scope}$$

* **Behavior**: `List` (Lấy danh sách), `Create` (Tạo mới), `Update` (Cập nhật), `Delete` (Xóa), `Get` (Lấy chi tiết).
* **Object**: `Users`, `Roles`, `Permissions`, `Devices`,...
* **Scope**: Phân biệt rõ ràng ngữ cảnh hệ thống Platform hay Tenant cô lập.
  * `Platform`: Dành cho các API thuộc Platform Administrator (ví dụ: `/api/v1/platform/...`).
  * `Tenant`: Dành cho các API thuộc Tenant Administrator hoặc phạm vi của một Tenant cụ thể (`/api/v1/tenant/...`).

* **Ví dụ mẫu**:
  * `ListUsersPlatform` (Lấy danh sách User hệ thống ở Platform scope)
  * `UpdateUserStatusPlatform` (Vô hiệu hóa/Cập nhật trạng thái User hệ thống ở Platform scope)
  * `ListRolesPlatform` (Lấy danh sách Roles ở Platform scope)
  * `ListRolesTenant` (Lấy danh sách Roles thuộc về một Tenant nhất định)

---

### 3.2. Tầng Service: Quy tắc `BehaviorObject` hoặc `BehaviorObjectBy[Condition]`
Tầng Service tập trung vào xử lý nghiệp vụ thuần túy, do đó Scope (Platform/Tenant) sẽ được truyền qua tham số context/ID chứ không đặt ở tên hàm. Tên hàm Service tuân theo: **Hành vi (Behavior)** + **Đối tượng tác động (Object)** và có thể có thêm điều kiện `By[Condition]`.

$$\text{Service Function Name} = \text{Behavior} + \text{Object} \ [\ + \text{By} + \text{Condition}\ ]$$

* **Ví dụ mẫu**:
  * `ListUsers` hoặc `ListUsersByLevel` (Nhận tham số level để lọc)
  * `UpdateUserStatus` (Cập nhật trạng thái của một User)
  * `CreateRole` (Tạo một vai trò mới)
  * `GetRenderContext` (Lấy thông tin cấu trúc hiển thị)

---

### 3.3. Tầng Repository: Quy tắc DB Persistence
Tầng Repository thực hiện lưu trữ bền vững dưới PostgreSQL. Các hàm thường đặt tên tương tự như Service (`BehaviorObject`) hoặc sử dụng các tiền tố thao tác dữ liệu chuẩn của Go Database:

* **Ví dụ mẫu**:
  * `ListUsers` (Quét dữ liệu users theo điều kiện)
  * `UpdateUserStatus` (Chạy lệnh SQL UPDATE trạng thái)
  * `GetUserProfile` (Chạy query SELECT thông tin profile)

---

## 4. Nguyên tắc Phát triển và Tối ưu hóa (HA & Security)

1. **Optimize & Tránh Race Condition**:
   * Khi thực hiện các hành động ghi hoặc cập nhật trạng thái liên quan đến phân cấp quyền lực (Hierarchy), luôn kiểm tra chéo level bảo mật của Actor thực hiện hành động so với Target (Ví dụ: `Actor Level <= Target Level` thì mới cho phép chỉnh sửa).
   * Sử dụng transactions thích hợp đối với các tác vụ ghi đồng thời.
2. **Tích hợp Telemetry & Metrics theo ownership**:
   * Handler gắn operation tĩnh vào context; service ghi đúng một workflow outcome
     qua `WorkflowRecorder` được module inject.
   * PostgreSQL, Redis và Kafka dependency metrics chỉ được ghi tại adapter chung;
     service không đo lại downstream call để tránh double-count.
   * Không dùng global metric singleton, raw path, UUID, owner/workspace/zone ID,
     topic, Redis key, SQL text hoặc raw error làm metric label. Contract đầy đủ nằm
     tại [TELEMETRY.md](TELEMETRY.md).
3. **Cơ chế Bảo mật Token & Session**:
   * ACR (Biên) chịu trách nhiệm xác thực Trinity Credentials thô qua cookie và kiểm tra session trạng thái tại Redis L2.
   * Control Plane nhận thông tin định danh và phân quyền an toàn qua các Header đáng tin cậy đã được ACR inject: `x-user-id`, `x-user-level`, `x-tenant-id`, `x-workspace-id`, `x-zone-id`.
