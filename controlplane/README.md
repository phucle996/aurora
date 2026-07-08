# Aurora Control Plane Architecture & Development Guide

Tài liệu này định nghĩa cấu trúc kiến trúc, quy ước đặt tên (Naming Convention), và mô hình thiết kế của các Module nghiệp vụ trong dự án `controlplane`. Tất cả các nhà phát triển bắt buộc phải tuân thủ hướng dẫn này để đảm bảo tính đồng bộ, bảo mật và khả năng mở rộng (HA/Scale) của hệ thống.

---

## 1. Tổng quan Kiến trúc Control Plane

`controlplane` đóng vai trò là bộ não điều phối trung tâm của nền tảng Cloud Native. Nó chịu trách nhiệm nhận yêu cầu quản trị, xác thực phân quyền, quản lý trạng thái nghiệp vụ chuẩn hóa (Source of Truth) lưu tại PostgreSQL, và phân phối chỉ thị cho các Dataplane hoặc Agent chạy ở biên.

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
2. **Tích hợp Telemetry & Metrics tại Service**:
   * Mọi lời gọi nghiệp vụ quan trọng hoặc các truy vấn xuống Database/Redis downstream đều phải được đo đạc Latency và ghi nhận vào hệ thống Metrics thông qua OTel SDK:
     ```go
     start := time.Now()
     res, err := s.repo.SomeAction(...)
     observability.CurrentMetrics().ObserveDependency("db", "module.object.action", time.Since(start), err)
     ```
3. **Cơ chế Bảo mật Token & Session**:
   * ACR (Biên) chịu trách nhiệm xác thực Trinity Credentials thô qua cookie và kiểm tra session trạng thái tại Redis L2.
   * Control Plane nhận thông tin định danh và phân quyền an toàn qua các Header đáng tin cậy đã được ACR inject: `x-user-id`, `x-user-level`, `x-tenant-id`, `x-workspace-id`, `x-zone-id`.