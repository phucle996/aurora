# Hierarchy Module Contract

Module `hierarchy` sở hữu business topology của Controlplane: Zone, Tenant,
personal workspace, tenant workspace và public encryption key của Zone.

Tài liệu này khóa quy tắc tổ chức và viết code trong
`controlplane/internal/hierarchy`. Đây là contract bắt buộc cho code mới và cho
mọi workflow cũ khi được sửa. God View vẫn là Source of Truth cho topology và
workflow end-to-end; nếu README, God View và code mâu thuẫn thì phải báo rõ và
cập nhật chúng trong cùng change-set, không âm thầm chọn một phía.

## 1. Ownership và boundary

Hierarchy sở hữu durable business state sau:

- Zone catalogue và Zone lifecycle desired state.
- Tenant và tenant membership topology.
- Personal workspace và tenant workspace.
- Zone service desired state.
- Public X25519 encryption key và lifecycle metadata của key theo Zone.

Hierarchy không sở hữu:

- Authentication, authorization policy hay critical proof; ACR sở hữu các phần
  này và Envoy chỉ forward identity đã được xác minh.
- Private key của Zone. Private key không được đi vào Controlplane, PostgreSQL,
  Redis, Kafka payload, log hay notification.
- Runtime execution trong Zone; Dataplane thực thi command của đúng Zone.
- Kafka dispatch. Durable mutation đi từ PostgreSQL/outbox hoặc WAL/CDC qua Job
  Orchestrator rồi mới tới Kafka.
- Runtime health/metrics làm business state. Dữ liệu đó thuộc observability hoặc
  soft-state workflow đã được God View cho phép.

PostgreSQL schema `hierarchy` là authoritative SoT. Shared Redis chỉ được dùng
cho bounded request/reply, cache/invalidation và dữ liệu có thể tái tạo. Không
đưa business state sang Redis và không mở đường Controlplane tới Zone NATS KV.

## 2. Canonical naming

Tên canonical duy nhất của module là `hierarchy`:

- Go root package: `package hierarchy`.
- Mọi package con mang prefix module: `hierarchyEntity`,
  `hierarchyRepoInterface`, `hierarchySvcInterface`, `hierarchyRepoImpl`,
  `hierarchySvcImpl`, `hierarchyHandler`, `hierarchyReq`,
  `hierarchyTaxonomy`, `hierarchyMigrations` và
  `hierarchyPubsubHandler`.
- Admin API: `/admin/hierarchy/...`.
- Critical admin mutation: `/admin/critical/hierarchy/...`.
- Customer API đặt `hierarchy` trong resource path hiện hành.
- Operation: `hierarchy.<object>.<behavior>`.
- Workflow metrics: recorder bind `module=hierarchy` từ `internal/observability`.
- Metric contract: `aurora_controlplane_workflow_*`; không có meter/package riêng cho module.
- Protobuf descriptor package: `hierarchy.rpc`.

Không tạo lại package, alias, route, metric hay operation mang tên module cũ
`core`. `NATS Core` và `JMAP Core` là tên công nghệ hợp lệ, không liên quan đến
tên module này.

## 3. Cấu trúc thư mục

```text
internal/hierarchy/
├── README.md
├── bootstrap.go
├── migration.go
├── migrations/
├── module.go
├── route.go
├── domain/
│   ├── entity/
│   ├── repo/
│   └── service/
├── repository/
├── service/
├── taxonomy/
├── test/
│   ├── e2e/
│   ├── fixtures/
│   ├── integration/
│   ├── mocks/
│   └── unit/
└── transport/
    ├── http/
    │   ├── dto/req/
    │   └── handler/
    ├── proto/
    └── pubsub/handler/
```

Các package có trách nhiệm duy nhất:

- `domain/entity` (`hierarchyEntity`): business entity và enum; không chứa JSON
  transport contract.
- `domain/repo`: repository interface; không khai báo struct.
- `domain/service`: service interface; không khai báo struct.
- `transport/http/dto/req`: request JSON struct duy nhất.
- `transport/http/handler`: ingress validation, mapping và HTTP response.
- `service`: business decision, system-owned values và orchestration.
- `repository`: PostgreSQL query, transaction, lock và durable precondition.
- `taxonomy`: sentinel error ổn định giữa repository, service và handler.
- `internal/observability`: recorder tập trung cho internal outcome/latency;
  Hierarchy không sở hữu package metric riêng và metric không định nghĩa HTTP contract.

## 4. Tổ chức file theo object

File được chia theo domain object, không gom toàn module vào một file và cũng
không tách mỗi method thành một file siêu nhỏ.

Ví dụ:

```text
domain/entity/zone.go
domain/repo/zone_repo.go
domain/service/zone_service.go
repository/zone_repo.go
service/zone_service.go
transport/http/handler/zone_handler.go
```

`zone_encryption_key`, `tenant`, `personal_workspace` và `tenant_workspace` có
nhánh file tương ứng. Một file của object có thể chứa nhiều workflow của chính
object đó, nhưng không được chứa workflow của object khác.

Mỗi implementation object chỉ có một constructor. Constructor repository và
service phải trả về interface thuộc `domain/repo` hoặc `domain/service`.
Handler constructor có thể trả concrete handler để `route.go` đăng ký method.
Toàn bộ dependency graph được dựng một lần tại `module.go`.

## 5. One workflow - one function - one layer

Mỗi workflow phải có đúng một đường gọi chính:

```text
1 Handler method -> 1 Service method -> 1 Repository method
```

Ví dụ `ActivateZoneEncryptionKey` phải có đúng ba method cùng tên theo ba layer.
Repository method có thể dùng một CTE nhiều nhánh hoặc một transaction cục bộ,
nhưng service không được tự ghép nhiều repository method để thay thế một durable
atomic workflow.

Ưu tiên workflow isolation hơn DRY:

- Duplicate code giữa hai workflow được chấp nhận.
- Không tạo base handler, base service hay generic repository.
- Không tạo helper dùng chung để Luồng A phụ thuộc implementation của Luồng B.
- Không kéo mapping, validation, error handling hay state transition sang helper
  dùng chéo workflow.
- Chỉ dùng primitive ổn định của standard library hoặc package hạ tầng đã có;
  không biến business step thành utility dùng chung.

Một thay đổi ở workflow A phải không tạo side effect ngầm lên workflow B.

Interface và implementation chỉ giữ workflow đang có consumer hoặc route thật.
Không giữ method dự phòng cho RPC, warmup, get/update chưa được wire; khi contract
mới xuất hiện thì thêm lại trọn handler/service/repository/entity trong cùng
change-set.

## 6. Entity và data pipeline

Mỗi workflow sở hữu một business entity phẳng, mang chính tên workflow, trong
`domain/entity`:

- Chỉ dùng field scalar, UUID, byte slice, enum và timestamp cần thiết. List
  workflow được dùng bounded slice của primitive/UUID để mang permission filter,
  nhưng không được nhúng entity khác.
- Không nhúng request DTO hoặc database model.
- Không nhúng entity của workflow khác.
- Không tạo entity dùng chung kiểu `Zone`, `Tenant`, `WorkspaceCatalog` rồi
  truyền chúng qua nhiều workflow. Ví dụ hợp lệ là `CreateZone`, `ListZones`,
  `ResolveZoneByCode`, `CreateTenantWorkspace` và `DeleteTenantWorkspace`.
- Không khai báo entity trong interface, handler, service hay repository file.
- Nếu workflow cần outbox thì outbox là entity thứ hai duy nhất được phép đi vào
  repository cùng business entity.

Data pipeline bắt buộc:

```text
Request DTO
  -> Handler validate và map
  -> một workflow entity
  -> Service bổ sung system-owned values
  -> Repository persist/return taxonomy
  -> Handler dựng gin.H response
```

Handler không sinh resource UUID, fingerprint, initial status, revision hoặc
giá trị do hệ thống sở hữu. Service sinh và gắn các giá trị đó vào entity trước
khi gọi repository. Retry nội bộ có thể giữ UUID đã được service gán.

## 7. Validation chỉ tại upstream boundary

Handler là nơi duy nhất validate dữ liệu do transport nhận:

- Path/query parameter và UUID format.
- Request body size.
- JSON syntax, required field và unknown field.
- Canonical Base64/hex/enum/code format.
- Trusted identity/proof header do Envoy và ACR inject.
- Pagination limit/cursor.
- Mapping DTO sang entity.

Service và repository không parse hoặc validate lại cùng dữ liệu. Chúng tin
entity đã qua handler.

Repository vẫn bắt buộc enforce durable precondition. Đây không phải duplicate
input validation mà là concurrency boundary:

- Zone/Tenant/Workspace có còn tồn tại tại thời điểm ghi hay không.
- Current state có cho phép transition hay không.
- Ownership hoặc parent relation còn đúng trong transaction hay không.
- Unique code/fingerprint có xung đột hay không.
- Row version hoặc lock/fencing condition có còn hợp lệ hay không.

Các điều kiện trên phải nằm trong cùng CTE/transaction với mutation. Không làm
`SELECT` kiểm tra rồi `UPDATE` ở query khác vì replica khác có thể thay đổi state
ở giữa hai lệnh.

## 8. Dependency và lifecycle

Dependency bắt buộc được kiểm tra một lần ở `module.go` và app bootstrap:

- PostgreSQL pool, Shared Redis client, cache registry và OTel dependency cần
  cho workflow phải fail fast trước readiness.
- Constructor trả nil/error phải được chặn ngay khi dựng module graph.
- Service và repository không kiểm tra lại dependency nil trong từng method.
- Không tạo fallback cho identity, schema, database, Redis hoặc security
  dependency.
- Không tự đọc Vault/config kết nối trong service hoặc repository. App/infra
  dựng dependency và inject xuống module theo topology được God View cho phép.

Background subscriber phải nhận cancellation, bounded timeout và dừng trong
`Module.Stop`. Shutdown phải ngừng nhận việc mới trước, hủy subscriber, chờ
bounded inflight và không để goroutine/socket rò rỉ.

## 9. Repository, CTE và transaction

Repository là layer duy nhất biết pgx/PostgreSQL detail:

- Ưu tiên một CTE cho một workflow.
- Dùng unique/partial index để bảo vệ invariant ở cấp database.
- Dùng `FOR UPDATE`, `FOR KEY SHARE`, advisory lock hoặc optimistic version khi
  invariant có race giữa nhiều Controlplane replica.
- Mutation business và outbox phải commit trong cùng PostgreSQL transaction.
- Không publish event trước commit và không dùng broker ACK thay DB commit.
- Query phải bounded; list dùng cursor pagination, không load vô hạn.
- Không giữ transaction trong lúc gọi Redis, Kafka hoặc external network.

Repository phải chuyển expected database outcome thành taxonomy:

- `RowsAffected == 0`, missing row, SQLSTATE unique/FK/check violation hoặc
  state guard fail phải map thành sentinel error có nghĩa nghiệp vụ.
- Không trả `pgx.ErrNoRows` hay `*pgconn.PgError` như public workflow contract.
- Lỗi hạ tầng không dự đoán được có thể wrap/return để handler log và trả 500.

Taxonomy chỉ mô tả behavior dùng chung trong module, không mang tên object:

- `ErrNotFound`.
- `ErrAlreadyExists`.
- `ErrConflict`.
- `ErrInvalidTransition`.
- `ErrPreconditionFailed`.

Object và workflow đã được xác định bởi operation/log/trace nên không tạo
`ErrZoneNotFound`, `ErrWorkspaceNotFound` hoặc sentinel tương tự. Không gom hai
failure semantic khác nhau chỉ vì chúng cùng map HTTP 409.

Sự tồn tại và điều kiện trạng thái là hai behavior khác nhau: parent không tồn
tại trả `ErrNotFound`; parent tồn tại nhưng inactive hoặc không thỏa guard trả
`ErrPreconditionFailed`. Repository phải trả hai outcome này từ cùng CTE để
không tạo race giữa bước kiểm tra và mutation.

Idempotency phải dựa trên natural invariant khi có thể: UUID đã gán, unique code
trong owner scope, unique fingerprint hoặc state no-op. Không tuyên bố
exactly-once. Timeout sau commit là outcome chưa xác định; retry phải tạo kết quả
idempotent hoặc conflict ổn định.

Ordering chỉ được đảm bảo theo aggregate/Zone đang lock hoặc theo monotonic
version của aggregate. Không giả định global ordering.

## 10. Error taxonomy và HTTP response

Error flow bắt buộc:

```text
Repository -> taxonomy sentinel/raw infra error
Service    -> xử lý nhánh business nếu cần, nếu không trả nguyên error
Handler    -> errors.Is, log lỗi không dự kiến, map HTTP response
```

Service và repository không ghi request log. Handler hoặc middleware là nơi ghi
log với operation đã gắn vào context. Không log public/private key material,
proof, identity secret hoặc raw payload.

HTTP response phải dùng `pkg/apires` và payload `gin.H`:

```go
apires.RespondSuccess(c, gin.H{
    "id":     result.ID,
    "status": result.Status,
}, "zone updated")
```

Quy tắc bắt buộc:

- DTO chỉ dùng cho request JSON; không tạo response DTO.
- Không trả internal outcome code trong JSON.
- Không dùng `Respond*WithCode`.
- JSON key được viết tường minh trong `gin.H` để handler sở hữu transport
  contract.
- Taxonomy/metric outcome chỉ dùng cho branch, log, metric và HTTP mapping.
- Lỗi không dự kiến trả thông điệp chung `internal_error`, không lộ SQL,
  topology, stack trace hoặc cryptographic detail.

## 11. Route, identity và security boundary

Route shape hiện hành:

- `/admin/hierarchy/...`: SRE read/admin surface.
- `/admin/critical/hierarchy/...`: mutation ảnh hưởng vận hành hoặc key
  lifecycle; ACR phải consume critical proof trước khi forward.
- `/api/v1/me/hierarchy/...`: personal scope từ verified user context.
- `/api/v1/tenant/hierarchy/...`: tenant scope từ verified tenant/workspace
  context và authorization middleware.

Không nhận `owner_id`, `owner_type`, `tenant_id`, `workspace_id`, `zone_id`, role
hay permission từ JSON khi giá trị đó đã thuộc verified request context. Envoy
phải strip header nội bộ từ request ngoài rồi inject lại; handler chỉ đọc header
theo contract edge đã xác minh.

Route `/critical/` là structural security contract, không phải naming cosmetic.
Tạo/xóa Zone, đổi operational state, đổi Zone service và mutation encryption key
phải nằm dưới `/critical/`. Không tạo route song song để bypass proof.

Public encryption key là public capability metadata nhưng inventory và lifecycle
vẫn là admin-plane. Private counterpart tuyệt đối không được thêm vào entity,
DTO, schema, response, event hoặc cache của Hierarchy.

`ACTIVE` chỉ được dùng để seal khi `loaded_at` có fresh Zone report. Report là giao
của keyring trên mọi Dataplane replica fresh, không phải báo cáo riêng của leader;
JO fence report bằng timestamp cùng Zone-KV leader token.
Hierarchy resolve key qua một workflow nội bộ duy nhất: service đọc Cache Engine
L1, cache miss mới gọi repository lấy PostgreSQL. Public-key bytes không được đưa
vào L2 Redis hoặc fanout payload. Cache value mang hard readiness deadline tính từ
remaining duration do PostgreSQL trả về; deadline này luôn được kiểm tra tại cache
boundary vì TTL jitter chỉ có trách nhiệm dọn RAM, không được gia hạn quyền seal.
Miss, stale readiness và Zone chưa có key không được negative-cache. Package
`internal/security` chỉ nhận typed resolver, không giữ pool, schema hoặc SQL của
Hierarchy. Sau activation commit, service publish invalidate-only qua fanout với
payload rỗng để các replica xóa L1; lỗi/mất Pub/Sub không rollback business
transaction và hard deadline vẫn là fallback.
Retire `DECRYPT_ONLY` có drain window và CTE kiểm tra mọi retained ciphertext; còn
reference thì trả conflict. Outbox INSERT ở từng module khóa Zone/key và từ chối key
đã retired, đóng race giữa seal, rotation và durable commit.

## 12. Transport và event contract

Controlplane không publish durable Zone command trực tiếp lên Kafka. Luồng chuẩn:

```text
Hierarchy PostgreSQL commit
  -> WAL/CDC hoặc transactional outbox
  -> Job Orchestrator
  -> Kafka durable transport
  -> Dataplane đúng Zone
```

Shared Redis request/reply phục vụ ACR catalog là bounded Central-local path,
không phải durable event bus. NATS Core không được thêm làm đường tắt giữa
Controlplane và Zone.

Protobuf source dùng chung phải đồng bộ trong cùng change-set giữa producer và
consumer. Không đổi field number, tái sử dụng field đã bỏ hoặc sửa một bản copy
rồi để service khác drift. Breaking schema cần version mới và compatibility
test.

Durable envelope phải có stable event/operation ID, schema version, aggregate
ID, Zone binding, monotonic version, timestamp và trace context theo God View.
Consumer là at-least-once: retry bounded với backoff/jitter, DLQ/quarantine và
idempotent side-effect boundary.

## 13. Observability

Mọi handler gắn operation canonical vào context. Service ghi metric outcome và
downstream latency với cardinality bounded:

- Không đưa UUID, code động, public key, fingerprint hay error text vào metric
  label.
- `destination` là tên method/downstream hữu hạn.
- Outcome là taxonomy nội bộ như `success`, `failure`, `conflict`.
- Trace context phải được giữ qua repository và event envelope.
- Log chỉ ở handler/middleware hoặc lifecycle boundary; không log lặp ở nhiều
  layer cho cùng error.

Observability không quyết định business completion và không thay PostgreSQL.

## 14. Migration contract

SQL trong `migrations/` là schema SoT của Hierarchy. Trong giai đoạn clean
baseline trước production:

- `000001`: enum/type.
- `000002`: table.
- `000003`: index/uniqueness.
- `000004`: cross-table constraint.
- `000005` và `000006`: role/membership constraint hiện hành.

Feature mới phải cập nhật đúng baseline file thay vì tự động nối thêm migration
chỉ để né chỉnh baseline. Việc chuyển sang append-only production migrations
phải là quyết định riêng, cập nhật runner và God View trước.

Mọi `.up.sql` phải idempotent vì embedded runner có thể chạy lại khi bootstrap.
DDL liên quan nhiều module chạy dưới transaction/advisory lock của app bootstrap.
Schema identifier chỉ được interpolate sau khi đã validate tại bootstrap; query
business dùng parameter binding cho value.

## 15. Test contract

Toàn bộ test mới của Hierarchy đặt dưới `internal/hierarchy/test`:

- `unit`: handler/service/taxonomy behavior không cần hạ tầng thật.
- `integration`: SQL migration, CTE, constraint, lock và repository taxonomy.
- `e2e`: workflow HTTP-to-durable-state theo God View.
- `mocks`: implementation của domain interface chỉ dành cho test.
- `fixtures`: dữ liệu dựng test, không chứa business implementation.

Test phải cover tối thiểu:

- Happy path và mọi taxonomy branch.
- Unknown JSON field, oversized body, invalid UUID/cursor/canonical encoding.
- Spoofed/missing identity và missing critical proof.
- Concurrent mutation vào cùng aggregate.
- Retry sau outcome chưa xác định và idempotent no-op.
- Unique/FK/check constraint mapping.
- Context timeout/cancellation.
- Response JSON key chính xác và không có internal code.
- Protobuf compatibility khi contract thay đổi.

Lệnh kiểm chứng tối thiểu:

```bash
go test ./internal/hierarchy/...
go test -race ./internal/hierarchy/test/unit ./internal/hierarchy/test/integration
go vet ./internal/hierarchy/...
```

Integration test dùng PostgreSQL/Redis thật phải tách rõ khỏi unit test và có
bounded timeout. Không coi mock test là bằng chứng cho transaction, lock hoặc
database constraint.

## 16. Comment contract

Chỉ thêm comment tại:

- Invariant hoặc state transition khó nhìn ra từ cú pháp.
- Race condition, lock/fencing và idempotency boundary.
- Security/trust boundary.
- Failure semantic hoặc quyết định kiến trúc không hiển nhiên.

Không thêm comment chỉ kể lại từng dòng code hoặc đánh số các bước hiển nhiên.
Comment phải giải thích **vì sao** invariant tồn tại và điều gì sẽ hỏng nếu bỏ
nó.

## 17. Compliance gate

Checklist trước khi merge một Hierarchy workflow:

- [ ] God View vẫn khớp topology và contract.
- [ ] Tên canonical là `hierarchy`, không tái tạo `core`.
- [ ] File chia theo object; workflow có một handler/service/repository method.
- [ ] Request DTO ở transport, business struct ở entity, response dùng `gin.H`.
- [ ] Handler đã validate toàn bộ ingress; layer dưới không validate lại.
- [ ] Service sinh system-owned value và không check dependency nil.
- [ ] Repository enforce durable precondition nguyên tử bằng CTE/transaction.
- [ ] Expected DB outcome được map sang taxonomy, không lộ pgx contract.
- [ ] Critical mutation nằm dưới `/critical/` và không có bypass route.
- [ ] Không có plaintext secret/private key trong state, event, log hoặc response.
- [ ] Retry, race, ordering và failure semantics có test.
- [ ] Metric/log/trace có cardinality và security boundary đúng.
- [ ] Test, race test, vet và `git diff --check` pass.

Code hiện hữu không phù hợp README này là implementation debt, không phải tiền
lệ để copy. Khi chạm vào workflow đó, change-set phải đưa phần được sửa tiến gần
contract này và không tạo thêm debt mới.
