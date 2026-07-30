# Managed Service Platform — Ý tưởng và contract nền tảng

> Đây là proposal contract của module, chưa phải workflow đã ship. Khi một
> workflow cụ thể được triển khai, God View tương ứng sẽ là source of truth và
> phải được cập nhật cùng contract/code.
>
> Lifecycle Source of Truth sau P00 là
> [Managed Service Lifecycle God View](../../../god_view/managedservice/managed_service_lifecycle_god_view.md).

## 1. Tóm tắt

Managed Service Platform biến Kubernetes cluster của mỗi Zone thành nơi triển khai dịch vụ cloud cho customer.

SRE định nghĩa catalog, version và blueprint. Controlplane giữ catalog cùng desired state. Dataplane render blueprint rồi thực thi xuống Kubernetes đúng Zone.

```text
resource → application → version → template revision → customer instance
```

Ví dụ: `database → psql → 16 → blueprint revision 7 → instance của customer A`.

Đây là self-service provisioning có kiểm soát, không phải endpoint nhận arbitrary YAML Kubernetes từ customer.

## 2. Từ điển business chuẩn

| Khái niệm UI | Tên domain | Ý nghĩa |
| --- | --- | --- |
| Resource | `ServiceCategory` | Nhóm năng lực, ví dụ database hoặc cache |
| Application | `ServiceDefinition` | Sản phẩm cụ thể, ví dụ PostgreSQL |
| Version | `ServiceVersion` | Phiên bản sản phẩm, ví dụ PostgreSQL 16 |
| Template | `ServiceBlueprint` | Blueprint render cho một version |
| Revision | `BlueprintRevision` | Bản immutable đã publish |
| Instance | `ManagedServiceInstance` | Dịch vụ customer yêu cầu triển khai |
| Instance revision | `InstanceRevision` | Snapshot immutable của desired config customer |

`ServiceCategory` chỉ phân loại; `ServiceDefinition` mô tả identity/capability;
`ServiceVersion` pin image/bundle và compatibility; `ServiceBlueprint` chứa template
và input/output contract; `BlueprintRevision` là đơn vị publish/pin/reconcile;
`ManagedServiceInstance` giữ ownership cùng desired state của customer.
`InstanceRevision` giữ config customer immutable; instance chỉ chuyển active head
sau khi Dataplane áp dụng revision thành công.

## 3. Platform hướng tới điều gì

* SRE thêm service mới bằng catalog và blueprint revision, không fork backend cho từng công nghệ.
* Console dựng form động từ metadata, không hard-code form theo resource.
* Customer chọn service, version và tham số trong Zone context đang active.
* Instance pin `template_id + revision + bundle_hash`, không đọc latest.
* Business ownership tách khỏi runtime Kubernetes.
* Controlplane, Job Orchestrator và Dataplane dùng một command/result contract.
* Process có thể scale-out, retry và recover sau crash mà không cần shared memory.

Controlplane sở hữu catalog, published revision, desired state, ownership, Zone binding và operation/audit metadata.

Controlplane không sở hữu Kubernetes client, live pod/resource, render engine, Zone credential hoặc runtime metric động.

## 4. Quyết định S00 đã chốt

Hệ thống có một SRE principal duy nhất để publish catalog và blueprint revision.
Customer authorization do middleware Controlplane kiểm soát theo static permission
năm bậc: personal scope là resource của user, tenant scope là workspace được cấp.

`Resource` là managed service lifecycle. Customer chỉ điều khiển các tham số
exposure, port, firewall và network policy trong allow-list; không gửi raw YAML.
Một instance là graph nhiều component, thường tách thành workload, service và
network-policy manifest. Operator như Strimzi là platform dependency của Zone.

Zone không đến từ create request; backend lấy trusted context do Envoy forward và
kiểm tra revision có hỗ trợ Zone. Update tạo `InstanceRevision` immutable mới.
Mutation luôn theo `durable intent → Kubernetes side effect → durable finalization`.
Delete chỉ hard-delete business record sau khi Dataplane xác nhận graph đã xóa;
failure giữ trạng thái retryable và deletion fence chống stale replay.

## 5. Vị trí trong hệ thống hiện tại

Public path:

```text
Console/SDK → Envoy → Controlplane API → Controlplane PostgreSQL
```

Envoy/ACR là boundary xác minh identity. Backend không tin `owner_id`, `workspace_id`, `zone_id`, role, permission hoặc internal header do client gửi.

Handler nhận, parse và validate request; service nhận entity đã normalize; repository thực hiện mutation atomic. Desired state và outbox commit cùng PostgreSQL transaction.

Controlplane không publish Kafka trực tiếp. JO bridge là bắt buộc, không có
fallback bypass JO:

```text
Controlplane PostgreSQL
  → logical WAL/CDC
  → Job Orchestrator
  → Kafka durable Central ↔ Zone
  → Dataplane đúng Zone
  → Kubernetes API của Zone
```

Kết quả đi ngược lại và fan out sang notification timeline:

```text
Dataplane → Kafka result/report → Job Orchestrator
  ├→ Controlplane projection → authoritative Console API
  └→ stream:{job_notifications} → Notification Service Scylla upsert
       → Centrifugo notifications:<user_id> → Console realtime upsert
```

Mỗi operation/resource action có một `timeline_id` ổn định. JO phát
`PROCESSING` sau command Kafka durable, sau đó update cùng timeline record sang
`SUCCESS` hoặc `FAILED` sau khi result đã settle; status/attempt không tạo record
mới. Notification path không rollback business operation nếu XADD lỗi/đầy.

Kafka là durable transport cho command/result/report. Managed Service V1 không định
nghĩa NATS Core subject, runtime envelope hoặc JO runtime consumer; NATS không là
fallback cho command/result. NATS JetStream KV vẫn là database nội bộ của đúng Zone,
không phải catalog trung tâm và không cấp credential cho Controlplane hoặc JO.

## 6. Catalog và blueprint do SRE định nghĩa

Catalog là contract discovery của customer và source metadata của SRE. Nó không mang
runtime status, quota động, Kubernetes object, raw template/bundle, secret hay Zone
routing mà client có thể điều khiển.

```text
ServiceCategory → ServiceDefinition → ServiceVersion → ServiceBlueprint → BlueprintRevision
```

`ServiceCategory`, `ServiceDefinition` và `ServiceVersion` là hierarchy customer
nhìn thấy. `ServiceBlueprint` là identity delivery nội bộ của SRE; V1 có đúng một
blueprint cho một version. Vì vậy customer chỉ chọn category, application và
version, đúng với UX self-service; không chọn template hoặc revision bằng UI.

Version trả `default_published_revision_id` trong catalog detail. Console dựng form
từ revision đó và gửi chính revision ID khi create. Controlplane chỉ nhận create
nếu revision vẫn là default và provisionable tại thời điểm submit. Nếu SRE publish
default mới khi form đang mở, request cũ phải trả lỗi refresh catalog thay vì âm
thầm áp schema/template khác. Instance update dùng revision đang pin, không bị
default mới tự động upgrade.

### 6.1. Identity, metadata và lifecycle

Mỗi entity có UUID nội bộ. Với catalog identity (`ServiceCategory`,
`ServiceDefinition`, `ServiceVersion`, `ServiceBlueprint`), `code` là stable,
lowercase slug, immutable và không tái sử dụng vì catalog không hard-delete.
Display metadata có thể sửa độc lập với revision; metadata dùng bounded text, i18n
có `en` fallback và `icon_key` thuộc design system; không nhận HTML, URL artifact
tùy ý hoặc presentation code.

Catalog là system data, không phải data "của SRE". Durable table dùng tên
system-neutral trong schema `managed_service`: `service_categories`,
`service_definitions`, `service_versions`, `service_blueprints`,
`blueprint_revisions` và `catalog_audit_events`. Chúng không có `owner_id`,
`owner_type`, `workspace_id` hoặc prefix `sre_`. `published_by`/`retired_by` chỉ
là trusted admin actor phục vụ audit, không phải ownership hay routing identity.

`ManagedServiceInstance` có identity customer-facing riêng:

* `id` là UUID nội bộ cho transport, fencing và Kubernetes ownership marker;
* `code` là lowercase DNS label bắt buộc, immutable, tối đa 35 ký tự và unique
  trên toàn bộ Managed Service module trong đúng một `workspace_id` — không phân
  biệt category hay application; đây là identity trong customer API/URL,
  duplicate-create key và Kubernetes workload-name base;
* `name` là display metadata tự do, không unique, có thể đổi độc lập với desired
  revision và không dùng làm Kubernetes `metadata.name`.

`name` chỉ chịu giới hạn kỹ thuật về độ dài, empty/control character để bảo vệ API
và storage; không có semantic uniqueness. Console có thể gợi ý `code` từ `name`
nhưng customer phải submit `code` rõ ràng. Không lưu `kubernetes_name` riêng; DP
derive nó deterministically từ immutable `code` và static component ID. Instance bị
hard-delete sau khi Dataplane xác nhận delete có thể tái sử dụng `code`; không giữ
tombstone/ledger vĩnh viễn chỉ để cấm reuse. Code cũ có thể trở thành workload name
của instance mới, nhưng command cũ không thể tác động nó vì execution fence vẫn là
`instance_id + operation_id + generation`.

Category/definition có `ACTIVE|RETIRED`. Service version có
`AVAILABLE|DEPRECATED|RETIRED`:

* `AVAILABLE` cho create mới với default revision;
* `DEPRECATED` không nhận create mới, nhưng instance đang pin revision published
  vẫn có thể đổi config trên chính revision đó;
* `RETIRED` chỉ còn cho operation đã tồn tại, retry, reconcile và delete.

Blueprint revision có `DRAFT|PUBLISHED|RETIRED`. Draft chỉ SRE thấy và có thể sửa.
Published revision immutable tuyệt đối. Retired revision không nhận create/update
mới, nhưng vẫn phải render/reconcile/delete an toàn cho instance đã pin nó. Không
hard-delete catalog identity hay revision từng được instance/operation tham chiếu.

Publish revision mới chỉ atomically đổi default pointer của version. Nó không đổi
desired state hoặc revision/hash của instance cũ.

### 6.2. Contract của BlueprintRevision

Một revision published pin toàn bộ contract cần tái lập:

* `blueprint_id`, revision number và `service_version_id`;
* canonical multi-document YAML bundle bounded trong PostgreSQL cùng immutable
  `bundle_hash`;
* `contract_version` và `contract_hash` của catalog contract canonical;
* `input_schema`, `ui_schema`, `output_contract`;
* immutable Zone selector, capability requirement và minimum Dataplane contract;
* lifecycle, `published_by`, timestamp và audit metadata.

`input_schema` mô tả flat customer value map: key, type, required, format,
enum/range, cardinality và mutability. `ui_schema` chỉ chứa label/i18n, widget,
group, thứ tự và help text; nó không cấp quyền hoặc mở field bị policy cấm.
`output_contract` chỉ mô tả safe observed metadata sau provision. V1 chỉ cho
allow-list typed output như host, port, database và TLS server name; không có
arbitrary JSONPath, Secret read/SecretKeyRef, raw input, password/token hoặc URI có
credential. Nó không trả lại raw input hay Kubernetes Secret value. Grammar chi tiết
của các contract này thuộc S05.

Form contract là `Platform Form Contract` có language/version giới hạn, không phải
arbitrary JSON Schema hoặc OpenAPI. Console không hiểu version thì không hiển thị
action create. Thay đổi schema, UI, template, image, render/network policy hoặc
Zone selector tạo revision mới; không sửa in-place. Controlplane dùng schema để
validate request nhưng không scan template, không lập variable binding và không
đối chiếu key trong schema với YAML tag; sự tương ứng đó là trách nhiệm của SRE.
Không có raw customer value trong event, log hoặc business database.

Zone selector là policy tĩnh, immutable của revision. Controlplane lấy Zone thực tế
từ trusted Envoy context và đối chiếu selector/capability trước create. Health,
capacity, outage hoặc maintenance runtime không mutate catalog và không được dùng
như catalog source of truth.

### 6.3. Publish và catalog projection

SRE publish theo một transaction logical:

```text
draft → validate hierarchy/form/UI reference/Zone policy
      → canonicalize + hash contract/bundle → record audit
      → publish immutable revision + switch default pointer
```

Một SRE principal nên security/compatibility review là audit gate được ghi lại, không
phải workflow approval nhiều principal. Public catalog chỉ trả hierarchy và revision
provisionable trong trusted workspace/Zone scope, cùng form contract cần cho Console;
không trả bundle reference, raw template, internal capability detail hoặc secret.

Catalog contract phải đủ để Console mock catalog, form và review screen mà không
hard-code theo service. Backend vẫn validate authoritative contract tại request
boundary; client-side validation chỉ phục vụ UX.

### 6.4. Input schema, parameter document và output contract

`Platform Form Contract v1` biểu diễn customer input bằng một flat typed map. Nó
không cho nested object/map, `null`, arbitrary JSON/YAML, Kubernetes quantity, label
hoặc annotation map và template fragment. Một field có stable `key`, `value_type`,
`cardinality`, constraint và `mutability`; không có source policy hoặc phân loại
secret ở grammar này.

V1 hỗ trợ các value type `STRING`, `BOOLEAN`, `INT64`, `DECIMAL`, `ENUM`,
`DNS_LABEL`, `CIDR`, `PORT`, `DURATION` và `BYTE_SIZE`. Cardinality là `ONE`,
`LIST` hoặc `SET`; list giữ nguyên thứ tự, còn set được sort và deduplicate khi
canonicalize. Collection chỉ chứa scalar, không tạo nesting level thứ hai.

`input_schema` và YAML là hai artifact độc lập do cùng SRE revision publish.
Schema quyết định Console hiển thị field nào và handler nhận/validate key nào; YAML
quyết định key đó có được dùng ở đâu hay không. Controlplane không enumerate YAML
tag, không tạo binding table và không reject chỉ vì một schema key không xuất hiện
trong template. Literal/default hay hành vi operator tự generate là content của YAML
do SRE viết, không phải một source branch của platform.

Request boundary reject unknown key, sai type, enum/range/format, overflow và payload
quá lớn trước khi tạo workflow entity. Nó canonicalize CIDR, duration, byte size và
decimal, sort key, giữ list order, sort/dedupe set rồi tính:

```text
input_hash        = SHA-256(canonical customer parameter map)
desired_spec_hash = SHA-256(bundle_hash + contract_hash
                           + canonical parameters + stable platform context)
```

Stable platform context chỉ chứa identity/routing/naming đã được platform xác minh,
như instance, immutable instance `code`, instance revision, generation, workspace
và Zone. Timestamp, retry attempt, random value, request header và raw auth context
không được tham gia render input hoặc hash. Retry cùng revision/input/context phải
sinh cùng desired spec.

Sau canonicalization, handler tạo `parameter_envelope` opaque bound với trusted target
Zone, instance, operation, generation, blueprint revision và bundle hash. Instance
revision/outbox chỉ giữ envelope và digest; raw map bị bỏ khỏi Controlplane sau request
boundary. Đây là cơ chế chung cho mọi field, không phụ thuộc một field có được SRE dùng
làm credential hay không.
`managed_service_outbox_records.payload` là whole command Protobuf; envelope là một
field ciphertext nested trong payload, không phải một runtime record độc lập.

`zone_id` chỉ là trusted routing/envelope-binding context; không có public-key record,
keyset, attestation hay key rotation trong Controlplane. Khi Zone-local encryption
runtime được chốt, private material vẫn chỉ ở Kubernetes Secret của đúng Zone; P01
không triển khai encryption/key-management flow đó.

Dataplane không persist map đã decrypt. Nó mở envelope trong RAM để render rồi gửi
runtime materialization tới Kubernetes: non-secret đi vào CRD/spec/ConfigMap theo
YAML SRE, sensitive value đi vào `v1/Secret` hoặc Secret do operator tạo. Vì CP không
query Kubernetes và reconciliation phải reproduce đúng immutable desired revision,
customer-supplied value vẫn tồn tại dưới dạng ciphertext trong `InstanceRevision`;
Kubernetes mới là nơi giữ runtime value. Literal/operator-generated value không đi
qua envelope.

Output contract chỉ khai báo safe `key`, type và `CUSTOMER|INTERNAL` visibility.
Actual endpoint/port/identifier là observed instance data sau provision, không thuộc
revision artifact và không tham gia render hash; nó không là API đọc lại customer
parameter hoặc Kubernetes Secret value.

Giới hạn V1: tối đa 64 field mỗi revision, 64 KiB canonical parameter document,
4 KiB cho một string, 64 item cho list/set và 128 enum value. Publish gate có golden
canonical/hash test cùng negative cases cho unknown/null/type/overflow/bad CIDR,
duplicate set, create-only mutation và payload size.

### 6.5. YAML render và Kubernetes ownership policy

Blueprint artifact là immutable multi-document YAML do SRE tạo. `apiVersion` và
`kind` là static artifact content, không phải parameter customer. S06 không có Kind
allow-list: mọi namespaced Kubernetes Kind hoặc CRD đang tồn tại trong Zone đều có
thể là component của Managed Service instance. YAML syntax hợp lệ là điều kiện cần;
document vẫn phải là Kubernetes object có `apiVersion`, `kind` và `metadata` để DP
có thể inject ownership rồi Kubernetes API/dry-run xác nhận schema thực tế.

Sự tự do Kind không mở rộng privilege của catalog: Zone executor ServiceAccount/RBAC
và capability profile pinned trong revision là runtime enforcement. Customer instance
vẫn chỉ namespaced object/CRD; cluster-scoped operator/CRD/platform artifact là
lifecycle SRE bootstrap riêng, không phải output customer create.

Renderer là YAML AST renderer, không phải text template engine. V1 chỉ nhận hai
custom YAML tag:

```yaml
metadata:
  name: !aurora/component primary
spec:
  replicas: !aurora/param replicas
```

`!aurora/param <key>` là exact lookup vào parameter map đã được handler validate và
seal; DP thay tag bằng typed YAML node tại lúc render. Controlplane không scan tag
hoặc đối chiếu `<key>` với `input_schema`. Tag được dùng ở value node ngoài
`apiVersion`, `kind` và toàn bộ `metadata`; namespace, protected annotation/label và
platform naming không bao giờ là customer parameter. Nó không có string interpolation,
lookup động, loop, include hoặc function tùy ý. Thiếu key, type không render được hoặc
YAML sau render không hợp lệ là `SRE_TEMPLATE_INPUT_MISMATCH`: terminal,
non-retryable cho operation đó và không có empty/default fallback. Mọi document dùng
`!aurora/component <id>` tại `metadata.name`; `<id>` là DNS-label static/unique, tối
đa 27 ký tự. `primary` là component ID reserved, phải xuất hiện đúng một lần trong
published blueprint và DP resolve nó thành đúng `instance.code`. Component khác resolve thành
`<instance.code>-<component-id>`. Vì code tối đa 35 và component ID tối đa 27, mọi
tên không quá 63 ký tự. Tag này là platform context, không phải customer parameter;
component khác có thể dùng cùng tag trong `spec` để tham chiếu tên.
`metadata.name` không dùng customer parameter hay `generateName`.

Kubernetes controller mới quyết định literal Pod name: StatefulSet primary thường
tạo pod `<code>-0`, còn Deployment thêm ReplicaSet hash/suffix. Contract ở đây là
workload/object name base bằng `code`, không cố override naming invariant của
Deployment/StatefulSet.

Không có product-level render timeout hoặc HTTP deadline: render nằm trong Dataplane
operation/outbox lifecycle và có retry riêng. Worker vẫn phải tôn trọng cancellation
khi graceful shutdown, cùng byte/document budget để một artifact lỗi không làm cạn
RAM hoặc starve worker khác. Render không gọi network, đọc file ngoài pinned bundle
hoặc thực thi code tùy ý. Readiness deadline không phải render timeout: nó là static
component contract do SRE publish và bị Zone policy cap để Pending/PVC/capacity có
retry/terminal semantics hữu hạn.

Physical Kubernetes namespace tách khỏi logical ownership namespace
`tenant_code/workspace_code` hoặc `username/workspace_code` dùng trong hierarchy/UI.
Nó được derive một-một, không hash/truncate:

```text
aur-ms-{t|p}-{base32lower_no_padding(owner_uuid_bytes || workspace_uuid_bytes)}
```

`t` là tenant owner, `p` là personal user owner. Hai UUID raw là 32 byte, Base32 là
52 ký tự; prefix dài chín ký tự nên namespace dài 61, hợp lệ DNS label và không
collision. Namespace nằm trong đúng Zone cluster nên không cần thêm Zone ID. Template
phải bỏ `metadata.namespace`; DP là nơi duy nhất inject/overwrite namespace này.

DP inject và sở hữu các annotation sau trên mọi object namespaced, đồng thời trên
namespace khi tạo lần đầu:

```text
platform.aurora.io/workspace-id
platform.aurora.io/owner-id
platform.aurora.io/owner-type
platform.aurora.io/managed-service-instance-id
platform.aurora.io/managed-service-instance-revision
platform.aurora.io/render-hash
```

Template không được định nghĩa `platform.aurora.io/*`; draft validation reject và
renderer overwrite phòng thủ. Trước apply/reconcile, object cùng tên phải có ownership
marker khớp instance; không bao giờ adopt object lạ. `render-hash`/revision có thể
đổi theo desired revision, còn workspace/owner/instance ID không đổi.

Mỗi published revision pin component contract tĩnh: component ID, document set,
apply-order, delete-order và readiness rule/deadline. Graph chuẩn V1 apply
`network-policy → service → workload`, còn delete `workload → wait finalizer/gone →
service → network-policy`; graph khác phải khai báo explicit. DP dùng server-side
apply với field manager cố định và chỉ `force=true` khi ownership marker cùng instance.
Foreign marker là `K8S_OWNERSHIP_CONFLICT`; partial apply không blind rollback mà
reconcile theo cùng desired hash/fence hoặc trả taxonomy.

V1 chỉ inject protected label cho `instance-id` và `component-id`, phục vụ list,
cleanup và idempotent reconciliation. Owner/workspace là annotation như contract đã
chốt; customer không truyền hoặc override bất kỳ protected label nào. SRE vẫn có thể
ghi static label ngoài protected prefix. Namespace/pod label dùng cho cross-workspace
traffic policy chưa tồn tại ở V1 và phải được chốt bằng network policy contract riêng,
không suy luận từ label do customer gửi.

Zone OTel Collector có thể đọc Kubernetes metadata rồi copy **chỉ** protected
annotation này thành telemetry attributes `aurora_workspace_id`, `aurora_owner_id`,
`aurora_managed_service_instance_id` và `aurora_component_id`. Đây không phải
Kubernetes traffic label: chúng chỉ tồn tại trên record telemetry để query scope.
Collector phải overwrite attribute cùng tên do workload tự gửi và chỉ tin metadata
đọc từ Kubernetes API; workload không được tự khai owner/workspace/instance trong
OTLP payload.

Cluster-scoped object không thể mang namespace workspace, nên không thuộc lifecycle
customer `ManagedServiceInstance`. Operator, CRD và global cluster artifact do SRE
bootstrap/operate theo lifecycle platform riêng; customer create không kích hoạt path
đó. Đây là scope boundary, không phải Kind allow-list.

### 6.6. Cloud Console contract

Console có ba surface customer-facing, đều nằm dưới feature `managed-services`:

```text
/managed-services       list instance + quick detail pane
/managed-services/new   catalog → configure → review/confirm
/managed-services/:code full instance detail
```

List là table-first: instance code/name, service/version, desired state, observed state, current/latest operation và updated time. Desktop có quick detail pane 67/33; mobile mở full detail route. Blueprint, revision artifact, YAML, Kubernetes object, namespace và platform metadata không là customer resource UI.

Create bắt đầu ở catalog table, không phải card gallery. Customer chọn category/application/version; Console lấy default published revision cùng form contract từ backend. Revision chỉ read-only trong review và được gửi lại khi submit, không là selector customer. Workspace/Zone chỉ là context; không có input `owner_id`, `workspace_id`, `zone_id`, permission, namespace, label hay raw manifest. Revision stale/retired buộc invalidate catalog/form và refresh/reselect, không map âm thầm parameter sang revision mới.

Configure dùng one feature-owned renderer với finite widget registry tương thích `Platform Form Contract v1`: text, numeric/unit-aware, select/radio, switch và bounded list/set editor. `ui_schema` là data presentation, không executable UI; unknown contract/widget fail closed và không mở submit. Client validation chỉ phục vụ UX, backend vẫn authoritative. Raw parameter chỉ sống trong form memory cho đến submit, không vào DOM persistence, localStorage hay sessionStorage.

Form draft chỉ tồn tại React memory và bị xóa khi auth generation, active workspace, Zone hoặc catalog revision đổi. Controlplane không decrypt/read-back `parameter_envelope`, nên configuration detail không prefill customer value từ revision cũ; update phải có input document theo schema của revision đang pin. Instance `code` là business identity, còn `name` chỉ là display metadata; cả hai không phải blueprint parameter hay Kubernetes `metadata.name`.

Review layout là form desktop 8/4 với sticky summary: instance code/name, service/version, revision, workspace, Zone và validation summary; không echo raw customer parameter như một durable read-back surface. Mobile xếp summary sau form và có action footer. Confirm gửi `code` cùng canonical create intent; không có HTTP `Idempotency-Key` và không auto-retry mutation. Sau network failure, user có thể submit lại cùng code/cùng intent để lấy instance hoặc operation đã tồn tại. `ACCEPTED` chỉ là desired state durable, nên cache không optimistic thành `READY`/`ACTIVE`.

Detail tách desired revision/configuration, durable observed state và
current/latest operation. Tabs V1: Overview, Configuration, safe Connection outputs
và Operations/activity. Managed Service không có `runtime:<user_id>` channel:
V1 không tạo Dataplane/JO NATS runtime protocol. Overview luôn rehydrate
desired/terminal truth từ Controlplane; update/delete/retry chỉ hiện khi
API trả action được phép; delete failure giữ `DELETING` và chỉ có retry delete,
không UI rollback hay browser activity record riêng theo attempt/status.

Customer Logs/Metrics là Zone-local observability read path, không phải operation
runtime truth. Managed-service workload đi qua OTel Collector vào VictoriaMetrics/
VictoriaLogs của đúng Zone. Browser trước hết đi assertion path qua Zone Control Edge
để nhận scoped ticket TTL 5 phút, rồi mở stream tối đa 5 phút qua Zone Public Edge tới Rust service
`zone-observability-stream`. Service chỉ nhận trusted scope đã inject, tự thêm filter
Zone/workspace/owner/instance/component và chỉ chạy panel allow-list; Console không
query Victoria trực tiếp hoặc gửi raw PromQL/LogsQL. Metrics là sampled/eventual,
logs tail có thể mất khi reconnect; `SUCCESS`/`FAILED` vẫn chỉ được xác nhận qua
Controlplane operation/timeline.

Mọi query dùng `useConsoleQueryScope()` làm cache fence và `fetchJSON` qua Envoy; feature không tạo cache/store/realtime client thứ hai. Mutation invalidate durable query prefix nhỏ nhất rồi refetch. Context scope không là authorization claim; backend vẫn kiểm soát owner, workspace, Zone và permission.

Realtime dùng `useRealtime()` tập trung cho `job.notification` wake-up: feature
coalesce invalidate list/detail/operation query rồi HTTP rehydrate durable truth,
kể cả reconnect. Không tạo Managed Service Centrifugo runtime adapter. Zone
observability stream là SSE/read transport riêng, đóng ngay khi auth generation,
workspace, Zone hoặc instance scope đổi; nó không merge hoặc ghi đè operation state.
Wire timeline identity là stable `notification_id = UUIDv5(operation_id)`; payload
bắt buộc có `notification_id`, `status_version`, `resource_id`, `operation_id`.
Console fence `(notification_id, status_version)`, không dedupe chỉ operation ID,
để `PROCESSING → SUCCESS|FAILED` không bị nuốt.

Gate S07 gồm mock toàn bộ S05 type/cardinality/i18n fallback, stale revision, unsupported widget, scope change, active operation, delete retry và reconnect. Realtime test phải chứng minh terminal update cùng notification ID làm query rehydrate; browser không chứa raw YAML, protected metadata hay plaintext secret.

## 7. Customer provisioning end-to-end

### 7.1. Khám phá catalog

Console gọi catalog API qua Envoy. Backend trả category, application, version và revision published mà customer có quyền dùng.

Zone binding, capability và quota được tính từ trusted active Zone context, session,
workspace và catalog; client không gửi Zone routing trong create request.

### 7.2. Nhập và nhận request

Console dựng form từ `input_schema` và `ui_schema`. Handler kiểm tra path/query/body,
`code`, `name`, UUID, pagination, schema, payload size và context do Envoy/ACR
inject. Không có HTTP `Idempotency-Key`.

Handler map DTO thành entity riêng của workflow. Service/repository không parse lại DTO, không lặp HTTP validation và không kiểm tra nil dependency.

### 7.3. Tạo desired state

Handler canonicalize create intent và map DTO thành entity workflow. Service không
parse hoặc validate lại transport input, permission hay cấp bậc. Repository CTE
atomically áp predicate durable về revision default/provisionable, Zone capability,
unique `(workspace_id, code)`, desired-state transition và non-terminal operation.

Create trùng code với cùng canonical create intent trả instance/current CREATE
operation; cùng code nhưng intent khác trả conflict ổn định. Update có cùng target
desired hash trả operation non-terminal hiện có; target khác khi operation đang
chạy trả conflict. Delete lặp khi `DELETING` trả delete operation hiện có; sau hard
delete thành công, code có thể được dùng lại bởi instance UUID mới.

Repository dùng một CTE/transaction atomic cho instance, operation và outbox. Kết quả trả taxonomy error hoặc entity; không đẩy raw `pgx` lên handler.

Instance lưu revision và hash đã chọn. `accepted` chỉ nghĩa desired state đã bền vững, không có nghĩa Kubernetes đã Ready.

### 7.4. Dispatch durable

JO đọc logical WAL/CDC của module outbox; không có outbox relay khác và CP không
publish Kafka trực tiếp. Outer `JobCommandV1` dùng `job_id=command_event_id`,
`job_topic=managed_service.instance.execute`, `source_domain=MANAGED_SERVICE`,
`resource_id=instance_id`, `attempt=0..4` và target Zone. Inner
`ManagedServiceCommandV1` pin command/operation/instance IDs, owner/workspace,
instance code, operation kind, generation/attempt, instance/blueprint revision,
canonical template bundle + component contract, bundle/contract/input/desired hashes,
opaque `parameter_envelope` + digest, schema version và trace context.
Toàn bộ command là outbox `payload`; envelope chỉ là nested ciphertext field.

Kafka key là `instance_id` để giữ ordering cục bộ. JO chỉ advance checkpoint sau
Kafka ACK `acks=all`; crash giữa publish và checkpoint tạo duplicate an toàn. Một
retryable result tạo command event mới với cùng operation/generation/revision và
attempt tăng; tối đa năm attempt `0..4`, base backoff `30s/2m/10m/30m` cộng jitter
được persist ở outbox `available_at`. Due retry scan bounded của chính JO dispatcher
chỉ phục hồi timer/CDC miss, không là relay thứ hai và không mutate aggregate.

### 7.5. Render và apply tại Dataplane

Dataplane Zone chỉ consume command dành cho Zone đó. Trước side effect, Dataplane
validate outer/inner protobuf, schema version, Zone binding, source event, revision,
all hashes, parameter-envelope digest, payload size và execution fence
`instance_id + operation_id + generation`; `attempt` chỉ correlate source command/
result và không làm yếu dedupe side effect. Đây là idempotency boundary của
at-least-once transport, không phải HTTP `Idempotency-Key`.

Render engine lấy đúng revision, chỉ Zone Dataplane mới mở `parameter_envelope` trong memory, rồi thay `!aurora/param` theo exact key và tạo manifest deterministic. Rendered YAML chỉ ở memory hoặc vùng tạm được bảo vệ trong lúc apply; không ghi plaintext manifest hay raw customer value về Central. Sau Kubernetes API apply, Dataplane không giữ db name/password hay decrypted map: Kubernetes giữ runtime config/Secret, còn CP chỉ giữ ciphertext desired input để replay/reconcile.

Dataplane apply namespaced object/CRD đã qua discovery và dry-run bằng Kubernetes API
của Zone. Nó dùng Zone-local lease/fence chỉ để giảm concurrent execution, server-side
apply/field manager cố định, protected ownership marker và component order/readiness
đã pin. Success chỉ sau mọi component Ready; delete chỉ success khi workload,
finalizer, service và network policy đã gone theo delete order. Partial apply không
blind rollback; retry/reconcile dùng cùng desired hash/fence. Mỗi external side effect
có idempotency boundary; at-least-once không phải exactly-once xuyên Kafka,
Kubernetes và PostgreSQL.

### 7.6. Result và reconcile

Dataplane gửi one terminal `ManagedServiceResultV1` nested trong platform result:
unique result event, source command event, all fences/hashes, safe observed snapshot và
outcome `SUCCEEDED|RETRYABLE_FAILURE|TERMINAL_FAILURE`. Không có customer runtime
progress protocol/NATS trong V1.

JO relay theo ordering của instance. CP result-inbox CTE dedupe result event, verify
all fences trước observed update, promote đúng pending revision khi success, clear
pending update khi terminal fail, giữ create `PROVISIONING` và delete `DELETING` khi
terminal fail. Retry/delayed outbox/DLQ và timeline update chỉ xảy ra transactionally.

Reconciler phát lại exact current operation khi mất result, Zone restart hoặc observed
version lệch. Nó bounded/jitter/lease-fenced, không tạo revision hoặc mutate desired
state ngoài settling exact pending operation; stale result không ghi đè observed mới.

## 8. API và transport contract dự kiến

SRE catalog là admin-plane riêng. Normal route đi qua Envoy vào
`/admin/managed-services/catalog/*`; critical route dùng prefix cố định
`/admin/critical/managed-services/catalog/*` để ACR nhận diện và bắt critical
proof. Controlplane **không** gắn `middleware.Authorize`, không đọc RBAC
permission/level tại các route này. ACR/Envoy phải authenticate/authorize admin route
trước khi forward, strip mọi header nội bộ do client gửi và inject trusted actor
identity chỉ cho audit. Critical route còn phải có ACR-injected proof marker/challenge
ID; thiếu marker fail-close tại Controlplane nhưng CP không tự diễn giải actor thành
role hay level hoặc tự verify chữ ký/nonce của ACR.

SRE API shape:

```text
POST  /admin/managed-services/catalog/categories
GET   /admin/managed-services/catalog/categories
GET   /admin/managed-services/catalog/categories/:category_id
PATCH /admin/managed-services/catalog/categories/:category_id

POST  /admin/managed-services/catalog/definitions
GET   /admin/managed-services/catalog/definitions
GET   /admin/managed-services/catalog/definitions/:definition_id
PATCH /admin/managed-services/catalog/definitions/:definition_id

POST  /admin/managed-services/catalog/versions
GET   /admin/managed-services/catalog/versions
GET   /admin/managed-services/catalog/versions/:version_id
PATCH /admin/managed-services/catalog/versions/:version_id
GET   /admin/managed-services/catalog/versions/:version_id/blueprint
GET   /admin/managed-services/catalog/blueprints/:blueprint_id
GET   /admin/managed-services/catalog/blueprints/:blueprint_id/revisions
GET   /admin/managed-services/catalog/drafts/:draft_id
GET   /admin/managed-services/catalog/audit
```

State transition và mọi hành vi thay đổi blueprint/revision luôn dùng critical route;
không có normal mirror:

```text
POST   /admin/critical/managed-services/catalog/categories/:category_id/retire
POST   /admin/critical/managed-services/catalog/definitions/:definition_id/retire
POST   /admin/critical/managed-services/catalog/versions/:version_id/deprecate
POST   /admin/critical/managed-services/catalog/versions/:version_id/retire
POST   /admin/critical/managed-services/catalog/versions/:version_id/blueprints
DELETE /admin/critical/managed-services/catalog/blueprints/:blueprint_id
POST   /admin/critical/managed-services/catalog/blueprints/:blueprint_id/drafts
PATCH  /admin/critical/managed-services/catalog/drafts/:draft_id
POST   /admin/critical/managed-services/catalog/drafts/:draft_id/validate
POST   /admin/critical/managed-services/catalog/drafts/:draft_id/publish
POST   /admin/critical/managed-services/catalog/revisions/:revision_id/retire
DELETE /admin/critical/managed-services/catalog/drafts/:draft_id
```

`in_use` không dựa vào badge/UI cache: repository CTE tính atomically dependency
graph catalog, default pointer, personal/tenant instance revision pin và
non-terminal operation tại lúc mutation. Caller phải lấy proof mới rồi ký đúng
method/path/body cho mỗi critical route. Critical proof là gate bổ sung, không phải
quyền bypass immutability/FK: published revision đang pin không được hard-delete;
critical action chỉ được retire khi lifecycle cho phép, còn hard delete trả
`SRE_CATALOG_RECORD_PINNED`. Mọi critical mutation dùng expected catalog version trong
CTE. CTE lock target/revision/default pointer trước khi evaluate `in_use`; create pin
dùng compatible key-share/FK lock để không có race giữa check và customer provisioning.
Audit actor + challenge ID + target/version/hash/outcome và không auto-retry vì
nonce/proof một lần đã bị ACR consume.

Customer API có hai implementation branch cô lập nhưng cùng route shape:

```text
/api/v1/personal/managed-services/*
/api/v1/tenant/managed-services/*
```

```text
GET    /catalog
GET    /catalog/versions/:version_id
GET    /instances
POST   /instances
GET    /instances/:code
PATCH  /instances/:code/name
PATCH  /instances/:code/configuration
DELETE /instances/:code
GET    /instances/:code/connection
GET    /instances/:code/operations
GET    /instances/:code/operations/:operation_id
POST   /instances/:code/operations/:operation_id/retry
```

Customer route mới đi qua Controlplane authorization middleware. Không có
`workspace_id`, `owner_id`, `zone_id`, permission, namespace hay raw manifest trong
path/body. Reconcile không là browser endpoint; nó thuộc JO/Dataplane workflow có
budget, lease và fencing riêng.

Internal contract:

* PostgreSQL outbox/CDC record cho JO;
* Kafka command, result và report envelope;
* projection/reconcile giữa JO và Controlplane;
* render/apply giữa Kafka và Dataplane.

Response HTTP dựng inline bằng `gin.H`. Request JSON struct chỉ nằm trong `transport/http/dto`. Error map từ taxonomy ổn định, không trả raw provider hoặc database error.

## 9. Invariant bắt buộc

1. Published revision immutable.
2. Instance luôn pin revision và bundle hash.
3. Desired state và outbox commit nguyên tử.
4. Không publish broker trước PostgreSQL commit.
5. Controlplane không gọi Kubernetes.
6. Dataplane không quyết định ownership hoặc permission.
7. Mỗi workflow có một handler, service và repository method riêng.
8. HTTP validation ở handler; transport validation mới ở Dataplane.
9. Redis/NATS runtime state không là business source of truth.
10. Retry và duplicate phải an toàn; ordering chỉ theo aggregate key.
11. Secret không đi qua template plaintext hoặc log.
12. Zone A không consume command của Zone B.
13. Client không tự chọn identity hoặc permission.
14. Delete có cleanup policy và retry semantics rõ ràng.
15. HTTP mutation không dùng `Idempotency-Key`; create dedupe bằng unique
    `(workspace_id, code)` cùng canonical intent, còn Dataplane fence theo
    `instance_id + operation_id + generation`.
16. SRE catalog chỉ vào qua admin edge policy; Controlplane không đánh giá
    permission/level cho `/admin/managed-services/catalog/*` hoặc
    `/admin/critical/managed-services/catalog/*`.
17. SRE mutation/delete của catalog record đang `in_use` phải dùng
    `/admin/critical/managed-services/catalog/*`; ACR proof là bắt buộc nhưng không
    bypass published-revision immutability hoặc durable pin/FK.

## 10. Không nằm trong bộ khung hiện tại

* Chưa viết render engine, Kubernetes client hoặc provisioning workflow.
* Chưa tạo migration, bảng, Kafka topic, protobuf hay implementation cho
  `parameter_envelope`.
* Chưa thiết kế billing, quota, pricing hay automatic user/workspace creation.
* Chưa implement YAML AST renderer/dry-run/apply dù contract cho namespaced Kind/CRD
  do SRE định nghĩa đã được chốt; chưa dùng live status để xác nhận durable business
  completion.
* Chưa thêm NATS path mới giữa các ứng dụng Central.

## 11. Tiêu chí thành công

Thêm service mới chỉ cần catalog + blueprint revision, không fork backend.

Console dựng form từ contract mà không biết PostgreSQL hay Kubernetes.

Command gửi lại sau crash không tạo duplicate side effect.

Zone mất kết nối không làm mất desired state tại Controlplane.

Revision mới không thay đổi instance chạy revision cũ.

SRE nhìn được revision, hash, operation và audit; customer chỉ nhìn resource trong ownership scope.

Đây là nền tảng để Aurora provisioning dịch vụ cloud linh hoạt, versioned, kiểm soát được và mở rộng theo số lượng service lẫn Zone.
