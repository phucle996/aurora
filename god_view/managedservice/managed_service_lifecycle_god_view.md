# Managed Service Lifecycle — God View

> [!IMPORTANT]
> Đây là Source of Truth end-to-end cho Managed Service Platform V1. Control
> header bên dưới ghi rõ phase nào đã ship; phần workflow tương lai không phải
> bằng chứng Kafka route hay Kubernetes executor đã được enable.
>
> Khi code, schema hay deployment mâu thuẫn với tài liệu này, dừng thay đổi và
> sửa God View/contract trước. Không suy luận runtime behavior từ module shell.

## 0. Control header

| Thuộc tính | Contract |
| --- | --- |
| Trạng thái | P00 frozen; P01 foundation, P02 SRE catalog/admin và P03 customer catalog/form shipped; customer mutation/runtime admission remains disabled |
| Business SoT | Controlplane PostgreSQL: system catalog, personal/tenant desired state, immutable revision, operation, inbox/fence và outbox |
| Durable transport | PostgreSQL outbox/WAL → Job Orchestrator → Kafka → Dataplane Zone → Kafka result → JO/Controlplane settle |
| Runtime executor | Dataplane đúng Zone gọi Kubernetes API; Controlplane không có Kubernetes credential/client |
| Delivery | At-least-once; ordering chỉ theo `instance_id`; external fence là `instance_id + operation_id + generation` |
| Customer completion | Durable Controlplane operation/API và một Notification timeline row; không dùng NATS runtime hay apply ACK |
| Telemetry | Zone OTel → VictoriaMetrics/VictoriaLogs → `zone-observability-stream` → Zone Public Edge; read-only, eventual |
| Canonical inner protobuf | `contracts/proto/managed_service.proto` được freeze ở P00 và generate từ root này tại P01 |
| Related SoT | [Kafka transport](../platform/kafka_platform_transport_god_view.md), [Notification timeline](../notification/user_timeline_god_view.md), [Dataplane telemetry](../dataplane/telemetry_god_view.md), [Zone Public Edge](../platform/zone_edge_gateway_god_view.md) |

## 1. Scope và boundary không được vượt qua

Managed Service cho phép SRE publish catalog service và immutable blueprint;
customer tạo desired state trong workspace hiện tại; Dataplane render graph Kubernetes
đúng Zone. Một `ManagedServiceInstance` là graph nhiều component, không phải một raw
Kubernetes object do customer gửi.

V1 **không** bao gồm arbitrary Helm/raw YAML runner, Kubernetes dashboard, billing/
quota, customer-selected Zone, secret Vault Zone, arbitrary Secret read-back, generic
runtime stream, NATS runtime protocol hoặc gateway thứ ba. SRE bootstrap cluster-scoped
operator/CRD là platform concern ngoài customer instance graph.

Boundary bắt buộc:

* Browser/Admin UI chỉ đi Envoy/ACR và public API/edge stream; không kết nối database,
  broker, Vault hay Kubernetes.
* Envoy/ACR strip internal header do client gửi, verify session/critical proof và inject
  trusted context. Controlplane không tự diễn giải SRE role/level tại admin catalog
  route; trusted actor chỉ dùng audit và thiếu context phải fail-close.
* Controlplane sở hữu catalog, owner/workspace/Zone binding, desired state và durable
  operation; nó không render YAML, decrypt parameter, publish Kafka trực tiếp hoặc gọi
  Kubernetes.
* JO là durable WAL/CDC-to-Kafka bridge và result relay. JO không tạo business
  aggregate, không có Zone KV credential và không tự quyết lifecycle final state.
* Dataplane chỉ thực thi command đã route đúng Zone. Nó không tin client identity,
  owner, workspace hay permission và không có CP PostgreSQL/Auth-State/Shared L2 Redis
  credential.

## 2. Canonical vocabulary, ownership và identity

| Object | Owner | Ý nghĩa immutable/mutable |
| --- | --- | --- |
| `ServiceCategory` | System/SRE catalog | Nhóm hiển thị catalog; không có owner/workspace/Zone |
| `ServiceDefinition` | System/SRE catalog | Loại managed service, ví dụ Apache Kafka |
| `ServiceVersion` | System/SRE catalog | Phiên bản application SRE hỗ trợ |
| `ServiceBlueprint` | System/SRE catalog | Blueprint line của một version |
| `BlueprintRevision` | System/SRE catalog | YAML, input/UI/output/component contract immutable sau publish |
| `ManagedServiceInstance` | Personal hoặc tenant workspace | Desired lifecycle customer-facing; giữ active/pending revision head |
| `InstanceRevision` | Cùng ownership với instance | Snapshot config ciphertext + pinned blueprint artifact; immutable |
| `ManagedServiceOperation` | Cùng ownership với instance | Một execution CREATE/UPDATE/DELETE với fence/generation riêng |
| Outbox/inbox/fence | Controlplane module transport/evidence | Không là aggregate owner; retention kéo dài hơn Kafka replay window |

Catalog table là system table, không có prefix `sre_` và không có `owner_id`,
`owner_type`, `workspace_id` hay `zone_id`. SRE actor chỉ là audit provenance như
`published_by`/`retired_by`.

Customer aggregate dùng hai physical branch tách biệt. Personal branch snapshot
`user_id`; tenant branch snapshot `tenant_id`; không có bảng customer polymorphic chung
hay generic repository chọn owner type. Mỗi workflow vẫn là một handler method → một
service method → một repository method trong branch tương ứng.

`ManagedServiceInstance.code` là lowercase DNS-label immutable, tối đa 35 ký tự, và là
Kubernetes workload-name base. `name` chỉ là display metadata, được phép đổi mà không
đổi render identity. Create uniqueness là `(workspace_id, code)` kết hợp canonical
create intent: trùng intent trả instance/CREATE operation hiện có; intent khác trả
stable conflict. Không dùng HTTP `Idempotency-Key`.

Namespace do Dataplane tạo, không nhận từ client/template:

```text
aur-ms-{p|t}-{base32lower(owner_uuid_bytes || workspace_uuid_bytes)}
```

DP inject protected owner/workspace/instance/revision/render-hash annotations và internal
instance/component labels. Owner/workspace không là traffic label V1; customer parameter,
template và workload không được ghi đè metadata protected.

## 3. Lifecycle state machine

`ManagedServiceInstance` là resource lâu dài; `ManagedServiceOperation` là một lần
thực thi thay đổi resource. Observed state là snapshot riêng, không thay lifecycle.

| Aggregate | States | Invariant |
| --- | --- | --- |
| Instance lifecycle | `PROVISIONING`, `ACTIVE`, `DELETING` | Không có `DELETE_FAILED`; mỗi instance có tối đa một operation non-terminal |
| Operation status | `ACCEPTED`, `DISPATCHING`, `RUNNING`, `RETRYING`, `SUCCEEDED`, `TERMINAL_FAILED` | `attempt` chỉ thuộc command delivery/correlation; không là external side-effect fence |
| Observed state | `UNKNOWN`, `PROGRESSING`, `READY`, `DEGRADED` | Không dùng cho authorization, billing hoặc terminal lifecycle decision độc lập |

```mermaid
stateDiagram-v2
    [*] --> PROVISIONING: CREATE desired state + outbox committed
    PROVISIONING --> ACTIVE: CREATE result SUCCEEDED / pending revision promoted
    PROVISIONING --> PROVISIONING: CREATE retry or terminal failure
    ACTIVE --> ACTIVE: UPDATE desired state / old active revision remains
    ACTIVE --> DELETING: DELETE desired state + outbox committed
    DELETING --> [*]: DELETE result confirms entire graph gone
    DELETING --> DELETING: retry or terminal failure
```

```mermaid
stateDiagram-v2
    [*] --> ACCEPTED
    ACCEPTED --> DISPATCHING: JO admits durable outbox command
    DISPATCHING --> RUNNING: Kafka ACK persisted and dispatch status projected
    RUNNING --> SUCCEEDED: current fenced result settles
    RUNNING --> RETRYING: retryable result with attempt remaining
    RETRYING --> DISPATCHING: due outbox command is durably published
    RUNNING --> TERMINAL_FAILED: terminal result or retry budget exhausted
```

Create terminal failure giữ instance `PROVISIONING`. Update terminal failure clear
pending revision, giữ active revision cũ và lưu target revision làm evidence. Delete
terminal failure giữ instance `DELETING`; không rollback mù về `ACTIVE`. Manual retry
tạo operation/generation mới, không sửa operation terminal cũ. Automatic retry giữ
operation/generation/revision cũ, chỉ tạo command event mới và tăng attempt.

## 4. Authoritative topology và durable completion path

```mermaid
flowchart LR
    UI[Customer Console] --> EDGE[Envoy + ACR]
    ADMIN[Admin UI] --> EDGE
    EDGE --> CP[Controlplane Managed Service]
    CP --> PG[(Controlplane PostgreSQL)]
    PG -->|logical WAL / module outbox| JO[Job Orchestrator]
    JO -->|JobCommandV1 / Kafka| KC[(Zone command topic)]
    KC --> DP[Dataplane exact Zone]
    DP --> K8S[Kubernetes API / customer graph]
    DP -->|JobExecutionResultProto / Kafka| KR[(Result topic)]
    KR --> JO
    JO -->|current result settle| CP
    JO -->|stable timeline XADD| NS[Notification Service]
    NS --> SCY[(Scylla timeline/inbox)]
    NS --> CF[Centrifugo notification]
    OTEL[Managed service pods] --> VC[Zone OTel + Victoria]
    VC --> OBS[zone-observability-stream]
    OBS --> ZPE[Zone Public Edge]
    ZPE --> UI
```

Kafka is durable transport, not the business database. PostgreSQL aggregate mutation
and outbox insert commit in one transaction before JO sees WAL. JO only advances LSN
after Kafka `acks=all`; a crash after broker ACK before LSN checkpoint intentionally
redelivers an idempotent command.

Managed Service V1 has no NATS subject, no Redis Pub/Sub runtime envelope and no
`runtime:<user_id>` channel. Terminal customer lifecycle is not inferred from
Kubernetes API ACK, pod RAM, OTel, Victoria, NATS or Centrifugo; it is settled only by
the current durable result/inbox transaction.

## 5. Admin catalog lifecycle

Admin catalog routes are registered under:

```text
/admin/managed-services/catalog/*
/admin/critical/managed-services/catalog/*
```

Normal catalog route is gated by Envoy/ACR admin policy and only carries safe reads plus
category/definition/version metadata create/update. Category/definition/version state
transitions and every blueprint/draft/validate/publish/retire/delete operation exist
only below `/critical/`; there is no normal mirror. ACR verifies/consumes a proof bound
to method, path and body. Controlplane only checks the injected proof marker/challenge
ID and fails closed if absent; it does not parse proof cryptography or re-run RBAC.

Catalog draft can change. Publish atomically validates artifact/schema/UI contract,
canonicalizes and hashes it, writes audit provenance and switches only the default
pointer. Published `BlueprintRevision` is immutable. Retire is allowed only under
durable predicate; critical proof does not bypass a pinned published revision, FK guard
or in-use operation. SRE template owns the relationship between input schema key and
`!aurora/param` tag; Controlplane may validate allowed tag location but must not
enumerate parameter keys or create a schema-to-tag binding table.

HTTP handlers never generate catalog, draft or audit-event UUIDs. They only parse
client/trusted transport input and map it to a workflow entity with system-generated
fields left empty. The corresponding service assigns every missing resource/audit UUID
before its single repository call and preserves any already assigned UUID so internal
redelivery keeps the same identity. The repository then commits catalog mutation and
audit provenance in its atomic CTE; it does not receive transport-generated audit IDs.

### 5.1 Customer catalog discovery and form contract

P03 exposes read-only, separately implemented branches:

```text
GET /api/v1/personal/managed-services/catalog
GET /api/v1/personal/managed-services/catalog/versions/:version_id
GET /api/v1/tenant/managed-services/catalog
GET /api/v1/tenant/managed-services/catalog/versions/:version_id
```

Every route requires the five-level `managed-service:catalog:read` permission.
Owner/workspace/Zone are never accepted from path, query or body. The handler consumes
typed trusted context; one personal or tenant repository statement verifies the
durable workspace/Zone binding and returns only active category/definition/blueprint,
`AVAILABLE` version and current `PUBLISHED` revision whose validation receipt still
matches its row/bundle/contract hash. Personal and tenant SQL are physically separate.
All four routes use the shared `pkg/apires` envelope: success is `{data,message}`
and failures are `{error,message}` with stable workflow taxonomy. Managed Service
handlers do not write `c.JSON` directly.

`zone_selector` V1 is exactly `{"mode":"all"}` or
`{"mode":"allow_list","zone_ids":[...]}`. `capability_requirement` V1 is exactly
`{"all_of":[...]}` using the bounded `hierarchy.zone_service_type` vocabulary. Catalog
eligibility reads `zone_services.desired_state=true`; operational `actual_state`,
health and capacity do not become catalog SoT. Selector/capability internals, YAML,
component contract and audit provenance never leave the customer API.
The `managed_service` capability is the mandatory Zone admission gate for this
module. Every personal/tenant catalog and version-detail query first requires its
Zone row to have `managed_service` enabled; revision-specific `all_of` requirements
are evaluated afterwards. A Zone without it is ineligible even when Kubernetes
itself is available. In V1 this capability is static desired state, not a health
signal and not an input to Zone draining.

Version detail returns the exact revision ID/hash plus `input_schema` and `ui_schema`.
The optional `expected_revision_id` is a read fence: a switched default returns
`CATALOG_STALE`, never an automatic form migration. Platform Form Contract V1 remains
a flat typed map. UI contract uses the finite widget registry `TEXT`, `TEXTAREA`,
`NUMBER`, `SWITCH`, `SELECT`, `RADIO`, `TOKEN_LIST`, `MULTI_SELECT`; unknown or
type-incompatible widgets fail publish validation and fail closed again in Console.

Responses are `private, no-store`; Console memory queries are fenced by auth generation,
personal/tenant mode, Zone, workspace and revision. Parameter draft is React memory
only and becomes unreachable when any fence changes. Catalog list uses a bounded
maximum page of 100 with an opaque keyset cursor; Console fetches subsequent pages
explicitly instead of issuing one unbounded request. P03 has no customer mutation,
outbox, Kafka/NATS/Redis route or Kubernetes side effect. P04 must recheck catalog,
default revision and Zone predicates atomically before accepting desired state.

## 6. Customer create path

```mermaid
sequenceDiagram
    autonumber
    participant C as Customer Console
    participant E as Envoy + ACR
    participant CP as Controlplane
    participant DB as PostgreSQL
    participant JO as Job Orchestrator
    participant K as Kafka
    participant DP as Dataplane Zone
    participant KS as Kubernetes API

    C->>E: create code + form values
    E->>CP: verified owner/workspace/current Zone context
    CP->>CP: handler validates and canonicalizes ingress only
    CP->>DB: scoped CTE: revision/Zone/code predicate + instance/revision/operation/outbox
    DB-->>CP: commit desired state ACCEPTED
    DB-->>JO: logical WAL outbox record
    JO->>K: command key instance_id, acks=all
    K-->>JO: durable ACK
    JO->>CP: scoped dispatch status projection
    K-->>DP: exact Zone command
    DP->>DP: validate transport + fence; materialize opaque envelope only in RAM
    DP->>KS: deterministic graph render/apply/readiness
    DP->>K: terminal result key instance_id
```

The handler validates path/query/body/schema/type/range/size and canonicalizes the flat
parameter map. It derives owner/workspace/Zone from verified context; client does not
send trusted routing/ownership. Service/repository receive normalized workflow entity
and do not parse DTO, repeat HTTP validation or nil-check injected dependencies.

The creation CTE locks in fixed order:

```text
workspace FOR KEY SHARE → instance FOR UPDATE → revision/operation
```

It atomically checks the trusted workspace/Zone binding and current catalog revision,
serializes code intent, persists only the Zone-bound opaque parameter envelope, then
inserts instance + immutable instance revision + CREATE operation + outbox.
`ACCEPTED` means desired state is durable; it never means Kubernetes is ready.

Failure semantics:

* Crash before commit: nothing is accepted or dispatched.
* Crash after commit before JO reads WAL: WAL replay dispatches the durable outbox.
* Crash after Kafka ACK before LSN: duplicate exact command is safe at DP fence.
* Kafka/JO unavailable: desired state remains `ACCEPTED`/`DISPATCHING`; no CP direct
  producer fallback exists.
* DP timeout/Zone outage: result/reconcile/retry process owns recovery; CP does not
  attempt Kubernetes execution.

## 7. Customer update and delete paths

### 7.1 Update

Update requires expected active generation. It creates a new immutable
`InstanceRevision`, target generation and UPDATE operation/outbox in one CTE. Existing
active revision remains serving until the matching current result succeeds. A same
target desired hash returns the non-terminal operation; a different target while one is
running returns conflict. Rename changes only display `name`, creates no Kubernetes
operation and never changes `code`.

Result success promotes exactly the pending revision. Terminal update failure clears
only the pending head. Automatic retry uses the same operation/generation/revision;
manual retry starts a new operation/generation with an explicit current target.

### 7.2 Delete

Delete changes durable desired state to `DELETING`, creates DELETE operation/outbox and
keeps the aggregate until Dataplane confirms every graph component/finalizer is gone.
Repeated delete returns the same pending DELETE operation. DP delete order is workload
→ wait finalizer/gone → service → network policy. It never deletes a shared workspace
namespace.

On terminal delete failure, CP retains `DELETING` + evidence/fence and emits a terminal
timeline update. On success, one CTE writes durable deletion fence then hard-deletes
instance/revision record. Fence/operation/result evidence remains until at least the
maximum command/result/DLQ retention plus safety margin, so stale delivery cannot
resurrect a deleted graph. Code may later be reused by a new instance UUID.

## 8. Transport contract and ordering

Managed Service uses generic platform topics only:

| Direction | Topic | Kafka key | Producer → consumer |
| --- | --- | --- |
| Command | `aurora.jobs.commands.zone.<zone_uuid>.v1` | `instance_id` | JO → Dataplane of that Zone |
| Result | `aurora.jobs.results.v1` | `instance_id` | Dataplane → JO |
| Poison record | `aurora.jobs.dlq.v1` | DLQ `event_id` | DP/JO → SRE tooling |

Outer command is existing `aurora.transport.v1.JobCommandV1`:

```text
job_id                    = command_event_id
job_version               = 1
attempt                   = 0..4
job_topic                 = managed_service.instance.execute
source_domain             = MANAGED_SERVICE
resource_id               = instance_id
target_zone_id            = trusted outbox Zone
payload_schema_version    = 1
payload                   = ManagedServiceCommandV1 bytes
```

Outer result is existing `job_lifecycle.JobExecutionResultProto`:

```text
job_id                    = source_command_event_id
job_topic/source_domain   = exact Managed Service source route
attempt                   = exact source attempt
result_status             = SUCCEEDED or FAILED
result_payload            = ManagedServiceResultV1 bytes
result_payload_schema_version = 1
```

`RETRYABLE_FAILURE` versus `TERMINAL_FAILURE` belongs to the inner result; both map to
outer `FAILED`. V1 emits terminal results only; it does not emit outer `PROCESSING`.
The canonical inner wire registry, fixed field numbers, reserved ranges and P01
generation rule live in [contracts/proto/README.md](../../contracts/proto/README.md).

Producer, broker and consumer enforce a 1,000,000-byte total record/payload ceiling.
Every UUID is 16 raw bytes; schema version, route, Zone, source event, all revision/hash
fences and trace context are validated before side effect. Malformed/oversize/cross-Zone
record is quarantined/DLQ without raw payload. Stale but well-formed result is ignored
with sanitized metric/audit and never overwrites a newer desired/observed state.

## 9. Dataplane execution contract

DP validates outer and inner Protobuf, Zone/topic binding, command/result route, schema
version, command event, revision/bundle/component/input/desired hashes, envelope digest, size and
fence `instance_id + operation_id + generation` before Kubernetes side effect. `attempt`
must equal the outer command but cannot form a new external idempotency key.

The only supported YAML AST tags are exact typed `!aurora/param <name>` and
`!aurora/component <id>`. There is no text interpolation, loop, include or function.
`apiVersion`, `kind`, whole metadata, namespace, protected metadata and generated name
cannot be parameters. Missing typed value, incompatible YAML node or invalid rendered
YAML returns terminal `SRE_TEMPLATE_INPUT_MISMATCH`; no empty fallback or endless retry.

DP materializes the Zone-bound opaque envelope for one execution in memory. It sends the
materialized value to Kubernetes according to SRE static YAML and then forgets it. It
does not write plaintext parameter, rendered manifest, database name/password or Secret
value to CP DB, Redis, NATS, Zone KV, Kafka result, disk, log or notification. Kubernetes
is the durable runtime materialization boundary.

Customer objects must be namespaced. Zone executor ServiceAccount/RBAC plus pinned
revision capability profile limits static `apiVersion`/`kind`; CP does not use a Kind
allow-list. DP discovery/dry-run checks Zone API then uses server-side apply with a
fixed field manager. It may force same-instance protected ownership only; a foreign
marker is terminal `K8S_OWNERSHIP_CONFLICT`. Partial apply has no blind rollback;
reconcile retries the exact desired hash/fence or returns an error taxonomy.

Success requires every component's static readiness rule/deadline, not apply ACK.
V1 default graph applies network policy → service → workload and deletes reverse,
though a published revision may declare a different explicit component order/rule.

## 10. Result settlement, retry, reconciliation và timeline

Dataplane sends one terminal `ManagedServiceResultV1`: unique result event, source
command event, all fences/hashes, outcome taxonomy, bounded sanitized message and only
SRE-declared-safe observed output. It must not include raw parameter, rendered YAML,
Secret, token or provider payload.

Controlplane result settlement is one CTE/transaction:

```text
insert unique result inbox
  → lock scoped instance/operation
  → verify source event + Zone + operation + generation + attempt + revision + hashes
  → write bounded observed snapshot and monotonic version
  → finalise current operation OR create durable delayed retry outbox
```

Only current fence may change lifecycle. Duplicate result converges through inbox unique
keys. A stale attempt/generation/source event is not a terminal failure and cannot
mutate desired/observed state. Malformed result is quarantine/DLQ data, not stale data.

Retry budget is exactly five commands, attempts `0..4`. Bases are `30s`, `2m`, `10m`,
`30m` plus 0–20% jitter; CP persists the final `available_at` in new outbox record.
JO CDC is the primary dispatcher. Its bounded per-module due-retry scan, lease/fence and
work budget recover delayed timer/CDC interruption only; it does not create aggregate
state or become a second relay. Attempt 4 retryable failure becomes terminal.

After durable Kafka ACK, JO emits one `PROCESSING` timeline event. After CP transaction
settles terminal state, JO emits `SUCCESS` or `FAILED`. All events use
`notification_id = UUIDv5(operation_id)`, preserve original creation timestamp and
update the same Scylla row with monotonic `status_version`; attempt/status never creates
another customer timeline item. Notification/Redis stream failure is observable and
retried by that path but never rolls back business settlement.

Reconciler is bounded, jittered and lease-fenced per Zone/instance. It can redispatch
the exact current operation or recheck protected graph/readiness after restart/missing
result/observed mismatch. It cannot create revision, promote/rollback desired state,
resurrect hard-deleted aggregate or poll every resource continuously.

## 11. Error taxonomy and failure disposition

| Taxonomy | Disposition |
| --- | --- |
| `SRE_TEMPLATE_INPUT_MISMATCH` | Terminal; SRE must publish corrected revision/config |
| `K8S_APPLY_REJECTED` | Terminal when static manifest/RBAC/schema invalid |
| `K8S_OWNERSHIP_CONFLICT` | Terminal; never adopt foreign object |
| `ZONE_PARAMETER_ENVELOPE_INVALID` | Terminal for the current operation; a corrected immutable revision is required |
| `K8S_API_UNAVAILABLE` | Retryable within budget |
| `K8S_CAPACITY_PENDING` | Retryable while static readiness/Zone policy permits |
| `K8S_READINESS_DEADLINE_EXCEEDED` | Revision policy determines terminal or retryable outcome |
| malformed schema/route/Zone/hash | Quarantine/DLQ; no side effect and no infinite retry |
| stale result fence | Ignore + sanitized metric/audit; no lifecycle mutation |

DLQ publish must be durable before source Kafka offset/checkpoint advances. DLQ diagnostic
holds error code, source topic/partition/offset, payload length and SHA-256 only; it
never carries raw envelope, template or parameter data.

## 12. Security, encryption and observability

`zone_id` is trusted routing and envelope-binding context, not a Controlplane-managed
public-key lifecycle. The create/update CTE proves the workspace is in that Zone in the
same transaction that writes desired state; the client cannot submit or override a Zone.
The encrypted-envelope implementation is deliberately deferred until the Zone-local
runtime secret contract is introduced. CP/JO never receive Zone private material, and
no keyset, rotation record, attestation or Zone-metadata projection is part of Managed
Service V1.

SRE decides through YAML whether a parameter becomes CRD/spec/ConfigMap/Kubernetes
Secret or an operator-generated value. Platform does not classify input as `secret`,
`generated` or `literal`; literal `v1/Secret.data`/`stringData` is rejected at publish
to stop catalog becoming a plaintext secret store.

Customer observability is strictly a Zone-local read path:

```text
Managed Service pods → Zone OTel Collector → VictoriaMetrics/VictoriaLogs
  → zone-observability-stream → Zone Public Edge → Browser
```

OTel Collector overwrites workload-supplied owner/workspace/instance/component telemetry
attributes from protected Kubernetes metadata. Zone Control Edge prepares a scoped
five-minute `observability.read` ticket; Zone Public Edge strips client-supplied scope
and allows one bounded stream. Browser supplies panel/component/time/cursor only, never
raw PromQL/LogsQL, namespace, label selector or owner/Zone identity. Stream disconnect
cancels upstream; slow metric clients coalesce and slow log clients close. This path
does not create timeline entries or mutate business state.

## 13. P00 fixture vocabulary and review gate

The reusable data contract is
[`controlplane/internal/managedservice/test/fixtures/CONTRACT.md`](../../controlplane/internal/managedservice/test/fixtures/CONTRACT.md).
It supplies stable personal/tenant/Zone/key/revision/graph/result vocabulary to CP, JO,
DP and Console tests, but does not create a shared business helper or runtime state.

P00 can exit only after these owners review this document and attached fixture/proto
registry against the cited platform God Views:

| Reviewer | Must approve |
| --- | --- |
| Controlplane architecture | desired state, ownership split, CTE/outbox and hard-delete semantics |
| JO transport | WAL checkpoint, route/key/order, retry timer, DLQ and result relay |
| Dataplane | fence, Zone-bound envelope materialization/render, Kubernetes graph, idempotency and graceful shutdown |
| Notification | stable timeline identity and non-rollback delivery semantics |
| Security/Zone | trusted identity, critical route, Zone binding, ACL/RBAC and observability scope |

P00 review đã freeze contract này. P01 có thể giữ migration và canonical inner protobuf
dormant, nhưng không được register command route, customer mutation, JO dispatcher,
Dataplane executor hoặc Kubernetes client trước các phase gate tương ứng.
