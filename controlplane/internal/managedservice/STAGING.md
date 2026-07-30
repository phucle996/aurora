# Managed Service Platform — Staging map trước Phase và Task

> Đây là staging design document, không phải danh sách task implementation.
> S00–S14 đóng contract thiết kế trước implementation; S15–S17 là gate bằng
> chứng sau implementation/release; chỉ sau đó mới tách phase/task.

Tài liệu này bổ sung cho [IDEA.md](./IDEA.md).
`IDEA.md` mô tả module hướng tới điều gì.
`STAGING.md` mô tả thứ tự các quyết định phải đóng trước khi code workflow.
Lifecycle Source of Truth sau P00 là
[Managed Service Lifecycle God View](../../../god_view/managedservice/managed_service_lifecycle_god_view.md).
Khi staging và God View mâu thuẫn, God View phải được cập nhật trước khi tiếp tục phase.

## 1. Cách dùng

Mỗi stage tạo một decision packet review được độc lập.
Không chuyển stage chỉ vì code đã compile.
S00–S14 chỉ đóng khi contract, owner và failure semantics đã được ghi; S15–S17
chỉ đóng khi có evidence chạy thật. Không gọi release gate là đã hoàn tất khi
module chưa tồn tại.
Topology thay đổi thì God View liên quan phải cập nhật trước phase/task.
Một quyết định chưa chốt không phải behavior đã ship.
P01 có durable baseline/canonical protobuf dormant; P02 đã có SRE catalog/admin
workflow; P03 đã có customer catalog/form read-only cho hai nhánh personal và
tenant. Customer mutation và transport worker vẫn chưa được enable.
## 2. Artifact sau staging

* problem statement, actor matrix và in/out-of-scope;
* ownership/connection matrix giữa Console, Envoy, CP, JO, Kafka, DP;
* domain glossary và lifecycle/state machine;
* catalog, blueprint, schema và immutable revision contract;
* render input/output, ownership injection, secret policy và Dataplane apply;
* API, validation, taxonomy, duplicate-request và execution-fence contract;
* durable state, CTE, transaction, outbox, ordering và checkpoint;
* Kafka command/result envelope, retry và DLQ;
* result, observed status, reconcile, fencing và recovery;
* threat model, audit, metric, trace và alert;
* retry budget, Kafka envelope, result inbox/reconcile và error taxonomy;
* test matrix, staging environment, drills, pilot runbook, rollback, release gates
  và God View change list.
## 3. Stage map

| Stage | Chủ đề | Gate chính |
| --- | --- | --- |
| S00 | Intent và scope | use case, actor, out-of-scope |
| S01 | Topology/ownership | connection và trust boundary |
| S02 | Domain vocabulary | object, owner, lifecycle |
| S03 | State machine | transition, retry, terminal |
| S04 | Catalog | SRE metadata và Console schema |
| S05 | Blueprint | parameter/input/output |
| S06 | Render policy | deterministic YAML và ownership injection |
| S07 | Console | dynamic form và UX boundary |
| S08 | Controlplane API | handler/entity/taxonomy |
| S09 | Persistence | schema, CTE, outbox |
| S10 | JO/Kafka durable dispatch | CDC, envelope, ordering, retry, DLQ |
| S11 | Dataplane | render, apply, execution fence, Zone telemetry identity |
| S12 | Result/reconcile | observed state, fencing |
| S13 | Security | identity, authorization, secret |
| S14 | Observability | trace, metric, audit |
| S15 | Failure drill | release evidence: crash, duplicate, outage |
| S16 | Test staging | release evidence: fixture, environment, E2E |
| S17 | Pilot rollout | release evidence: scope, rollout, rollback |
| S18 | Phase extraction | staging packet thành phase/task |

## 4. Invariant chung

1. Controlplane sở hữu catalog, ownership và desired state.
2. Dataplane sở hữu runtime execution trong đúng Zone.
3. JO bridge durable Central state với Zone transport.
4. Kafka là durable Central↔Zone command, result và durable report transport.
5. Managed Service V1 không định nghĩa NATS Core subject, runtime envelope hay
   JO runtime consumer. NATS không thay Kafka cho command/result; một future
   soft-state workflow chỉ được thêm khi có concrete JO-owned consumer và God
   View riêng, không có browser path.
6. Zone NATS JetStream KV không phải Controlplane database.
7. Console không kết nối DB, Redis, Kafka, Vault hoặc Kubernetes.
8. Controlplane không gọi Kubernetes API.
9. Dataplane không quyết định owner, workspace, Zone hay permission.
10. Published blueprint revision immutable; instance pin revision/hash.
11. Desired state và outbox commit trong cùng transaction.
12. At-least-once mặc định; ordering chỉ theo aggregate key.
13. Retry bounded, backoff/jitter, DLQ hoặc quarantine.
14. Redis/NATS runtime state không là business source of truth.
15. Plaintext secret không đi qua template, event, log hoặc business DB.
16. Handler validate HTTP; Dataplane validate transport boundary mới.
17. Service/repository không parse DTO hoặc kiểm tra nil dependency lần hai.
## 5. S00 — Intent và scope

**Status: CLOSED**

Mục tiêu: đóng bài toán, người dùng và giới hạn của module.

### Quyết định đã chốt

* Một SRE principal duy nhất publish catalog/revision; `published_by` chỉ audit.
* Middleware kiểm soát static permission năm bậc: personal là toàn bộ resource
  user; tenant là workspace được cấp phép.
* Handler dùng trusted auth context; repository scope owner/workspace từ context.
* `Resource` là managed service lifecycle; customer chỉ điều khiển exposure,
  port, firewall, network-policy parameters trong allow-list, không raw YAML.
* Instance là graph nhiều component: `workload.yaml`, `service.yaml`,
  `network-policy.yaml`; Strimzi/operator là dependency có sẵn trong Zone.
* Customer không gửi `zone_id`; Zone đến từ trusted Envoy context, CP kiểm tra
  revision hỗ trợ Zone.
* Update tạo `InstanceRevision` immutable; correction tạo revision mới, transient
  failure retry operation hiện tại.
* Delete hard-delete business record sau DP xác nhận graph đã xóa; intent/outbox
  phải tồn tại trước side effect.
* Delete failure giữ instance `DELETING` cùng DELETE operation
  `TERMINAL_FAILED`, không rollback mù; deletion fence giữ trong retry window để
  stale command không resurrect resource.

Ngoài scope: Kubernetes dashboard, arbitrary Helm runner, CI/CD engine, billing,
secret vault, owner directory và runtime monitoring database.
Mọi mutation dùng ba bước:
```text
durable intent → Kubernetes side effect → durable finalization
```
Create/update ghi intent trước; `accepted`/`deleting` chưa phải runtime success.
JO finalizes active state/hard-delete sau result hợp lệ và operation/generation.
Output: problem statement, actor/authorization matrix, graph manifest,
delete/revision semantics, pilot và decision log.
Gate: owners ký; không còn CP→Kubernetes use case; S00 không còn quyết định mở.

## 6. S01 — Topology và ownership

**Status: CLOSED**

Luồng chuẩn:
```text
Browser/SDK → Envoy/ACR → Controlplane API → PostgreSQL
  → CDC/WAL/outbox → JO → Kafka → Dataplane Zone → Kubernetes API
```
JO là bridge bắt buộc cho mọi Managed Service Central↔Zone command/result. Controlplane chỉ ghi PostgreSQL outbox/CDC; không có publish Kafka trực tiếp hay fallback bypass JO khi JO chậm/lỗi.

Result và user timeline đi theo hai nhánh sau JO:
```text
Dataplane result → Kafka → JO fence/settle CP projection
  ├→ authoritative API projection
  └→ XADD stream:{job_notifications}
       → Notification Service Scylla timeline/inbox upsert
       → Centrifugo notifications:<user_id> → Console realtime upsert
```
JO XADD `PROCESSING` sau command Kafka durable, rồi XADD `SUCCESS` hoặc `FAILED`
sau result settle với cùng timeline identity. Một operation/resource action có đúng
một Scylla timeline/inbox record; status/attempt chỉ update record đó. Terminal thiếu
processing vẫn upsert cùng record.
`timeline_id` là UUIDv5 ổn định theo `operation_id`, không theo status/attempt.
Event giữ original creation timestamp; chỉ status, severity, message, updated time và
monotonic version mutable. Scylla upsert xong trước Centrifugo và không xóa `read_at`.
XADD lỗi/đầy chỉ metric/log/alert, không rollback business operation.
`stream:{job_notifications}` đi tới Scylla; không dùng `runtime:<user_id>`.

Ownership:

* Console giữ UI state, form và polling/realtime merge.
* Envoy/ACR verify session, identity và critical proof.
* Controlplane giữ catalog, desired state, owner, Zone binding, operation.
* JO giữ CDC/outbox, checkpoint, Kafka bridge và fencing.
* Kafka giữ durable transport, không phải business DB; Dataplane render/apply/report.
* Kubernetes giữ actual resource state.

JO không có credential Zone KV; Dataplane chỉ nhận đúng Zone credential.
Gate: matrix, timeline sequence, trust diagram và God View change list được security review; ACL/NetworkPolicy map được từ matrix.

## 7. S02 — Domain vocabulary

**Status: CLOSED**

Canonical model và example Kafka:

```text
ServiceCategory: Messaging
  → ServiceDefinition: Apache Kafka
    → ServiceVersion: Kafka 3.8 (Strimzi operator đã có sẵn trong Zone)
      → ServiceBlueprint: kafka-standard
        → BlueprintRevision: 3
          → ManagedServiceInstance: orders-kafka (tenant workspace A, current Zone)
            ├→ InstanceRevision: 1 (replicas=3, storage=100Gi, private)
            └→ InstanceRevision: 2 (expose 9094, allowed CIDR=10.0.0.0/8)

Dataplane output: Kafka CR + Service + NetworkPolicy
```

`BlueprintRevision` là template immutable do SRE publish và dùng chung. `InstanceRevision` là snapshot immutable config của một customer; `ManagedServiceInstance` chỉ chuyển active head sau Dataplane success. Kubernetes objects là implementation output, không phải public entity.

Mỗi workflow sở hữu entity phẳng riêng; không dùng `CatalogInput`, `ResourceEntity`, `TemplateEntity` chung mọi flow hay DTO transport làm service entity.
Output: glossary, entity ownership table, ID/naming policy, sensitivity map; gate: mọi API field map được tới owner; template, blueprint, revision không mơ hồ.

## 8. S03 — Lifecycle và state machine

**Status: CLOSED**

```text
ManagedServiceInstance
  lifecycle: PROVISIONING | ACTIVE | DELETING
  active_revision_id | pending_revision_id | observed_state | observed_generation

ManagedServiceOperation
  kind: CREATE | UPDATE | DELETE
  status: ACCEPTED | DISPATCHING | RUNNING | RETRYING | SUCCEEDED | TERMINAL_FAILED
  target_revision_id | operation_id | generation | attempt | last_sanitized_error
```

`Instance` là resource lâu dài; `Operation` là một lần thực thi thay đổi resource.
`InstanceRevision` pin config customer cùng blueprint revision/bundle hash; immutable.

Transition chung:

```text
ACCEPTED → DISPATCHING → RUNNING → SUCCEEDED
                         ↘ RETRYING → DISPATCHING
                         ↘ TERMINAL_FAILED
```

Create tạo instance `PROVISIONING`, pending revision và CREATE operation; success
promote active revision rồi `ACTIVE`. Create terminal fail giữ `PROVISIONING` để
customer chỉ có retry hoặc delete, không được giả `ACTIVE`. Update giữ active
revision cũ, tạo pending immutable revision; success promote, terminal fail clear
pending nhưng operation vẫn giữ target revision để audit/manual retry. Delete set
`DELETING` trước side effect; success hard-delete sau DP xác nhận graph đã xóa;
failure giữ `DELETING` + DELETE operation `TERMINAL_FAILED`, không rollback `ACTIVE`
và có deletion fence. Không tồn tại lifecycle/operation state `DELETE_FAILED`.

Mỗi instance chỉ có một operation non-terminal. HTTP create dedupe bằng `code`
unique trong workspace cùng canonical create intent; không có `Idempotency-Key`.
Dataplane dedupe side effect bằng `instance_id + operation_id + generation`, không
dựa vào code hoặc HTTP header.
Mỗi operation có tối đa năm command attempt `0..4`. Lỗi retryable tạo command
event mới nhưng giữ nguyên `operation_id`, `generation`, target revision và desired
hash; chỉ `event_id` và `attempt` tăng. Backoff được persist cùng outbox
`available_at`: lần retry 1–4 lần lượt có base `30s`, `2m`, `10m`, `30m`, cộng
jitter 0–20%; crash không được tính lại hoặc reset clock. Manual retry tạo operation
và generation mới, không tái dùng retry budget cũ. Khi attempt 4 vẫn retryable,
operation thành `TERMINAL_FAILED` với `RETRY_BUDGET_EXHAUSTED` và last cause đã
sanitize.

Result phải khớp source command event, instance, operation, generation, attempt,
target revision, bundle/contract/desired hash và Zone; stale result bị ignore có
metric/audit, không mutate desired/observed state. Timeline map
`ACCEPTED..RETRYING → PROCESSING`, `SUCCEEDED → SUCCESS`,
`TERMINAL_FAILED → FAILED` trên cùng timeline ID.

Gate: state diagram có guard, owner, retry/terminal semantics, retry budget và
không có state ad hoc.

## 9. S04 — Catalog contract

**Status: CLOSED**

Public catalog chỉ phục vụ discovery/form contract, không mang runtime, quota động, Kubernetes
object, raw template/bundle hay Zone routing từ client. Model là `Category → Definition → Version
→ Blueprint → BlueprintRevision`; V1 có đúng một blueprint nội bộ cho một version.

Customer chỉ chọn category/application/version. Version trả default published revision để Console dựng form; create gửi revision ID và CP chỉ nhận khi nó vẫn default/provisionable.
Stale form sau publish phải refresh, không âm thầm đổi contract.

Catalog `code` immutable/non-reusable; `ManagedServiceInstance.code` immutable,
DNS-label tối đa 35 ký tự, unique trên toàn module trong workspace và có thể reuse
sau hard delete thành công. Code là API identity và Kubernetes workload-name base;
instance `name` chỉ là display metadata, không unique. Display metadata có i18n
`en` fallback, bounded text và design-system `icon_key`. Version là
`AVAILABLE|DEPRECATED|RETIRED`; revision là
`DRAFT|PUBLISHED|RETIRED`. Deprecated chặn create mới nhưng instance pin revision vẫn update;
retired chỉ pending/retry/reconcile/delete. Published immutable; publish mới chỉ đổi default pointer.

Revision giữ canonical multi-document YAML bundle bounded trong PostgreSQL, cùng
`bundle_hash`, `contract_hash`, versioned input/UI/output contract, immutable Zone
selector/capability requirement, component/readiness contract và audit. V1 không có
bundle registry ngoài; bundle không đi ra public catalog API nhưng exact bytes được
đóng trong command nội bộ để DP render đúng revision. Form là restricted `Platform
Form Contract`.
UI chỉ presentation. YAML template và input schema là hai artifact độc lập của cùng SRE revision; CP validate schema nhưng không scan variable/tag hay tự tạo binding giữa hai artifact. Zone Envoy context phải match selector; health/capacity/maintenance không là catalog state.

Publish atomically validate hierarchy/form/UI reference/Zone policy, canonicalize-hash, audit,
publish immutable revision và switch default. Gate: Console mock được catalog/form; stale/default,
retire và Zone mismatch có negative case; hash reproduce được revision.

## 10. S05 — Blueprint và parameter

**Status: CLOSED** — `Platform Form Contract v1` là flat typed map, không nested object/map, raw JSON/YAML/Kubernetes fragment hoặc `null`.
Type: STRING/BOOLEAN/INT64/DECIMAL/ENUM/DNS_LABEL/CIDR/PORT/DURATION/BYTE_SIZE; cardinality: ONE/LIST/SET.
Schema chỉ nói Console nhận/validate customer value nào. Template không bind field khai báo: SRE tự
giữ template tag và schema tương ứng; CP không enumerate tag, không tạo binding table và không
coi unused schema key là lỗi business. Literal hay operator tự generate là YAML/CRD content do SRE
viết, không phải source branch của platform.
Handler reject unknown/type/range/size, canonicalize và tạo entity/hash; service/repository không
validate lại. List giữ order, set sort/dedupe; retry không đưa timestamp/random/attempt vào hash.
Sau validation, full parameter map được seal thành Zone-bound opaque `parameter_envelope`; durable
instance revision giữ envelope + digest, không giữ raw map. Outbox `payload BYTEA` là toàn bộ
Managed Service command Protobuf; `parameter_envelope` chỉ là một field ciphertext bên trong
payload đó, không phải runtime record riêng. DP chỉ mở plaintext trong RAM của một execution để
render, rồi quên nó; không ghi DB, Redis, NATS, Zone KV hay disk. Kubernetes là nơi giữ runtime
config đã materialize: non-secret vào CRD/spec/ConfigMap theo YAML của SRE, sensitive value vào
`v1/Secret` hoặc Secret do operator tạo. Nếu `db_name` là customer parameter thì ciphertext vẫn
thuộc immutable desired revision để retry/reconcile reproducible, còn runtime value nằm ở
Kubernetes; value hard-code/operator-generated không cần xuất hiện trong envelope.
Output V1 chỉ là typed observed metadata declared-safe, ví dụ host/port/database/
TLS server name. DP chỉ report value do executor biết an toàn; không có arbitrary
JSONPath, Secret read, SecretKeyRef, raw input, password/token, connection URI có
credential hay API đọc Kubernetes Secret. Limit: 64 field, 64 KiB canonical
document, 4 KiB/string, 64 list item, 128 enum value. Gate: golden
canonical/hash/envelope và negative cases cho unknown/null/type/overflow/CIDR/duplicate/create-
only/size; retry cùng input/context ra cùng hash.

## 11. S06 — Render policy

**Status: CLOSED** — SRE publish multi-document YAML immutable như một flat
artifact; `apiVersion`/`kind` static, không Kind allow-list tại CP và không có
product render timeout. CP không phân tích variable trong artifact. Dataplane YAML
AST chỉ có `!aurora/param` và platform `!aurora/component`; không text
interpolation, loop/include/function tùy ý.

Customer instance chỉ render namespaced Kind/CRD; DP discovery/dry-run validate
Zone API, cluster scope là SRE platform bootstrap. Không dùng Kind allow-list để
đoán policy: execution bị giới hạn bởi Zone executor ServiceAccount/RBAC và pinned
capability profile của revision. DP force namespace
`aur-ms-{t|p}-{base32lower(owner_uuid_bytes||workspace_uuid_bytes)}`. DP sở hữu
reserved annotations workspace/owner/instance/revision/render hash và internal
instance/component labels; owner/workspace không là label V1, customer không
override, cross-workspace traffic label chưa tồn tại.

Zone OTel Collector được phép copy protected annotation thành telemetry-only
attributes cho query scope, nhưng không tạo Kubernetes traffic label và phải
overwrite attribute do workload tự khai.

`!aurora/param <name>` exact-match một key trong payload đã được handler
validate/seal và DP thay bằng typed YAML node. Tag được dùng ở value node ngoài
`apiVersion`, `kind` và toàn bộ `metadata`; namespace, naming, protected
annotation/label không bao giờ là parameter. Missing key, type không render được
hoặc YAML sau render invalid trả terminal `SRE_TEMPLATE_INPUT_MISMATCH`, không empty
fallback hay retry vô hạn. Template không set namespace/reserved metadata; foreign
object cùng tên không adopt. `!aurora/component primary` resolve đúng instance code;
component còn lại resolve `code-component_id`, nên code ≤35 và component ID ≤27. Pod
thật vẫn do Deployment/StatefulSet controller đặt suffix.

Mỗi revision còn pin component contract tĩnh: component ID, document set,
apply-order, delete-order và readiness rule/deadline. V1 graph chuẩn apply
`network-policy → service → workload`; delete `workload → wait finalizer/gone →
service → network-policy`. SRE có thể khai báo graph khác nhưng phải explicit order
và readiness rule; apply API success không phải `SUCCEEDED`. DP dùng server-side
apply với field manager cố định; `force=true` chỉ khi ownership marker cùng instance,
foreign marker là terminal `K8S_OWNERSHIP_CONFLICT`. Partial apply không blind
rollback: reconcile cùng desired hash/fence tiếp tục hoặc trả result taxonomy. Không
có render timeout; cancellation/graceful shutdown và byte/document budget bảo vệ HA,
còn readiness deadline tĩnh của revision (bị Zone policy cap) quyết định khi
Pending/PVC/capacity trở thành retryable/terminal result. Gate: golden
YAML/tag/name/namespace/hash, dry-run, RBAC, ownership, ordering và readiness
negative cases.

## 12. S07 — Console contract

**Status: CLOSED** — `Managed Services` là table-first list/quick detail, create flow catalog → configure → review và full instance detail. Customer chỉ chọn category/application/version; workspace/Zone/revision là read-only backend context.
Form renderer chỉ support finite S05 widget/type registry; draft ở memory, clear khi scope/revision đổi, không raw YAML/parameter/local persistence. Confirm gửi code cùng canonical create intent, không có `Idempotency-Key`; manual submit lại cùng intent chỉ lấy instance/operation đã tồn tại. `ACCEPTED` không optimistic thành Ready.
Detail tách desired/observed/operation; CP không decrypt/read-back `parameter_envelope`, nên update yêu cầu input document theo pinned revision thay vì prefill raw value cũ. Delete/retry chỉ theo action API. Query key dùng Console scope; publication chỉ wake-up/refetch, không merge business state.
Managed Service realtime dùng stable `notification_id` UUIDv5(operation_id) như timeline ID cùng `status_version`, `resource_id` và `operation_id`; dedupe/fence theo `(notification_id,status_version)`, không operation ID đơn lẻ. Gate: schema/stale/scope/operation/retry/reconnect tests.

P03 implementation chỉ mở discovery/form foundation; review dựng create intent trong
React memory nhưng không gọi mutation trước P04. Query cache key gồm auth generation,
personal/tenant mode, Zone, workspace và revision; HTTP response dùng
`Cache-Control: private, no-store`. `zone_selector` V1 chỉ có `all`/`allow_list`,
`capability_requirement.all_of` chỉ dùng static `zone_service_type` desired state.
Finite UI widget registry là
`TEXT|TEXTAREA|NUMBER|SWITCH|SELECT|RADIO|TOKEN_LIST|MULTI_SELECT`.

## 13. S08 — Controlplane API và validation

**Status: CLOSED**

Mỗi workflow giữ vertical slice cô lập:

```text
1 handler method → 1 service method → 1 repository method
```

Trùng code giữa personal/tenant hoặc workflow được chấp nhận khi giữ isolation.
Không có `helpers.go`, `common.go`, generic mapper/validator hay generic
`MutateInstance`. File tách theo object giống IAM/Storage:

```text
category
definition
version
blueprint
revision
audit
```

Mỗi object có entity/repository/service/DTO/handler file cùng tên; `domain/repo`
và `domain/service` tách interface theo object, constructor implementation trả về
interface tương ứng. JSON request struct chỉ ở DTO, struct business ở entity,
response dựng inline bằng `gin.H`.

Handler parse DTO, validate và canonicalize path/query/body/schema/payload/code/name
ngay tại transport boundary, map sang entity workflow và map taxonomy error sang HTTP
status ổn định. Service/repository tin entity đã normalize; không parse DTO, không
validate lại code/name/schema hay kiểm tra dependency nil. Repository trả taxonomy
error, không trả raw `pgx`.

SRE catalog là admin-plane:

```text
Admin UI → Envoy/ACR admin route policy
         → /admin/managed-services/catalog/*
         → Controlplane object handler tương ứng

Admin UI → Envoy/ACR critical proof policy
         → /admin/critical/managed-services/catalog/*
         → Controlplane object handler tương ứng
```

Route này không gắn `middleware.Authorize` tại Controlplane và không dùng CP
permission/level. ACR/Envoy là gate bắt buộc, strip header nội bộ client gửi và
inject trusted actor identity chỉ để audit. Header actor thiếu phải fail-close, nhưng
CP không diễn giải nó thành role/cấp bậc. Path chứa `/critical/` buộc ACR verify và
consume one-time critical proof bind method + path + body; CP chỉ fail-close nếu
Envoy-injected proof marker/challenge ID thiếu, không tự verify chữ ký/nonce. SRE
workflow là category, definition, version, blueprint, draft, validate, publish,
retire, delete và audit.

Category/definition/version metadata create/update có thể dùng admin route thường.
Mọi blueprint/draft/validate/publish/retire/delete có khả năng đổi runtime contract
đi trực tiếp qua `/admin/critical/`; không tồn tại normal mirror. Critical path dùng
CTE cùng transaction với expected catalog version/use predicate; proof không bypass
immutable published revision hoặc FK. In-use revision không hard-delete: critical
action chỉ retire nếu lifecycle cho phép, nếu không trả
`SRE_CATALOG_RECORD_PINNED`. Audit chỉ lưu actor, critical challenge ID,
action/target/version/hash/outcome; không lưu proof, nonce hay raw template. CTE lock
target/revision/default pointer trước khi evaluate `in_use`; create pin dùng compatible
key-share/FK lock để không có race giữa check và customer provisioning.

Customer có hai route branch implementation riêng, cùng public shape:

```text
/api/v1/personal/managed-services/*
/api/v1/tenant/managed-services/*

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

Customer routes dùng Controlplane authorization middleware. `workspace_id`, owner,
Zone, permission, namespace và manifest không nằm trong path/body. Reconcile là
JO/Dataplane workflow có work budget/lease/fencing, không là browser endpoint.

Không dùng HTTP `Idempotency-Key`. `ManagedServiceInstance.code` là lowercase,
immutable và unique `(workspace_id, code)` trên toàn module; `name` là display text
không unique. Create trùng code cùng canonical intent trả instance/current CREATE
operation, intent khác trả conflict. Update cùng desired hash trả operation đang
chạy; target khác khi có non-terminal operation trả conflict. Delete lặp khi
`DELETING` trả delete operation hiện có. Sau hard delete thành công có thể reuse
code, vì command cũ fence bằng UUID/generation.

Taxonomy có disposition cố định, không suy từ error string. Handler chỉ map nhóm
HTTP ingress; DP/JO dùng cùng code taxonomy trong Protobuf/result:

| Nhóm/code | Nơi tạo | Disposition |
| --- | --- | --- |
| `REQUEST_INVALID`, `CATALOG_STALE`, `INSTANCE_CODE_CONFLICT`, `OPERATION_CONFLICT` | handler/repository | HTTP 400/409, không tạo command |
| `SRE_TEMPLATE_INPUT_MISMATCH`, `K8S_APPLY_REJECTED`, `K8S_OWNERSHIP_CONFLICT`, `ZONE_PARAMETER_ENVELOPE_INVALID` | DP | terminal result, không auto-retry |
| `K8S_API_UNAVAILABLE`, `K8S_CAPACITY_PENDING`, `K8S_READINESS_DEADLINE_EXCEEDED`, `ZONE_EXECUTOR_UNAVAILABLE` | DP | retryable result; CP tạo attempt kế tiếp nếu còn budget |
| `COMMAND_CONTRACT_INVALID`, `COMMAND_ZONE_MISMATCH`, `COMMAND_HASH_MISMATCH` | JO/DP ingress | quarantine/DLQ rồi settle operation terminal bằng sanitized code |
| `RESULT_FENCE_MISMATCH`, `RESULT_STALE_ATTEMPT` | JO/CP result inbox | ignore + metric/audit, không đổi state và không DLQ lại |
| `RETRY_BUDGET_EXHAUSTED` | CP result settlement | terminal result sau attempt 4, giữ last sanitized cause |

Raw Kubernetes/provider/database detail không đi qua taxonomy message. Mọi message
cho API/timeline/DLQ bị bound 1 KiB và redact secret/manifest/envelope.

Gate: route/request/response/error/auth/duplicate/backpressure semantics có test
contract; duplicate create không tạo hai instance, missing trusted admin actor/proof
fail-close, không có normal mirror cho runtime-affecting catalog mutation, critical
delete pinned không hard delete và customer body không override trusted context.

## 14. S09 — Durable persistence

**Status: CLOSED**

Module dùng SQL schema riêng `managed_service`; config thêm
`SchemaSQL.ManagedService = "managed_service"` và app migration runner gọi
`managedservice.ApplyMigrations` trong transaction/advisory-lock toàn app. Có đúng
sáu baseline migration:

```text
000001 enum/type lifecycle
000002 system catalog hierarchy
000003 blueprint revision + catalog audit
000004 personal instance aggregate
000005 tenant instance aggregate
000006 module outbox + index + invariant trigger
```

Catalog là system data, không phải data "của SRE":

```text
service_categories
service_definitions
service_versions
service_blueprints
blueprint_revisions
catalog_audit_events
```

Không bảng catalog nào có prefix `sre_`, `owner_id`, `owner_type`, `workspace_id`
hay `zone_id`. Admin actor chỉ nằm ở `created_by`, `updated_by`, `published_by`,
`retired_by` để audit; không tạo ownership. Blueprint revision giữ canonical YAML
bundle bounded, bundle/contract hash, input/UI/output/component contract, Zone selector
và capability requirement. Draft mutable; published content immutable, chỉ cho phép
`PUBLISHED → RETIRED`; version default pointer chỉ trỏ published revision.

Mọi aggregate customer tách physical table theo ownership:

```text
personal_managed_service_instances
personal_managed_service_instance_revisions
personal_managed_service_operations
personal_managed_service_result_inbox
personal_managed_service_deletion_fences

tenant_managed_service_instances
tenant_managed_service_instance_revisions
tenant_managed_service_operations
tenant_managed_service_result_inbox
tenant_managed_service_deletion_fences
```

Personal branch giữ `user_id` snapshot; tenant branch giữ `tenant_id` snapshot —
không dùng `owner_type` polymorphic trong customer aggregate. Mỗi instance có
workspace/Zone snapshot, immutable `code`, display `name`, lifecycle,
active/pending revision head, generation/revision sequence, `create_intent_hash`,
bounded observed state/output và metadata version. Mỗi branch có
`UNIQUE(workspace_id, code)`. `code` là K8s workload-name base, `name` không đi vào
render. Instance revision giữ immutable opaque `parameter_envelope`, input/desired
hash, blueprint revision/bundle/contract pin; raw
canonical parameter map không vào DB. Operation cũng tách bảng theo branch và giữ
target revision/hash, generation, retry parent, status/status version, bounded
sanitized error và actor snapshot. `instance_id` của operation/result là immutable
snapshot UUID, không FK tới instance row: delete success có thể hard-delete instance
và revision, còn evidence/dedupe được dọn theo retention riêng. Operation/result
inbox/deletion fence chỉ được purge sau ít nhất
`max(command retention, result retention, DLQ replay retention) + safety margin`;
fence giữ `instance_id + operation_id + generation` qua toàn bộ cửa sổ đó nhưng
không cấm reuse `code`.

Outbox là transport record của module, không phải customer aggregate, nên có đúng
một `managed_service_outbox_records` theo shape Storage/Mail hiện có:

```text
id, event_id, zone_id, job_topic, payload BYTEA,
owner_id, owner_type, actor_user_id,
status, available_at, completed_at, job_version, resource_id,
payload_schema_version, trace_id, idle,
error_code, error_message, created_at, updated_at
```

`available_at` là durable retry clock; initial command có giá trị `created_at`, retry
command mang `attempt` trong payload và thời điểm đã jitter/persist. Chỉ `payload` là
Managed Service protobuf bytes; không thêm custom bridge/event-kind table. SRE
catalog không ghi runtime outbox.

Repository mutation là một CTE atomic. Lock order customer cố định:

```text
workspace FOR KEY SHARE → instance FOR UPDATE → revision/operation
```

Create serialize `(workspace_id, code)`, compare canonical create intent rồi insert
instance + initial revision + operation + outbox cùng commit. Update config dùng
expected active generation, tạo revision/generation/operation/outbox mới; rename chỉ
đổi display name. Delete chuyển `DELETING` + delete operation/outbox, không rollback
ACTIVE. Manual retry tạo operation/generation mới và không tạo revision config mới.
Result inbox unique `result_event_id` và unique source command `(operation_id,
attempt, source_command_event_id)`; duplicate/stale result không mutate desired state.
Result settlement là một CTE: insert inbox → lock instance/operation → verify all
fences → update observed snapshot → finalise operation/lifecycle hoặc insert retry
outbox có `available_at`. DELETE success atomically ghi deletion fence rồi hard-delete
instance/revision; fence giữ tới sau Kafka retry/DLQ/reconcile window và không cấm
reuse code.

Invariant: revision payload/hash immutable, active/pending head cùng instance và
không trùng nhau, một operation non-terminal mỗi instance, result khớp instance +
operation + generation + attempt + source event + revision/hash, error không raw
provider detail. Không lưu DB: rendered manifest, Kubernetes object/live metric,
worker lease, temporary render, raw result payload hay plaintext secret.

Gate: migration/rollback/lock-order review; duplicate create/update/delete/retry,
hard-delete/code reuse, stale result, crash trước/sau outbox commit và personal/tenant
ownership isolation đều có PostgreSQL integration test.

## 15. S10 — Job Orchestrator và Kafka durable dispatch

**Status: CLOSED**

Managed Service V1 có đúng một execution transport plane: PostgreSQL outbox/WAL →
JO → Kafka → Dataplane → Kafka result → JO/Controlplane. CP không publish Kafka
trực tiếp; JO không tạo business aggregate và không có Managed Service NATS consumer.
JO XADD `PROCESSING` chỉ sau Kafka command ACK; `SUCCESS|FAILED` chỉ sau result đã
settle transactionally. `stream:{job_notifications}` giữ một timeline/inbox row,
không phải runtime stream.

CDC logical replication là path dispatch chính. Transaction CP tạo
instance/revision/operation/outbox cùng commit. JO validate outbox, route Zone,
registry `source_domain + job_topic`, byte/schema/hash size rồi publish vào topic
đã provision sẵn `aurora.jobs.commands.zone.<zone_uuid>.v1`; Kafka producer idempotent
dùng `acks=all`. JO chỉ advance LSN sau durable ACK. Crash sau ACK trước LSN tạo
duplicate command an toàn; command key là `instance_id` (`resource_id`), nên ordering
chỉ được hứa theo instance, không phải globally.

Retry delay vẫn là một phần của cùng dispatcher, không phải outbox relay thứ hai.
Khi result retryable, CP result-settlement CTE insert outbox record mới với
`available_at` đã persist. CDC là admission path; record chưa đến hạn không publish
và LSN có thể advance. Một due-retry scan bounded, lease/fence và chỉ đọc đúng module
outbox `PENDING + available_at <= now()` của JO dispatcher là recovery/timer path để
không mất retry khi JO restart; nó không tạo aggregate, không thay desired state và
không bypass CDC. Kafka publish/DLQ/CP terminal settlement luôn xong trước consumer
offset/dispatcher checkpoint tương ứng.

Command dùng outer platform envelope hiện có và inner contract mới:

| Layer | Exact contract |
| --- | --- |
| `JobCommandV1` | `job_id = command_event_id`, `job_version=1`, `attempt=0..4`, `job_topic=managed_service.instance.execute`, `source_domain=MANAGED_SERVICE`, `resource_id=instance_id`, `target_zone_id`, payload schema/version, traceparent/tracestate. Kafka key là UUID `instance_id`, không phải `job_id`. |
| `ManagedServiceCommandV1` payload | `command_event_id` (phải bằng outer `job_id`), `operation_id`, `instance_id`, `owner_type`, `owner_id`, `workspace_id`, immutable `instance_code`, `operation_kind`, `generation`, `attempt`, `instance_revision_id`, `blueprint_revision_id`, canonical `template_yaml`/component contract, `bundle_hash`, `contract_hash`, `input_hash`, `desired_spec_hash`, opaque Zone-bound `parameter_envelope` + digest, schema version và timestamp. |
| `JobExecutionResultProto` | outer `job_id = source_command_event_id`, same job topic/domain/attempt/Zone route and trace context; DP publishes result with `instance_id` copied from verified source command as Kafka key. |
| `ManagedServiceResultV1` payload | unique `result_event_id`, `source_command_event_id`, `operation_id`, `instance_id`, generation, attempt, target revision, bundle/contract/desired hash, Zone, outcome `SUCCEEDED|RETRYABLE_FAILURE|TERMINAL_FAILURE`, taxonomy code, bounded sanitized message, safe observed snapshot/version và schema version. |

Static `template_yaml` là internal SRE artifact, không public catalog response và không
chứa literal Kubernetes Secret credential; nó phải hash-match revision trước render.
`parameter_envelope` vẫn là ciphertext nested field duy nhất chứa customer input.
JO/DP reject/quarantine malformed protobuf, unsupported version, source/domain route
mismatch, cross-Zone command, hash mismatch hoặc oversize record. Stale result fence
mismatch chỉ ignore + metric/audit; malformed command/result đi `aurora.jobs.dlq.v1`
rồi CP settle terminal khi source operation còn current, không retry vô hạn.

Không có Managed Service NATS subject, `managed_service_runtime` envelope, Redis
Pub/Sub producer hay `runtime:<user_id>` channel. Customer logs/metrics là Zone-local
Victoria read path tại S14. Topic ACL tách JO producer, Dataplane đúng Zone consumer,
Dataplane result producer và JO result consumer; Zone A không đọc command của Zone B.

Gate: byte-compatible Proto evolution, route registration, crash sau Kafka publish
trước checkpoint, delayed retry restart, duplicate/out-of-order command-result,
DLQ and Zone ACL tests; không có Managed Service runtime event đi tới Console.

## 16. S11 — Dataplane render/apply và Zone telemetry identity

**Status: CLOSED**

Dataplane validate outer/inner Kafka Protobuf, schema, route Zone, source event,
revision/bundle/contract/desired hash, parameter-envelope digest và payload size trước render;
execution fence là `instance_id + operation_id + generation`; `attempt` chỉ correlate
source command/result và không được làm yếu dedupe side effect. Dataplane resolve
exact command bundle, không fetch latest và không nhận owner/permission override. Nó
chỉ materialize `parameter_envelope` in RAM của một execution under the Zone-local
runtime secret contract, render
YAML AST, gửi materialized config tới Kubernetes API rồi quên plaintext; không
log/report/DB/Redis/NATS/Zone KV/disk raw map, rendered manifest, database name hay
password.

Zone NATS JetStream KV chỉ được dùng làm local lease/fence coordination để giảm
concurrent execution; nó không giữ desired state, decrypted input, result hay
completion source of truth. Lease loss/pod restart không được report `SUCCEEDED`;
redelivery/reconcile dùng command fence, protected ownership marker và Kubernetes
actual objects làm idempotency boundary. At-least-once không biến Kubernetes side
effect thành exactly-once.

Executor force namespace/protected metadata, dry-run/discovery validate rồi server-side
apply static components theo revision contract. Field manager cố định; object foreign
marker bị terminal reject, object của cùng instance/hash có thể converge. Create/update
chỉ `SUCCEEDED` sau mọi component đạt static readiness rule. Delete apply thứ tự
`workload → wait finalizer/gone → service → network-policy`; chỉ success khi toàn
graph không còn. Partial apply, API outage, Pending/PVC/capacity và readiness deadline
trả taxonomy đã chốt; không blind rollback và không publish success chỉ vì API apply
đã ACK.

V1 không có `ManagedServiceRuntime` struct/protobuf/NATS subject/JO runtime consumer.
DP chỉ emit bounded trace, fixed-cardinality metric và Kafka terminal result; customer
Console rehydrate operation qua durable notification/API, không thấy in-memory progress.

Logs/metrics customer-facing không thuộc execution RAM. Zone OTel Collector đọc
protected Kubernetes metadata và overwrite telemetry attributes owner/workspace/
instance/component trước khi metrics vào Zone VictoriaMetrics và logs vào Zone
VictoriaLogs. Console và Controlplane không query Zone Victoria trực tiếp; Rust
`zone-observability-stream` đúng Zone là read-only adapter sau hai Zone edge, nhận
trusted scope đã inject và tự thêm filter instance/workspace/Zone/component cho
panel/query allow-list. Không nhận raw PromQL/LogsQL từ browser. Đây là observed,
eventual read path, không quyết định desired state, operation result hay authorization
ownership.

Gate: pilot apply sandbox; duplicate không tạo resource thứ hai; malformed/foreign-
Zone command reject trước Kubernetes side effect; DP restart/lease loss/redelivery
không tạo success giả; RAM/trace không chứa secret/raw provider payload và không
mutate business state.

## 17. S12 — Result, status và reconcile

**Status: CLOSED**

Controlplane sở hữu desired/business lifecycle; Dataplane báo observed/execution
evidence; JO giữ relay/checkpoint, không tự quyết định business outcome; Console
hiển thị desired và observed khi lệch nhau. Durable observed state chỉ là
`UNKNOWN|PROGRESSING|READY|DEGRADED`; nó không thay instance lifecycle
`PROVISIONING|ACTIVE|DELETING` và không là source để auth/billing/quota.

Result inbox settlement chạy trong một CP transaction/CTE theo thứ tự: insert unique
`result_event_id` → lock instance/operation → verify source event, Zone, operation,
generation, attempt, target revision và all hashes → write bounded observed snapshot
with monotonic observed version → chọn finalization hoặc retry outbox. `SUCCEEDED`
promote đúng pending revision; update terminal failure clear pending và giữ active
revision cũ; create terminal failure giữ `PROVISIONING`; delete terminal failure giữ
`DELETING`. Delete chỉ hard-delete instance/revision sau result xác nhận mọi component
trong graph đã gone/finalizer complete. Không xóa workspace namespace vì namespace có
thể chứa instance khác.

Duplicate result event converge qua inbox unique key. Result của command cũ, attempt
cũ hoặc generation cũ không ghi đè observed/desired mới; chỉ metric/audit. Result
malformed/cross-Zone/hash mismatch đi quarantine taxonomy, không được coi là stale
success. Result retryable làm operation `RETRYING` và create exact retry outbox nếu
còn attempt; terminal/error và timeline update chỉ xảy ra sau transaction commit.

Reconcile trigger khi outbox/result missing, DP/Zone reconnect, observed mismatch,
bounded periodic scan hoặc SRE manual retry. Nó chạy per-Zone/per-instance lease,
small page, jitter và work/CPU/API budget. Reconcile có thể redispatch exact current
operation/generation, check Kubernetes protected ownership/readiness và update observed
snapshot, nhưng không tự tạo revision, không promote/rollback desired state và không
resurrect deleted instance. Chỉ result/fence của pending operation hiện tại được phép
settle lifecycle. Zone/Kafka/Kubernetes unavailable dừng batch theo backpressure và
để next bounded cycle retry; không storm/poll dày trên mọi replica.

Gate: result projection replayable; restart không mất desired state; stale/duplicate
result không ghi đè observed mới; duplicate reconcile không tạo side effect; delete
partial/finalizer, missing-result and delayed-retry scenarios có integration/E2E
evidence.

## 18. S13 — Security, identity và secret

**Status: CLOSED**

ACR/Envoy verify session và inject context nội bộ.
Controlplane đối chiếu user/workspace/owner/Zone bằng durable state.
Dataplane chỉ tin routing từ transport đã verify nhưng vẫn validate Zone.

SRE catalog permission tách customer instance permission.
Customer chỉ thấy revision/instance trong ownership scope.
Publish, retire, delete và retry critical phải có policy/audit.

Customer observability dùng đúng hai Zone edge generic, không tạo gateway thứ ba:
Zone Control Edge xử lý Central assertion và preparation của short-lived scoped
ticket; Zone Public Edge chỉ expose stream sau một lần ext-authz ở lúc mở connection.
Zone Control Authorizer verify assertion/audience/Zone/expiry và Zone access projection,
rồi inject scope `owner_id + workspace_id + zone_id + instance_id + component policy +
panel policy`; Public Edge strip ticket và toàn bộ header scope từ browser trước khi
forward tới `zone-observability-stream`. Rust service không nhận Central session,
không tự authorization, không mint identity và không có Zone KV credential.

Ticket là read-only scope proof TTL đúng 5 phút, audience
`zone-public-edge-gateway`, capability `observability.read` và stream lifetime tối đa
5 phút. Nó bind `jti`, actor, auth generation, owner/workspace/Zone/instance,
component/panel allow-list, policy revision, method/path và expiry. Replay không tạo
side effect business nhưng có thể tạo một connection thứ hai, nên Public
Edge/Authorizer enforce concurrent connection, request-rate và stream-duration quota;
không tuyên bố exactly-once hoặc single-use distributed nếu chưa có CAS durable riêng.
Client không gửi raw Victoria query, owner/workspace/Zone/instance, label selector hay
namespace.

SRE state transition và mọi blueprint/revision mutation/delete là critical action.
Route phải đặt chính xác dưới `/admin/critical/managed-services/catalog/*` để ACR bắt
path và xử lý Ed25519/step-up/nonce proof trước khi forward; không có normal mirror.
`in_use` là predicate durable của CTE trên catalog dependency/default pointer,
personal/tenant revision pin và non-terminal operation, không tin UI cache. Critical
proof marker/challenge ID thiếu tại CP phải fail-close. Critical proof không cho phép
hard-delete published revision đang pin; action hợp lệ là retire hoặc taxonomy
`SRE_CATALOG_RECORD_PINNED`.

`template_yaml` là flat immutable artifact của SRE; `input_schema` là contract form
riêng của cùng revision. CP dùng schema để validate payload ở HTTP boundary và chỉ
parse YAML draft để kiểm tra cú pháp, static metadata/reserved policy, component
contract và literal Secret ban. CP không enumerate `!aurora/param`, không biết template
dùng input key nào và không suy luận một value là secret hay non-secret. Sau
canonicalization, full map trở thành opaque envelope bound
với trusted `zone_id + instance_id + operation_id + generation + revision + bundle_hash`.
Chỉ envelope/digest đi vào instance revision, outbox, JO và Kafka.

`zone_id` là routing/binding context từ Envoy, không là public-key record và không có
keyset, attestation, rotation hay CP→Zone metadata projection riêng cho Managed
Service. Repository CTE khóa workspace rồi chứng minh persisted workspace Zone khớp
trusted Zone trước durable write. Khi Zone-local encryption runtime được chốt, private
material vẫn chỉ sống trong Kubernetes Secret của Zone; CP/JO không nhận nó. Dataplane
không cache map/database name/password sau workflow; Kubernetes API là durable runtime
materialization boundary. Customer value render vào `v1/Secret` chỉ sống ở Kubernetes
Secret encryption-at-rest; value render vào CRD/spec/ConfigMap cũng chỉ được DP gửi cho
Kubernetes rồi bỏ khỏi memory khi execution kết thúc.

SRE quyết định bằng YAML một value customer đi vào `v1/Secret`, CRD hay config thông
thường; platform không có nhánh parameter `secret`, `generated` hay `literal`. Giá trị
không dùng `!aurora/param` là literal YAML; operator tự generate credential cũng là
operator-specific YAML/CRD behavior. Tuy vậy literal `v1/Secret.data`/`stringData`
phải bị reject lúc publish để catalog revision không trở thành kho plaintext
credential. Credential phải đến từ customer parameter hoặc do operator Zone tạo; CP,
JO và Console không có API đọc Kubernetes Secret. Kubernetes Secret V1 phải có
encryption-at-rest, namespace RBAC tối thiểu và không cấp quyền `list/watch` Secret
rộng cho executor/operator.

Template tag thiếu value, typed node incompatible hoặc render YAML lỗi là
`SRE_TEMPLATE_INPUT_MISMATCH`: lỗi terminal của revision/configuration, không retry
vô hạn và phải được sửa bằng published revision mới. Extra/unused schema key không
là CP business validation; đó là debt của SRE schema/template. Runtime/result/log/
notification không mang raw parameter, rendered manifest hoặc Secret value.

Zone Vault không thuộc V1. Khi được triển khai, đó là Vault riêng từng Zone làm
secret source of truth; Vault CSI/Secrets Operator hoặc Zone adapter materialize
Secret cho workload. CP/JO vẫn không có Zone Vault credential và migration từ
operator-created Kubernetes Secret sang Vault-managed Secret là workflow riêng,
không dual-write âm thầm.

Gate: threat model cover template injection, cross-Zone command, ownership
spoofing, replay và secret exfiltration; negative/security tests pass.

## 19. S14 — Observability và audit

**Status: DESIGN CLOSED (contract only; chưa có Rust project, route hay deployment)**

Request, command/result dùng operation ID, event ID, aggregate ID và trace context.

Metric: publish latency, accepted/failed, outbox age, JO lag, Kafka retry/DLQ, Zone command latency, render/apply latency/error, reconcile, stale result, fencing rejection, backpressure.

Customer Logs/Metrics là Zone-local read path, tách tuyệt đối khỏi NATS runtime:

```text
Managed Service pods
  → Zone OTel Collector (verified Kubernetes metadata enrichment)
  → VictoriaMetrics / VictoriaLogs đúng Zone
  → zone-observability-stream (Rust, read-only)
  → Zone Public Edge
  → Browser
```

Browser lấy scoped ticket TTL 5 phút qua assertion path của Zone Control Edge, sau đó
mở SSE/read stream tối đa 5 phút tới Zone Public Edge. Public Edge chỉ ext-authz một
lần khi mở connection; không authorize từng byte và không retry upstream stream tự
động. Auth/workspace/Zone/instance/policy scope đổi phải close stream; browser lấy
ticket mới trước reconnect.
`zone-observability-stream` nhận only trusted injected scope và request bounded
`panel_id + component selector allow-list + time range/cursor`; service tự dựng
Victoria query. Raw PromQL, LogsQL, arbitrary metric name/label, namespace, owner,
workspace, Zone và instance ID từ client đều reject. Console/Controlplane không có
Victoria credential và không query Zone Victoria trực tiếp.

V1 giữ metrics 7 ngày và logs 3 ngày trong customer Victoria plane của Zone. Metrics
stream là sampled/eventual và coalesce khi downstream chậm; logs tail không cam kết
replay/exact ordering khi reconnect. Mỗi connection có bounded byte/range/
point/log-line/in-flight budget; client chậm bị close để tự reconnect, không có queue
RAM vô hạn hoặc retry storm. Victoria/adapter/Public Edge outage trả retryable error
và không trả `READY`/`SUCCESS` giả. `SUCCESS`/`FAILED`, desired state, reconcile và
timeline vẫn chỉ do Kafka result → JO → Controlplane/Notification durable path quyết
định.

`zone-observability-stream` là root Rust subproject/Deployment riêng đúng Zone, có
read-only network identity tới VictoriaMetrics/VictoriaLogs và không có access tới
Kafka, NATS, PostgreSQL, Shared/Auth Redis, Zone KV, Kubernetes API hoặc Vault.
Service/edge/authorizer scale độc lập theo connection/query load, dùng bounded
shutdown drain; disconnect client phải cancel Victoria request/tail. Không cache hoặc
persist raw log/metric/customer payload.

`aurora_workspace_id`, `aurora_owner_id` và `aurora_managed_service_instance_id` là
high-cardinality telemetry dimensions chỉ được phép trong customer Victoria read
plane, với retention/series budget riêng theo Zone. Chúng không được gắn vào metric
health của platform/edge/JO/Dataplane, không đi vào alert aggregate toàn platform và
panel không được fan-out query qua nhiều instance ngoài verified scope.

Audit ghi actor, operation, revision, hash, Zone, outcome, timestamp. Observability
access chỉ audit stream-open/deny/close và bounded scope/panel/outcome, không audit
mỗi sample/log line. Không ghi secret, raw parameter, rendered manifest, raw query
hoặc raw log vào audit.

Gate: trace được một operation từ HTTP tới Dataplane result.
Alert phân biệt outage, poison message, render error, policy reject và Victoria/
stream backpressure; test phải chứng minh forged scope/raw query bị deny, scope change
đóng stream, slow client không làm cạn RAM, Victoria outage không làm terminal state
đổi và không có Managed Service NATS path tới Browser.

## 20. S15 — Failure matrix và drill

**Status: RELEASE GATE — pending implementation evidence**

PostgreSQL down: mutation fail-close, không publish trước commit.
JO down: desired state durable, outbox age tăng, request không mất.
Kafka down: checkpoint không advance, retry bounded, alert lag.
Dataplane down: command giữ Kafka, không success giả.
Kubernetes down: không success giả; result retryable hoặc reconcile/redelivery sau
backoff bền vững.
Zone-bound parameter envelope invalid: fail-close `ZONE_PARAMETER_ENVELOPE_INVALID`,
không plaintext fallback.
Victoria/Public Edge down: chỉ stream retryable/stale, không đổi operation/timeline.

Duplicate HTTP key trả operation gốc.
Duplicate Kafka command không tạo resource thứ hai.
Out-of-order result không ghi đè observed mới.
Stale worker bị fencing reject.
Retired revision vẫn phục vụ instance đã pin theo policy.

Drill: kill CP sau DB commit; kill JO sau Kafka publish; redeliver command; restart
JO trước `available_at`; delay Zone; sai hash/Zone; unknown parameter; partial apply;
malformed Zone-bound envelope; đầy retry queue/DLQ; delete finalizer treo;
Victoria/edge outage và forged scope ticket.

Gate: mỗi drill có expected state, evidence, rollback, owner và không cần sửa DB thủ công để phục hồi normal flow.

## 21. S16 — Test staging environment

**Status: RELEASE GATE — pending implementation evidence**

Thành phần: CP với PostgreSQL migration thật; JO test worker/CDC fixture; Kafka test cluster có ACL/topic; Dataplane sandbox; Kubernetes kind/k3d hoặc cluster staging; Envoy/ACR fixture; observability collector; catalog/revision/instance fixtures.

Unit cover pure rule/taxonomy; integration cover CTE, constraint, transaction,
outbox và authorization.
Transport cover protobuf compatibility, inner/outer event fence, size, schema version,
Zone binding, `instance_id` ordering, delayed retry và DLQ.
Golden render cover deterministic manifest, unknown parameter, namespace/ownership
injection, component order/readiness và foreign object rejection.
E2E cover request tới observed result.
Chaos cover crash, duplicate, lag, outage, key rotation, retry timer và reconcile.

Gate: staging reproduce public path và durable command path; evidence lưu cùng
revision contract.

## 22. S17 — Pilot rollout

**Status: RELEASE GATE — pilot service và staging Zone được chọn lúc release**

Pilot chọn service có image/bundle ổn định, ít object, parameter rõ, không
cross-Zone, cleanup dễ và rollback đơn giản.

Không chọn workload có migration phức tạp hoặc secret topology chưa ổn định.

```text
catalog hidden → internal SRE → one staging Zone
  → limited customer scope → observe → expand Zone → public
```

Visibility và customer scope do backend quyết định.

Rollback không xóa revision instance đang pin.
Ngừng instance mới trước khi dừng reconcile instance cũ.
Rollback code giữ command/result schema tương thích.

Gate: pilot chạy create, update, delete, retry, reconcile; không có P0/P1
security, data-loss hoặc duplicate-side-effect bug chưa xử lý.

## 23. S18 — Task extraction gate sau design close

**Status: READY — S00–S14 đã đóng; S15–S17 chờ implementation/release evidence**

S00–S14 đã trả lời và không còn quyết định thiết kế mở:

* Ai sở hữu record và transition?
* Revision publish/retire/pin thế nào?
* Input nào validate ở handler và nào tại Dataplane?
* Outbox commit, Kafka ordering, JO checkpoint ra sao?
* Kafka key, retry, DLQ, Zone ACL là gì?
* Duplicate command và stale result xử lý thế nào?
* Zone-local envelope materialization sẽ lấy runtime secret ở đâu khi implementation bắt đầu?
* Cluster-scoped SRE platform artifact sẽ có lifecycle riêng thế nào?
* Observed status nào evidence, status nào business state?
* Reconcile có fencing/bounded budget chưa?
* Failure drill nào chứng minh recovery?
* God View nào cần tạo/cập nhật?

S15–S17 không chặn việc tạo phase/task; chúng chặn claim rằng module đã release.

### God View action list

Các cross-cutting God View đã được cập nhật cùng contract này: Kafka transport, JO
runtime workers, Notification timeline/runtime boundary, Zone telemetry, Zone public
edge và Zone lifecycle key metadata. Task đầu tiên trước workflow code phải tạo
`god_view/managedservice/managed_service_lifecycle_god_view.md` làm end-to-end SoT
cho catalog → desired state → JO/Kafka → DP/Kubernetes → result/reconcile. Nó chỉ
phản chiếu contract đã đóng ở đây; không được dùng để mở lại topology hay business
rule đã chốt.

### Staging packet

1. IDEA/STAGING đã review.
2. Domain glossary và lifecycle diagram.
3. Ownership/connection matrix.
4. Catalog/blueprint schema.
5. API/transport proposal.
6. Persistence/outbox contract.
7. Render/apply policy.
8. Status/reconcile contract.
9. Security threat model.
10. Dashboard/alert proposal.
11. Failure drill matrix.
12. Staging test evidence.
13. Pilot runbook/rollback.
14. God View change list.
15. Release evidence matrix và pilot selection record.

## 24. Tách staging thành Phase và Task

Phase chỉ chứa nhóm stage đã đóng contract.

Task phải có stage reference, owner, input artifact, code/data boundary,
dependency, acceptance test, rollback/failure semantics và observability.

Không tạo task “implement module” chung chung.
Không trộn catalog workflow với customer provisioning workflow.
Không gộp CP, JO, DP thay đổi nếu chưa có transport contract.
Không gọi task hoàn tất khi chỉ pass compile mà chưa có durability/failure evidence.

Grouping sơ bộ:

```text
Phase A — contract và catalog foundation
Phase B — blueprint/revision publication
Phase C — customer desired state
Phase D — durable dispatch qua JO/Kafka
Phase E — Dataplane render/apply pilot
Phase F — result/reconcile/observability
Phase G — security, failure drill và rollout
```

Grouping trên là khung extraction; task thật sinh từ staging packet mà không cần đoán
business rule. Mỗi task vẫn phải khai báo failure/rollback/evidence riêng.

## 25. Release-only selection sau design close

Không còn quyết định product/topology/transport mở trước phase. Những quyết định
đã khóa là YAML AST subset, PostgreSQL canonical bundle, safe output-only V1,
Kubernetes Secret V1/Zone Vault V2, logical WAL/CDC primary dispatch, durable
observed snapshot, một SRE principal không async approval, result/retry taxonomy,
trusted Zone binding và public observability stream.

Chỉ còn chọn **pilot service** và **staging Zone** gần release, dựa trên catalog đã
implement, capacity và failure-drill evidence. Đây là rollout scope, không thay đổi
contract hay chặn task extraction.

## 26. Staging done definition

Design staging hoàn tất khi:

* S00–S14 có owner, output và contract closed;
* không có connection trái topology;
* mọi field có owner và sensitivity;
* state machine xử lý retry, duplicate, stale result và delete;
* catalog/revision/render contract có compatibility rule;
* retry budget, envelope, result inbox, Zone binding và error taxonomy đã viết rõ;
* security/observability design review và God View change list nhất quán;
* phase/task tách được mà không cần đoán business rule.

Release staging hoàn tất khi S15–S17 có staging E2E request-to-result, failure drill
chứng minh desired state không mất, security/observability evidence đã ký và pilot
runbook/rollback được thực thi. Chỉ lúc đó mới claim vertical slice có thể ship.
