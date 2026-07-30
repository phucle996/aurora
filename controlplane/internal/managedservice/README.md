# Managed Service Platform Module

Module **Managed Service Platform** chịu trách nhiệm quản lý catalog dịch vụ
do SRE định nghĩa và desired state của các dịch vụ mà Dataplane sẽ triển khai
trên Kubernetes đúng Zone trong các workflow sau này.

P01 đã ship durable baseline và canonical inner protobuf binding. P02 đã ship SRE
catalog/admin API, immutable revision workflow và Admin UI. P03 đã ship customer
catalog/form read-only cho personal/tenant cùng Cloud Console dynamic-form foundation.
Customer mutation, Kafka producer/consumer Managed Service, renderer và Kubernetes
client vẫn chưa được mở; các workflow đó chỉ được thêm sau God View/phase tương ứng.

Chi tiết product proposal nằm trong [IDEA.md](./IDEA.md). Trình tự staging từ
contract tới release gate trước khi tách phase/task nằm trong
[STAGING.md](./STAGING.md).
Kế hoạch triển khai theo phase, dependency, owner và acceptance evidence nằm trong
[PHASES.md](./PHASES.md).
Source of Truth end-to-end của lifecycle nằm trong
[Managed Service Lifecycle God View](../../../god_view/managedservice/managed_service_lifecycle_god_view.md).
Registry ownership cho protobuf inner contract nằm tại
[contracts/proto/README.md](../../../contracts/proto/README.md); fixture vocabulary
P00 nằm tại [test/fixtures/CONTRACT.md](./test/fixtures/CONTRACT.md).

---

## 📁 Cấu trúc Thư mục (Directory Structure)

```text
internal/managedservice/
├── README.md              # Contract và quy chuẩn viết code của module
├── IDEA.md                # Product idea và contract nền tảng
├── STAGING.md             # Design/release staging gates
├── PHASES.md              # Phase/task execution plan
├── test/                  # Managed Service test suite, cùng layout với IAM
│   ├── e2e/               # Workflow end-to-end theo God View
│   ├── fixtures/          # Fixture vocabulary/data, không phải runtime helper
│   │   └── CONTRACT.md
│   ├── integration/       # Migration, CTE, transaction và transport boundary
│   ├── mocks/             # Test doubles cho dependency workflow
│   └── unit/              # Unit/contract tests độc lập
├── bootstrap.go            # Module lifecycle hook (hiện chưa có worker)
├── migration.go            # Embedded six-file durable baseline migration
├── migrations/             # 000001..000006 only; no seventh baseline migration
├── module.go              # Fail-fast wiring của từng object slice
├── route.go               # Admin metadata/read và critical runtime routes
├── domain/                # DDD Core Layer
│   ├── entity/            # Flat business entity riêng cho từng workflow
│   ├── repo/              # Repository interface tách theo object
│   └── service/           # Service interface tách theo object
├── model/                 # Persistence models và workflow-local SQL scanning
├── repository/            # PostgreSQL repository implementations
├── service/               # Business Logic Layer
├── taxonomy/              # Stable error taxonomy và route-policy names
└── transport/             # API Interface Adapters Layer
    ├── http/
    │   ├── dto/           # Request-only JSON structs
    │   └── handler/        # Gin handlers và inline gin.H responses
    └── proto/              # Generated binding from contracts/proto/managed_service.proto
```

Các thư mục layer giữ cùng convention với IAM/Storage. Một file branch sở hữu
toàn bộ hành vi của đúng một object; không gom toàn catalog vào một file:

```text
domain/entity/                  category.go ... audit.go
domain/repo/                    category.go ... audit.go
domain/service/                 category.go ... audit.go
repository/                     category_repo.go ... audit_repo.go
service/                        category_service.go ... audit_service.go
transport/http/dto/             category.go ... revision.go
transport/http/handler/         category_handler.go ... audit_handler.go
```

Mỗi object có interface riêng trong `domain/repo` và `domain/service`; constructor
implementation trả về interface đó. Trong object, một workflow vẫn là đúng một
handler method → service method → repository method. Không tạo `helpers.go`,
`common.go`, generic mapper, generic validator hoặc `MutateInstance`. `doc.go`
không cần thiết cho runtime; package chỉ xuất hiện khi có mã thực sự thuộc package đó.

Boundary cố định của module:

```text
Admin UI
    -> Envoy / ACR admin-route policy
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

Managed Service V1 không có NATS Core subject, runtime protobuf hay JO runtime
consumer. Durable command/result chỉ đi PostgreSQL outbox/WAL → JO → Kafka →
Dataplane → Kafka result. Không có path Managed Service sang Shared Redis Pub/Sub,
Notification runtime, Centrifugo runtime, Zone Public Edge hoặc Browser; Console chỉ
rehydrate operation/timeline durable. Một future NATS soft-state workflow phải có
concrete JO consumer và God View riêng, không được suy ra từ module này.

```text
Managed Service workload / Kubernetes telemetry
    -> Zone OTel Collector
    -> Zone VictoriaMetrics / VictoriaLogs
    -> zone-observability-stream (Rust service, Zone-local, read-only)
    -> Zone Public Edge
    -> Browser
```

`zone-observability-stream` là Rust subproject/Deployment riêng của Zone, không
phải package của Dataplane và không có credential Kafka, NATS, PostgreSQL, Zone KV
hay Controlplane. Zone Control Edge xử lý assertion/short-lived scoped ticket;
Zone Public Edge chỉ mở một stream đã được kiểm tra scope tại thời điểm connect.
Service tự inject scope verified `zone + workspace + owner + instance + component`,
chỉ chấp nhận panel/query allow-list và không nhận raw PromQL/LogsQL. Terminal
operation vẫn đi Kafka/Controlplane/timeline durable path.

SRE catalog là admin-plane riêng:

```text
Admin UI
    -> Envoy / ACR admin-route policy
    -> /admin/managed-services/catalog/*
    -> Controlplane object handler tương ứng

Admin UI (runtime/catalog lifecycle mutation)
    -> Envoy / ACR critical-proof policy
    -> /admin/critical/managed-services/catalog/*
    -> Controlplane object handler tương ứng
```

Route SRE không gắn `middleware.Authorize` tại Controlplane và không đọc RBAC
permission/level. Envoy phải strip header nội bộ do client gửi, ACR/Envoy là
gate duy nhất trước khi forward, rồi inject trusted actor identity chỉ để audit
(`published_by`, `retired_by`). Thiếu actor identity phải fail-close; đó là audit
boundary, không phải CP tự đánh giá quyền. Route có `/critical/` để ACR verify và
consume proof bind method/path/body; Controlplane chỉ fail-close nếu marker/challenge
ID do ACR inject bị thiếu. Blueprint/draft/validate/publish/retire/delete không có
normal-route mirror: chúng luôn đi thẳng qua `/admin/critical/`. Repository CTE vẫn
quyết định transition/pin hợp lệ; critical proof không bypass immutable revision,
FK hoặc hard-delete guard. Customer route vẫn đi qua nhánh
`/api/v1/personal/managed-services/*` hoặc `/api/v1/tenant/managed-services/*`
và middleware authorization tương ứng.

P03 customer discovery có đúng bốn route read-only:

```text
GET /api/v1/personal/managed-services/catalog
GET /api/v1/personal/managed-services/catalog/versions/:version_id
GET /api/v1/tenant/managed-services/catalog
GET /api/v1/tenant/managed-services/catalog/versions/:version_id
```

Chúng dùng `managed-service:catalog:read`, typed context từ `pkg/context` và CTE
workflow-local để bind workspace với owner/tenant + Zone. Public response không có
YAML, component contract, selector/capability hay audit. Server không cache scoped
catalog trong Redis; response `private, no-store`, còn Console cache RAM được fence
bằng auth generation + owner mode + Zone + workspace + revision. List dùng keyset
cursor và page tối đa 100; Console chỉ tải page tiếp theo theo action hữu hạn. P03
không đăng ký `POST /instances`.

Zone admission của module bắt buộc capability `managed_service` trong
`hierarchy.zone_service_type`. Mọi personal/tenant catalog và version-detail query
kiểm tra durable `zone_services.desired_state=true` trước requirement riêng của
revision; `actual_state` không phải eligibility SoT. Capability này chưa là runtime
health signal và không được làm Zone draining khi chưa có observer/policy contract
riêng.

Mọi handler trả response qua `pkg/apires`: payload vẫn dùng `gin.H`, success envelope
là `{data,message}`, error envelope là `{error,message}`. Stable taxonomy cần code
riêng phải được bổ sung theo HTTP status trong `pkg/apires`; handler không gọi
`c.JSON` trực tiếp và không tạo generic `respondError` dùng chung giữa workflow.

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

Test hiện có migration/route contract, canonical protobuf và revision-handler
security/validation boundary. `mocks/` cũng chia theo object, không tạo mock module chung.

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
* P01 đã sở hữu đúng sáu cặp baseline tại `migrations/000001` tới `000006` và
  `migration.go` đã được global app migration runner gọi trong cùng transaction/
  advisory lock. Baseline gồm system catalog, immutable blueprint revision, physical
  personal/tenant aggregate và outbox; không có route hoặc dispatcher đang hoạt động.

S09 đã chốt physical ownership cho persistence:

```text
managed_service.service_categories ... catalog_audit_events
    # System data, không có prefix sre_ và không owner/workspace/Zone.

managed_service.personal_managed_service_*
    # Customer aggregate thuộc personal workspace.

managed_service.tenant_managed_service_*
    # Customer aggregate thuộc tenant workspace.

managed_service.managed_service_outbox_records
    # Module transport evidence theo cùng shape Storage/Mail. Nó mang owner snapshot
    # cho routing/audit, nhưng không biến customer aggregate thành bảng polymorphic.
```

SRE là admin workflow/actor, không phải owner của catalog table. `published_by`
hoặc `retired_by` chỉ giữ audit provenance. Customer instance `code` là immutable
DNS label tối đa 35 ký tự và Kubernetes workload-name base; `name` chỉ là display
metadata. Mọi customer operation/result/fence nằm trong đúng physical personal hoặc
tenant branch, không dùng bảng polymorphic chung.

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
  - code DNS-label ≤35, version, UUID và pagination;
  - JSON Schema/UI Schema/manifest contract khi workflow publish blueprint;
  - ownership/routing context đã được Envoy/ACR inject.
* Service và repository tin tưởng entity đã được handler normalize.
* Service/repository không parse lại DTO và không lặp lại HTTP validation.
* Handler không sinh resource UUID, draft UUID hoặc audit-event UUID. Các field
  system-generated được để `uuid.Nil` khi map request sang entity; đúng service
  workflow sinh UUID còn thiếu trước khi gọi repository. Service giữ nguyên UUID
  đã có để một retry nội bộ tiếp tục dùng cùng identity.
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
  → sinh system UUID/AuditID còn thiếu và giữ nguyên identity đã có
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

### 8. Quy Chuẩn Khởi Tạo Tài Nguyên (Atomic Resource Provisioning Pattern)

Để đảm bảo tính nhất quán dữ liệu, chống Race-Condition, ngăn chặn rác Outbox Event và tối ưu hóa tài nguyên Database, tất cả các workflow khởi tạo Resource (Instance, Resource Object, Stateful Entity) tại Controlplane BẮT BUỘC phải tuân thủ mô hình 2 tầng sau:

#### A. Tầng API / Handler (Static Validation & Fail-Fast ở Memory)
- **Sanitization & Shape Check:** Parse và validate kiểu dữ liệu, giới hạn kích thước Payload (`http.MaxBytesReader`), kiểm tra cấu hình JSON Schema / Form Contract.
- **Canonicalization & Hashing:** Chuyển đổi JSON thô (`json.RawMessage`) thành cấu trúc Go rồi `json.Marshal` ngược lại để chuẩn hóa chuỗi JSON (Canonical JSON), dùng làm dấu vân tay SHA256 Hash duy nhất (Idempotent Fingerprint).
- **Fail-Fast:** Ngắt và trả về lỗi `400 Bad Request` hoặc `422 Unprocessable Entity` ngay lập tức tại RAM nếu dữ liệu sai định dạng. Tuyệt đối KHÔNG mở kết nối Database hay gọi SQL khi Request Body chưa hợp lệ.

#### B. Tầng Database Repository (Atomic CTE Transaction)
Thực thi duy nhất **01 câu lệnh SQL CTE (Common Table Expression)** trong một Transaction duy nhất. SQL CTE phải thực hiện khóa (Lock) theo thứ tự ưu tiên và kiểm tra nguyên tử 5 nhóm điều kiện:

1. **Workspace & Owner Binding:** Xác thực `workspace_id` gắn đúng với `owner_id` và `zone_id` tương ứng.
2. **Zone Admission Gate:** Kiểm tra `zone_services` của Zone target xem có `desired_state = true` với dịch vụ tương ứng (VD: `service_type = 'managed_service'`) hay không.
3. **Zone Capability & Selector Matching:** 
   - Kiểm tra `zone_selector` (`all` hoặc nằm trong `allow_list`).
   - Kiểm tra Zone có chứa đầy đủ các năng lực hạ tầng (`all_of`: `storage`, `database`...) mà Resource Blueprint yêu cầu.
4. **Catalog Integrity & Checksum Verification:** Kiểm tra toàn bộ chuỗi trạng thái (`active`/`available`/`published`) và đối chiếu mã hash SHA256 giữa DB và Receiver Receipt.
5. **Code Uniqueness & Intent Serialisation:** 
   - Kiểm tra tính duy nhất của mã `code` trong cùng Workspace.
   - Trùng `code` + Trùng Intent Payload $\rightarrow$ Trả về Operation/Instance hiện tại (Idempotent Success).
   - Trùng `code` + Khác Intent Payload $\rightarrow$ Trả về Conflict Error (`ErrCatalogCodeConflict`).

#### 🟢 Atomic Write Commit:
Chỉ khi TẤT CẢ các bước trên khớp 100%, CTE mới ghi nhận đồng thời trong 1 lần Commit duy nhất:
- `Resource Instance` (trạng thái `PROVISIONING` / `PENDING`)
- `Instance Revision` (lưu snapshot configuration + ciphertext)
- `Resource Operation` (trạng thái `ACCEPTED`)
- `Outbox Event` (để Job Orchestrator nhặt và gửi sang Kafka)

*Lưu ý:* Nếu bất kỳ điều kiện nào thất bại, PostgreSQL sẽ tự động Rollback toàn bộ Transaction. Không có bản ghi rác nào được tạo và KHÔNG có Outbox Event nào được bắn sang Kafka.

