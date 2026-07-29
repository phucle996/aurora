# Managed Service Platform Module

Module **Managed Service Platform** chịu trách nhiệm quản lý catalog dịch vụ
do SRE định nghĩa và desired state của các dịch vụ mà Dataplane sẽ triển khai
trên Kubernetes đúng Zone trong các workflow sau này.

Ở trạng thái hiện tại, module mới chỉ có boundary và dependency wiring. Chưa
được phép thêm workflow business, endpoint, bảng PostgreSQL, outbox, Kafka hay
Kubernetes client vào bộ khung này nếu chưa có contract và God View tương ứng.

---

## 📁 Cấu trúc Thư mục (Directory Structure)

```text
internal/managedservice/
├── README.md              # Contract và quy chuẩn viết code của module
├── bootstrap.go            # Module lifecycle hook (hiện chưa có worker)
├── module.go              # Dependency Injection wiring của module shell
├── route.go               # HTTP route boundary; chưa đăng ký business route
├── domain/                # DDD Core Layer
│   ├── entity/            # Flat business entity riêng cho từng workflow
│   ├── repo/              # Một nhóm repository interfaces
│   └── service/           # Một nhóm service interfaces
├── model/                 # Persistence models và SQL scanning helpers
├── repository/            # PostgreSQL repository implementations
├── service/               # Business Logic Layer
├── taxonomy/              # Stable error taxonomy và permission constants
└── transport/             # API Interface Adapters Layer
    └── http/
        ├── dto/           # Request-only JSON structs
        └── handler/        # Gin handlers và inline gin.H responses
```

Các thư mục layer được giữ theo cùng convention với IAM, nhưng chưa tạo file
workflow rỗng. Khi ship workflow đầu tiên, file sẽ được đặt đúng layer:
`domain/entity`, `domain/repo`, `domain/service`, `model`, `repository`,
`service`, `taxonomy` và `transport/http/{dto,handler}`. `doc.go` không cần
thiết cho runtime; package chỉ xuất hiện khi có mã thực sự thuộc package đó.

Boundary cố định của module:

```text
SRE Console
    -> Controlplane Managed Service Catalog
    -> Controlplane PostgreSQL

Customer Console
    -> Controlplane Managed Service API
    -> Controlplane PostgreSQL

Controlplane
    - sở hữu catalog và desired state
    - không kết nối Kubernetes
    - không render manifest
    - không publish Kafka trực tiếp
```

Tên business được chuẩn hóa:

```text
ServiceCategory
ServiceDefinition
ServiceVersion
ServiceBlueprint
BlueprintRevision
ManagedServiceInstance
```

---

## 🧪 Test Suite và Coverage

Toàn bộ test của Managed Service Platform nằm dưới:

```text
internal/managedservice/test/
├── e2e/          # Workflow đầu-cuối ánh xạ 1-1 với God View
├── fixtures/     # Dữ liệu test, không phải business source
├── integration/  # PostgreSQL, migration, transaction và authorization
├── mocks/        # Test doubles cho boundary của workflow
└── unit/         # Handler/service/model theo public contract
```

Chạy test module:

```bash
go test ./internal/managedservice/...
go test ./internal/managedservice/... -covermode=atomic -coverprofile=/tmp/managedservice.cover
go tool cover -func=/tmp/managedservice.cover
```

Coverage của repository và handler phải có integration test với PostgreSQL thật
khi workflow đã sở hữu side effect. Unit test không được coi là đã cover
database transaction, CTE, constraint hoặc outbox durability.

---

## 🎨 Quy chuẩn Viết Code (Code Style & Guidelines)

### 1. Quy định về Database Migration (Baseline Clean Standard)

* Khi module bắt đầu sở hữu durable tables, thư mục `migrations/` chỉ được duy
  trì đúng **6 cặp file migration** (`000001` -> `000006`).
* Không tạo migration thứ 7. Bảng, index, function, trigger và seed mới phải
  được cập nhật vào đúng baseline file tương ứng.
* Migration phải idempotent, có advisory-lock khi chạy HA và không chứa secret,
  endpoint Kubernetes hoặc customer credential.
* Global app migration runner là owner của transaction. Module migration không
  được tự mở transaction lồng nhau nếu caller đã mở transaction.
* Bộ khung hiện tại chưa có durable table nên chưa tạo `migrations/` và chưa
  đăng ký migration giả.

Migration của module sẽ được thêm ở top-level `migration.go` và
`migrations/` khi workflow đầu tiên sở hữu durable state. Transaction và
advisory lock vẫn thuộc global app migration runner; module không mở transaction
lồng nhau.

### 2. Comment xen kẽ trong Code (`// [COMMENT]: ...`)

* Rải comment dạng `// [COMMENT]: ...` tại các invariant, security boundary,
  race condition, retry/ordering decision và các bước SQL khó đọc.
* Comment phải giải thích quyết định hoặc execution step, không lặp lại cú pháp.

```go
// [COMMENT]: Pin immutable blueprint revision trước khi tạo desired-state row
// để retry sau đó không nhìn thấy template head mới hơn.
revision := input.TemplateRevision
```

### 3. Kiểm tra Nil Strict (Fail-Fast Dependency Injection)

* Dependency của module phải được kiểm tra ngay lúc app dựng module graph.
* `module.go` chỉ chịu trách nhiệm wiring constructor và kiểm tra constructor
  result khi cần.
* Service, repository và handler không được lặp lại nil check dependency ở
  trong workflow.
* Controlplane không có fallback sang Kubernetes, Kafka, Redis hoặc một
  executor khác khi dependency không đúng contract.

### 4. Nguyên tắc Single Boundary Validation

* Handler là nơi đầu tiên nhận HTTP request và là nơi validate:
  - path/query/body;
  - code, version, UUID và pagination;
  - JSON Schema/UI Schema/manifest contract khi workflow publish blueprint;
  - ownership/routing context đã được Envoy/ACR inject.
* Service và repository tin tưởng entity đã được handler normalize.
* Service/repository không parse lại DTO và không lặp lại HTTP validation.
* Dataplane vẫn validate lại Protobuf, Zone binding, revision hash và rendered
  Kubernetes object vì đó là một transport trust boundary mới, không phải
  business validation lặp lại trong Controlplane.

### 5. Thiết kế theo Workflow-Driven và Cô lập Luồng

* Mỗi workflow có contract khép kín:

```text
1 Handler method
→ 1 Service method
→ 1 Repository method
```

* Repository dùng một CTE/transaction atomic cho mutation liên quan.
* Không query N+1 trong một workflow.
* Không dùng một `CatalogInput`, `ResourceEntity`, `TemplateEntity` chung cho
  mọi workflow.
* Trùng code giữa hai workflow được chấp nhận nếu điều đó giữ được isolation.
* Luồng SRE publish blueprint không được gọi trực tiếp code provisioning của
  customer; luồng customer create không được mutate catalog.
* Controlplane không biết chi tiết Kubernetes API và Dataplane không quyết định
  owner, workspace, Zone hay permission.

### 6. Quy tắc Không Logging tại Tầng Service

* Service không ghi log.
* Repository không log raw SQL parameter, manifest, customer parameter hoặc
  secret.
* Handler/middleware là nơi map lỗi, log operation context và trả response.
* Template, parameter, rendered manifest và secret không được log nguyên bản.
  Chỉ log operation ID, revision, hash, sanitized taxonomy và bounded size.

### 7. Quy tắc Entity Phẳng và Luồng Dữ liệu Duy nhất

* Mỗi workflow sở hữu một flat business entity riêng:
  `string`, `int`, `time.Time`, `uuid.UUID`, `[]byte` hoặc canonical JSON bytes
  khi contract thực sự dynamic.
* Không nhúng struct business vào struct workflow khác.
* Không dùng DTO transport làm entity service.
* Request JSON struct chỉ nằm trong `transport/http/dto`.
* Response không định nghĩa DTO; handler dựng inline bằng `gin.H`.

Luồng dữ liệu chuẩn:

```text
Handler
  Request DTO
  → validate tại transport boundary
  → map thành đúng một business entity

Service
  Entity
  → xử lý business rule
  → tạo Outbox Entity nếu workflow thật sự cần event

Repository
  Entity + Outbox Entity
  → một SQL CTE/transaction atomic
  → trả entity hoặc taxonomy error
```

Nguyên tắc cốt lõi:

* Một workflow chỉ có một business entity chính.
* Nếu có entity thứ hai thì đó phải là Outbox Entity hoặc một persistence
  projection được contract nêu rõ.
* Không lưu plaintext secret trong PostgreSQL, Redis, Kafka, Zone KV, log hoặc
  notification.
* Published blueprint revision là immutable; muốn sửa phải tạo revision mới.
* Customer instance luôn pin `template_id + revision + bundle_hash`, không đọc
  “latest template” ở downstream.
