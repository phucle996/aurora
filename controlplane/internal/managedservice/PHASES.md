# Managed Service Platform — Phase và Task implementation plan

> **Status:** PLANNED — đây là execution plan sinh từ contract đã đóng trong
> [STAGING.md](./STAGING.md), không mô tả workflow đã ship.
>
> **Thứ tự ưu tiên tài liệu:** `AGENTS.md` và God View của workflow →
> `STAGING.md` → [IDEA.md](./IDEA.md) → file này. File này không được tự mở lại
> product/topology decision đã chốt.

## 1. Mục tiêu và cách dùng

Module này phải biến catalog SRE thành managed service customer chạy ở Kubernetes
đúng Zone, nhưng vẫn giữ boundary rõ ràng:

```text
SRE catalog / Controlplane desired state
  → PostgreSQL outbox + logical WAL/CDC
  → Job Orchestrator
  → Kafka đúng Zone
  → Dataplane render/apply Kubernetes
  → Kafka result
  → Controlplane result settlement + Notification timeline
```

Một phase chỉ mở khi dependency phase trước đã đạt exit gate. Một task chỉ hoàn tất
khi code, migration/contract, test, observability và rollback/failure semantics cùng
đạt; compile hoặc happy-path demo không đủ.

Mã task dùng tiền tố `MS-xxx`. Mỗi task nêu owner kỹ thuật, dependency và acceptance
evidence. Nếu một task làm lộ quyết định business/topology chưa có trong
`STAGING.md`, dừng task đó để cập nhật staging/God View trước, không tự suy luận.

## 2. Invariant không được thay đổi trong bất kỳ phase nào

| Chủ đề | Invariant bắt buộc |
| --- | --- |
| Ownership | Controlplane sở hữu catalog, owner, Zone binding và desired state; Dataplane chỉ thực thi đúng Zone; JO chỉ bridge/settle theo contract. |
| Transport | CP không publish Kafka trực tiếp. Durable path là PostgreSQL outbox/WAL → JO → Kafka. Managed Service V1 không có NATS runtime subject, Redis Pub/Sub runtime hay browser runtime channel. |
| Validation | Handler parse/validate/canonicalize HTTP ingress. Service/repository tin entity đã normalize. DP chỉ validate transport trust boundary mới. |
| Workflow isolation | Mỗi workflow có một handler method → một service method → một repository method. Personal và tenant tách vertical slice; duplicate code được chấp nhận, helper/generic mutator không được dùng. |
| Persistence | Catalog là system table; customer aggregate personal và tenant physical table riêng. Desired state và module outbox commit trong cùng PostgreSQL transaction/CTE. |
| Idempotency | Create dedupe bằng `(workspace_id, code) + canonical create intent`; DP external side-effect fence là `instance_id + operation_id + generation`, không phải HTTP `Idempotency-Key` hay `attempt`. |
| Revision | Published BlueprintRevision và InstanceRevision immutable. Update tạo revision/generation mới; retry tự động giữ operation/generation/revision cũ và chỉ đổi command `event_id`/attempt. |
| Secret | CP chỉ giữ opaque `parameter_envelope` + digest bound với trusted Zone. DP chỉ materialize trong RAM của một execution; không ghi plaintext vào log, DB, Redis, NATS, Zone KV hoặc result. |
| Kubernetes | Customer chỉ namespaced object/CRD. DP force namespace/protected metadata, dùng SSA field manager cố định, không adopt object foreign và không blind rollback partial apply. |
| Completion | Kubernetes apply ACK không phải success. Success chỉ khi static component readiness đạt; delete chỉ khi graph đã gone/finalizer hoàn tất. |
| Runtime telemetry | Victoria read path là diagnostic/eventual. Nó không quyết định lifecycle, billing, authorization hay timeline terminal. |

## 3. Dependency map và điểm có thể song song

```text
P00 Source of truth
  ↓
P01 Shared foundation, Zone binding, migration, transport base
  ├──────────────→ P02 SRE catalog/admin API
  │                    ↓
  │                 P03 Customer catalog/read + form contract
  │                    ↓
  └──────────────→ P04 Customer desired-state/outbox
                         ↓
                       P05 JO CDC/Kafka dispatch
                         ↓
                       P06 Dataplane render/apply
                         ↓
                       P07 Result settlement/reconcile/timeline
                      ↙                         ↘
            P08 Console admin/customer UI      P09 Zone observability read path
                      ↘                         ↙
                         P10 Staging drills, pilot, release gate
```

Quy tắc song song:

* Sau P00, `MS-010` migration phải hoàn tất trước P02 integration; `MS-012` Zone binding
  và `MS-013` protobuf của P01 có thể chạy song song với P02 sau khi migration shape
  đã được review.
* P03 UI catalog/form chỉ có thể mock API trước P02 hoàn tất; không merge create
  mutation trước P04.
* P08 có thể dựng component/mock khi P03/P04 đang làm, nhưng chỉ nối mutation/realtime
  sau P07.
* P09 có thể dựng OTel/stream service sau P01, nhưng public route chỉ enable sau P07
  có security/reconcile evidence.
* P05, P06 và P07 là chuỗi bắt buộc; không tách delivery contract thành các branch
  CP/JO/DP không tương thích.

## 4. Definition of done cho một task

Mỗi task implementation phải khai báo và review đủ bảy điểm sau:

1. **Owner và boundary:** service nào sở hữu state/side effect; dependency nào bị cấm.
2. **Input trust:** request/Protobuf nào được validate ở boundary; data nào downstream
   chỉ tin tưởng.
3. **Durability/order:** transaction, outbox/inbox, Kafka key, commit point, retry/DLQ
   và stale fence.
4. **Concurrency:** lock order, unique predicate, lease/fence hoặc idempotency marker.
5. **Failure/rollback:** failure trước/sau commit, retry disposition và cách disable/
   rollback không làm mất desired state.
6. **Observability:** trace correlation, fixed-cardinality metric, sanitized log/audit;
   không đưa owner/resource/customer payload làm platform metric label.
7. **Evidence:** unit/integration/contract/E2E test đúng module root, formatter/linter,
   `git diff --check` và God View cập nhật nếu topology/contract thay đổi.

Không task nào được:

* tạo `helpers.go`, `common.go`, generic repo/service/validator hoặc `MutateInstance`;
* trả raw `pgx`/provider error qua service/handler;
* dùng DTO làm entity business hoặc DTO response thay cho inline `gin.H`;
* kiểm tra nil dependency trong service/repository/handler thay vì fail-fast tại app
  construction;
* gọi Kubernetes từ CP, truy cập CP PostgreSQL từ DP, hay cấp Zone KV credential cho
  CP/JO;
* publish broker trước PostgreSQL commit hoặc ACK Kafka trước durable side effect/DLQ.

## 5. P00 — End-to-end source of truth và implementation freeze

**Status:** CLOSED — design review đã freeze God View, fixture vocabulary và inner
wire registry. P01 chỉ được phép dựng foundation dormant; không customer mutation hay
runtime route nào được suy ra từ việc freeze này.

**Mục tiêu:** tạo artifact SoT trước dòng code workflow đầu tiên, để mọi team làm
cùng command/result/state model.

**Dependency:** không có.

**Exit gate:** God View chuyên biệt tồn tại; phase/task registry có owner; no-code
contract review xác nhận không còn diverge với `STAGING.md`.

### MS-000 — Tạo Managed Service lifecycle God View

* **Artifact:** [managed_service_lifecycle_god_view.md](../../../god_view/managedservice/managed_service_lifecycle_god_view.md)
  đã tạo và được chốt trong design review.
* **Owner:** Controlplane architecture owner.
* **Phạm vi:** tạo `god_view/managedservice/managed_service_lifecycle_god_view.md`.
* **Nội dung bắt buộc:** admin catalog, personal/tenant desired state, CTE/outbox,
  logical WAL/CDC, generic Kafka topics/inner protobuf, DP Kubernetes graph, result
  inbox/reconcile, Notification timeline và Zone Victoria read path.
* **Acceptance:** một request create được trace được từ trusted Envoy context tới
  terminal durable state; sequence nêu rõ crash windows, retry `0..4`, fence,
  deletion hard-delete và public observability không là completion path.
* **Rollback:** documentation-only; không tạo code/migration.

### MS-001 — Freeze wire registry và generated-code ownership

* **Artifact:** [contracts/proto/README.md](../../../contracts/proto/README.md) đã
  reserve canonical root source `contracts/proto/managed_service.proto`; P01 tạo
  source và generated bindings từ đúng root này, không có local copy.
* **Owner:** JO + Dataplane transport owners.
* **Phạm vi:** ghi trong God View và proto plan ownership của outer `JobCommandV1` /
  `JobExecutionResultProto`, inner `ManagedServiceCommandV1` /
  `ManagedServiceResultV1`, route `MANAGED_SERVICE` +
  `managed_service.instance.execute`.
* **Required decisions to mirror:** command key/result key `instance_id`,
  `job_id=command_event_id`, source result event, schema versions, payload limits,
  trace context, outcome mapping và backward-compatible field numbering.
* **Acceptance:** byte-compatibility test strategy được chỉ ra trước khi thêm `.proto`;
  không có copy protobuf drift giữa `job-orchestrator/proto` và `dataplane/proto`.
* **Rollback:** không publish route/topic cho đến P05.

### MS-002 — Freeze test fixture vocabulary

* **Artifact:** [test/fixtures/CONTRACT.md](./test/fixtures/CONTRACT.md) đã định nghĩa identity,
  graph, key, retry, stale/duplicate và hard-delete fixture contract; test implementation
  pending ở phase owner tương ứng.
* **Owner:** module test owner.
* **Phạm vi:** define stable fixtures: personal owner, tenant owner, two workspaces,
  two trusted Zones, published/retired revision, three-component
  blueprint, foreign Kubernetes object, retryable and terminal result.
* **Acceptance:** fixture identity/hash/UUID conventions được viết vào God View/test
  plan; same fixtures được reuse across CP, JO, DP and Console tests without sharing
  business helper code.
* **Rollback:** fixture only; no runtime state.

## 6. P01 — Shared foundation, Zone binding và module baseline

**Mục tiêu:** dựng dependency fail-fast, immutable data shape, trusted Zone binding và
transport skeleton trước customer mutation.

**Status:** FOUNDATION SLICE SHIPPED — MS-010 đến MS-013 đã có baseline migration,
module shell, trusted Zone-bound contract và generated binding dormant. Kafka ACL
provisioning (MS-014) cùng observability/test harness đầy đủ (MS-015) vẫn là release
gate trước P02/P04; P01 không mở customer/admin business API.

**Dependency:** P00.

**Exit gate:** fresh Controlplane/JO/DP environment boot được hoặc fail-close đúng
contract; no customer API/mutation is enabled yet.

### MS-010 — Six-file PostgreSQL baseline migration

* **Owner:** Controlplane Managed Service persistence owner.
* **Files:** `controlplane/internal/managedservice/migrations/000001` đến `000006`
  và `migration.go`/global app migration registration.
* **Scope:** tạo lifecycle enums; system catalog hierarchy/audit; immutable blueprint
  revisions; personal aggregate; tenant aggregate; outbox/index/constraint/trigger.
  `blueprint_revisions` giữ canonical bounded YAML bundle + component contract;
  instance revision giữ Zone-bound envelope/digest, không raw parameter map.
* **Concurrency/data requirements:** physical personal/tenant tables riêng; unique
  `(workspace_id, code)`; one non-terminal operation/instance; CTE lock order
  `workspace FOR KEY SHARE → instance FOR UPDATE → revision/operation`; evidence rows
  có retention predicate thay vì tombstone resource.
* **Acceptance:** migration idempotent trên fresh DB, rollback clean, constraint tests
  cover duplicate code, active/pending heads, cross-owner FK/lock race, outbox atomicity
  và hard-delete/code reuse.
* **Rollback:** module chưa enabled; disable migration runner before any customer data.
  Không thêm migration thứ bảy cho baseline change trước pilot.

### MS-011 — App/module fail-fast wiring

* **Owner:** Controlplane bootstrap owner.
* **Scope:** hoàn chỉnh managedservice module constructor, dependency injection,
  migration registration and graceful `Bootstrap/Stop`; no hidden fallback.
* **Invariant:** app construction validate required PostgreSQL, trusted auth context
  adapter and contract registry once. Service/repository/handler do not nil-check
  infrastructure dependency.
* **Acceptance:** missing config/dependency makes app fail before serving; module shell
  does not connect Kubernetes, Kafka directly, NATS, Vault or Zone infra.
* **Rollback:** module route remains unregistered/disabled; no partial worker starts.

### MS-012 — Trusted Zone binding contract

* **Owner:** Managed Service persistence owner.
* **Scope:** every future personal/tenant mutation receives `zone_id` only from the
  trusted Envoy context. The workflow-local repository CTE locks the scoped workspace
  and verifies its persisted `zone_id` in the same statement before it inserts an
  instance/revision/operation/outbox. There is no Zone key table, keyset, attestation,
  Kafka metadata projection or key-management API.
* **Acceptance:** a client-supplied/mismatched Zone is rejected before durable intent;
  service/repository never parse a header or perform a second unscoped lookup.
* **Rollback:** no Zone runtime state exists; disable the dormant customer route.

### MS-013 — Shared protobuf and generated-code baseline

* **Owner:** JO/DP transport owners.
* **Scope:** add inner Managed Service command/result messages using additive,
  byte-compatible fields; regenerate both Rust codebases from byte-identical source.
* **Required fields:** all IDs/fences, owner/workspace/instance code, operation kind,
  revisions, canonical template bundle/component contract, hashes, Zone-bound envelope/digest,
  safe observed output, taxonomy disposition and schema versions.
* **Acceptance:** protobuf breaking check, fixture encode/decode across JO↔DP, oversize
  and unknown-version negative tests. No raw parameter/manifest/Secret can appear in
  result/DLQ schema.
* **Rollback:** do not register route/consumer until P05; additive schema remains
  dormant and compatible with existing workloads.

### MS-014 — Kafka topic, ACL và route admission preparation

* **Owner:** platform Kafka/JO owner.
* **Scope:** pre-provision generic Zone command topics, result/DLQ policies and ACLs
  for JO producer, exact-Zone DP command consumer, exact-Zone DP result producer and
  JO result consumer. Add planned route registry contract but keep it disabled.
* **Acceptance:** Zone A credentials cannot consume Zone B command topic; producer uses
  idempotence, `acks=all`, zstd, manual consumer commits and bounded record size;
  auto-topic creation stays disabled.
* **Rollback:** revoke Managed Service principals/route registration; existing Kafka
  workloads are unaffected.

### MS-015 — Foundation observability and test harness

* **Owner:** Controlplane/JO/DP observability owners.
* **Scope:** bind Managed Service operations vào unified Controlplane metric contract,
  thêm trace attributes với fixed cardinality và
  test harness for PostgreSQL, Kafka and trusted Zone-binding fixtures. No customer runtime stream.
* **Acceptance:** every future command can correlate `operation_id`, command event,
  instance ID and trace context without emitting them as platform metric labels; test
  harness can simulate CP commit, duplicate CDC and DP redelivery.
* **Rollback:** exporter failure sau bootstrap là diagnostic-only và không rollback
  business transaction. Bootstrap tuân global OTel `fail_strategy`: `fail_open` dùng
  no-op provider, còn `fail_close` dừng process trước khi nhận traffic.

## 7. P02 — SRE catalog, immutable blueprint và admin-plane workflow

**Mục tiêu:** SRE có thể tạo/publish/retire catalog revision an toàn trước khi customer
thấy catalog.

**Dependency:** P01 migrations, app wiring and fixture vocabulary.

**Exit gate:** a published revision can be created, hash-reproduced, selected by an
eligible Zone and safely retired/critical-protected; no customer instance is needed.

### MS-020 — Category vertical workflows

* **Owner:** Controlplane `category` object slice.
* **Scope:** create/list/get/update use `/admin/managed-services/catalog/*`; retire uses
  `/admin/critical/managed-services/catalog/*`. Each workflow has its own
  handler/service/repo method and CTE predicate.
* **Security:** no CP authorization middleware; Envoy/ACR admin route supplies trusted
  audit actor. Missing actor fails closed; response uses inline `gin.H`.
* **Acceptance:** immutable/non-reusable code, bounded i18n metadata, audit record,
  duplicate/race tests and trusted-header spoofing negative test.

### MS-021 — Definition vertical workflows

* **Owner:** Controlplane `definition` object slice.
* **Scope:** create/list/get/update definition use normal admin route; retire uses the
  critical route. Compatible locks prevent hierarchy mutation racing a new
  descendant/reference.
* **Acceptance:** parent retired/invalid state blocks mutation with taxonomy; no generic
  catalog mutator; admin actor/audit/outcome are durable and sanitized.

### MS-022 — Version vertical workflows

* **Owner:** Controlplane `version` object slice.
* **Scope:** create/list/get/update version use normal admin route; deprecate/retire use
  the critical route. Enforce `AVAILABLE|DEPRECATED|RETIRED` semantics and
  default-published pointer guard.
* **Acceptance:** deprecated blocks new create but existing pinned instance policy is
  representable; retired only permits pending/retry/reconcile/delete; stale default
  cannot be silently accepted.

### MS-023 — Blueprint and draft vertical workflows

* **Owner:** Controlplane `blueprint` and `revision` object slices.
* **Scope:** create blueprint, create/get/patch draft. Draft stores canonical YAML,
  input/UI/output/component contracts, Zone selector and capability requirement.
* **Validation boundary:** handler validates JSON DTO/type sizes; draft validation
  validates YAML syntax, Platform Form Contract, reserved metadata/tag policy,
  component IDs/order/readiness declaration and literal Secret.data/stringData ban.
* **Acceptance:** CP does not enumerate `!aurora/param` or bind schema to tags;
  missing/unused relation remains SRE debt, not a runtime generic validator.

### MS-024 — Publish revision and default-pointer workflow

* **Owner:** Controlplane `revision` object slice.
* **Scope:** atomic draft validation → canonicalize/hash → append immutable published
  revision → audit → switch version default pointer.
* **Concurrency:** lock version/default pointer/revision in one CTE; published body,
  hash, selector, component/readiness contract and form contract never update in place.
* **Acceptance:** same artifact re-hashes deterministically; stale Console revision is
  rejected rather than remapped; publish crash leaves either old default or fully
  published new default, never a half graph.

### MS-025 — Critical in-use catalog mutation workflow

* **Owner:** Controlplane object slice + ACR route owner.
* **Scope:** blueprint/draft/validate/publish/retire/delete only exist under
  `/admin/critical/managed-services/catalog/*`; there is no normal mirror. CTE detects
  published/default descendants, personal/tenant revision pins and stale row version,
  while ACR supplies the verified marker/challenge ID.
* **Invariant:** proof is not a bypass for immutable published revision or pinned FK.
  In-use published revision can retire where lifecycle allows, never hard-delete.
* **Acceptance:** concurrent create pin versus retire/delete lock test; missing critical
  marker fails close; critical audit excludes proof/nonce/template plaintext.

### MS-026 — SRE catalog read model and Admin UI

* **Owner:** Controlplane read slice + Admin UI owner.
* **Scope:** safe catalog/admin audit list APIs and Admin UI tables/detail/draft/publish
  flow. Raw template bytes are only returned by the dedicated SRE draft-editor GET;
  customer/general list APIs never receive them, Zone executor internals or secret.
* **Acceptance:** UI surfaces `in_use` precondition but treats backend CTE as authority;
  critical flow obtains new proof bound to method/path/body; stale page refreshes after
  revision/default change.
* **Rollback:** hide admin feature/route while catalog data remains durable; do not
  delete a published revision to roll back UI.

## 8. P03 — Customer catalog discovery và form contract

**Status:** CLOSED — personal/tenant catalog và version/form read paths, static Zone
eligibility, authorization permission và Cloud Console memory-only form foundation đã
ship. Customer mutation vẫn absent cho tới P04.

**Mục tiêu:** customer chỉ thấy revision provisionable trong trusted
owner/workspace/Zone scope và Console render được form mà không biết YAML/Kubernetes.

**Dependency:** P02 published catalog, Zone selector/capability fixtures.

**Exit gate:** personal và tenant customer có catalog read/form response stable;
unsupported/stale scope không thể submit desired-state mutation.

### MS-030 — Personal catalog discovery vertical slice

* **Owner:** Controlplane `personal_catalog` entity/repo/service/handler.
* **Routes:** personal catalog list/detail version, derived from trusted user/workspace/
  active Zone context.
* **Rules:** no client `owner_id`, `workspace_id` or `zone_id`; only published default
  revision that matches Zone selector/capability and version lifecycle is returned.
* **Acceptance:** scope/cache tests prove a personal user cannot discover tenant or
  unsupported Zone contract; response excludes YAML, bundle reference, protected
  metadata and internal capability details.

### MS-031 — Tenant catalog discovery vertical slice

* **Owner:** Controlplane `tenant_catalog` entity/repo/service/handler.
* **Rules:** same public shape as personal but authorization middleware derives tenant
  workspace permission; no shared owner-type polymorphic repository query.
* **Acceptance:** tenant visibility follows trusted workspace authorization; identical
  user in another workspace cannot reuse a catalog response as a create authority.

### MS-032 — Version/form read contract and stale-revision semantics

* **Owner:** Controlplane catalog read owner.
* **Scope:** return restricted Platform Form Contract, UI contract, default revision ID,
  catalog/version display metadata and lifecycle. Keep `input_schema` and YAML
  independent artifacts.
* **Acceptance:** unknown widget/contract version fails close in Console; revision
  retired/default switched while form is open yields `CATALOG_STALE`/refresh behavior,
  never automatic parameter migration.

### MS-033 — Cloud Console discovery and dynamic form foundation

* **Owner:** Cloud Console managed-services feature owner.
* **Planned surface:** `/managed-services`, `/managed-services/new` catalog → configure
  → review. Use feature-owned finite widget registry only for S05 types/cardinality.
* **Security/UI rules:** form draft stays React memory; clear it on auth generation,
  workspace, Zone or revision change; never persist raw parameter in URL, localStorage,
  sessionStorage or analytics.
* **Acceptance:** type/cardinality/i18n fallback/stale scope tests; unsupported schema
  does not show submit; UI sends only code/name/revision/input document, never trusted
  context fields.

### MS-034 — Customer catalog contract test suite

* **Owner:** CP + Console test owners.
* **Scope:** golden API response fixtures for personal/tenant, stale published default,
  deprecated/retired version, Zone mismatch, unknown widget and oversized document.
* **Acceptance:** contract tests run before P04 create API changes; Console mock uses
  no hard-coded service-specific form.

## 9. P04 — Customer desired state, immutable revisions và transactional outbox

**Mục tiêu:** accept create/update/delete/retry as durable desired state only. No task
in this phase executes Kubernetes or assumes a Kafka result.

**Dependency:** P01 trusted Zone-binding + migration baseline, P03 catalog/form
contract. Each create accepts only the Zone bound to the trusted workspace context.

**Exit gate:** every personal/tenant mutation commits exact aggregate + outbox atomically,
dedupes correctly and remains safe when no JO/DP is running. Public mutation admission
stays feature-disabled outside controlled integration fixtures until P07 can settle
terminal result/timeline.

### Shared implementation boundary for P04

All tasks below must use files organized by owner/domain:

```text
domain/entity/            personal_instance.go / tenant_instance.go
repository/               personal_instance_repo.go / tenant_instance_repo.go
service/                  personal_instance_service.go / tenant_instance_service.go
transport/http/dto/       personal_instance_dto.go / tenant_instance_dto.go
transport/http/handler/   personal_instance_handler.go / tenant_instance_handler.go
```

They may duplicate code deliberately. They must not share a polymorphic instance
mutation helper, generic CTE builder or common handler validator. Handler builds a
workflow-local entity after canonicalizing; service/repository do not validate it again.

### MS-040 — Personal instance read vertical slices

* **Owner:** `personal_instance` read methods.
* **Routes:** list, get by immutable code, list/get operation. `connection` endpoint is
  introduced only in P07 after safe observed output exists.
* **Acceptance:** scope derives user/workspace from middleware; list/detail carries
  desired, observed and operation states separately; no envelope/input raw value is
  selected or serialized.

### MS-041 — Tenant instance read vertical slices

* **Owner:** `tenant_instance` read methods.
* **Rules:** physical tenant tables only; trusted tenant/workspace scope always present
  in CTE predicate.
* **Acceptance:** tenant read cannot see another tenant/personal row even if UUID/code
  is guessed; pagination and code validation happen only in handler.

### MS-042 — Personal create desired-state workflow

* **Route:** `POST /api/v1/personal/managed-services/instances`.
* **Handler:** validates code/name/revision/form payload, canonicalizes input/hash,
  receives the trusted Zone context and constructs a personal-create entity.
* **Repository CTE:** lock workspace/revision/default pointer compatibly; compare
  existing `(workspace_id, code)` create intent; insert `PROVISIONING` instance,
  immutable revision, CREATE operation generation 1 and `available_at=now()` outbox
  in one commit.
* **Duplicate/failure:** same code+intent returns original instance/operation; different
  intent returns conflict; failure before commit leaves no message, failure after commit
  remains recoverable by outbox.
* **Acceptance:** PostgreSQL race test with concurrent same/different intent; no raw
  parameter in database query result/log/test snapshot.

### MS-043 — Tenant create desired-state workflow

* **Route:** `POST /api/v1/tenant/managed-services/instances`.
* **Implementation:** independent tenant handler/service/repository/CTE with same
  semantics as MS-042; owner snapshot is `tenant_id`, never a polymorphic owner field.
* **Acceptance:** authorization/multi-workspace race tests and exactly one tenant
  aggregate/outbox record under duplicate submit.

### MS-044 — Personal rename workflow

* **Route:** `PATCH /api/v1/personal/managed-services/instances/:code/name`.
* **Scope:** mutate display `name` only; it does not create revision, generation,
  outbox, Kubernetes apply or code change.
* **Acceptance:** name validation only in handler; CTE scope/row-affect taxonomy and
  concurrent rename test; no impact on active/pending operation.

### MS-045 — Tenant rename workflow

* **Route:** tenant mirror of MS-044.
* **Acceptance:** tenant CTE is separate; code, workspace binding and instance identity
  remain immutable; no generic rename function shared with personal path.

### MS-046 — Personal configuration update workflow

* **Route:** `PATCH /api/v1/personal/managed-services/instances/:code/configuration`.
* **Handler:** validates pinned revision input contract, canonicalizes desired hash and
  keeps the trusted Zone binding; it never pre-fills old raw input from CP.
* **Repository CTE:** lock instance, reject non-terminal operation/invalid lifecycle,
  no-op duplicate target hash, create pending immutable InstanceRevision + UPDATE
  operation/generation/outbox atomically while retaining active revision.
* **Acceptance:** update same target returns current operation; different target during
  active operation conflicts; terminal failure later can clear pending but not alter
  active revision.

### MS-047 — Tenant configuration update workflow

* **Route:** tenant mirror of MS-046.
* **Acceptance:** separate tenant CTE/repo function, correct tenant snapshot and Zone
  key selection; no cross-owner revision lookup.

### MS-048 — Personal delete desired-state workflow

* **Route:** `DELETE /api/v1/personal/managed-services/instances/:code`.
* **Repository CTE:** transition to `DELETING`, create DELETE operation/outbox and
  preserve active revision/config ciphertext until DP confirms graph removal.
* **Duplicate/failure:** repeat while deleting returns existing operation; no rollback
  to `ACTIVE`; no hard delete before P07 terminal success settlement.
* **Acceptance:** race with update/retry blocked by one non-terminal operation guard;
  code remains unavailable until actual hard-delete success.

### MS-049 — Tenant delete desired-state workflow

* **Route:** tenant mirror of MS-048.
* **Acceptance:** tenant physical table only, same deletion fence semantics and
  cross-workspace deny test.

### MS-050 — Personal manual retry workflow

* **Route:** `POST /api/v1/personal/managed-services/instances/:code/operations/:operation_id/retry`.
* **Scope:** only allowed terminal operation/lifecycle predicate. It creates a **new**
  operation and generation, pins the historical target revision; it does not create a
  configuration revision or reset old automatic retry budget.
* **Acceptance:** retry target cannot be swapped by client; stale/active operation
  conflict is stable taxonomy; delete retry remains `DELETING` and cannot resurrect.

### MS-051 — Tenant manual retry workflow

* **Route:** tenant mirror of MS-050.
* **Acceptance:** independent tenant flow and CTE; retries retain immutable tenant
  ownership/workspace/Zone snapshot.

### MS-052 — P04 transactional and negative integration suite

* **Owner:** Controlplane test owner.
* **Scope:** create/update/delete/retry sequences for both branches, rollback before/
  after transaction, code reuse only after hard delete, Zone-bound input envelope,
  version/default pin race, and no-JO/no-DP outage.
* **Exit evidence:** `go test ./internal/managedservice/...` from Controlplane module
  plus PostgreSQL integration fixtures; no transaction leaves aggregate without outbox
  or outbox without aggregate.

### MS-053 — Personal/tenant dispatch-status internal workflows

* **Owner:** two separate `personal_instance` and `tenant_instance` internal application
  slices, consumed only by JO after Kafka ACK.
* **Scope:** each service/repository function applies an already validated dispatch
  entity and advances exact current operation from `ACCEPTED`/`DISPATCHING` to
  `RUNNING`, keyed by source event/operation/generation. No browser route and no
  generic owner-type switch are introduced.
* **Acceptance:** duplicate JO callback is no-op; stale source event cannot mark newer
  operation running; this transport-progress write never promotes revision or settles
  terminal lifecycle. P05 cannot enable MS-062 without both branch tests.

## 10. P05 — Job Orchestrator CDC, Kafka dispatch, retry timer và DLQ

**Mục tiêu:** move durable intent to the exact Zone at-least-once, preserving aggregate
ordering and never treating broker ACK as CP state.

**Dependency:** P04 outbox contract and P01 shared proto/topic/ACL preparation.

**Exit gate:** JO can dispatch/replay a test outbox record safely, respects `available_at`,
and no Managed Service code uses NATS or a direct CP Kafka producer.

### MS-060 — Enable MANAGED_SERVICE route registry

* **Owner:** JO + DP route owners.
* **Scope:** register only `source_domain=MANAGED_SERVICE` and
  `job_topic=managed_service.instance.execute`; unsupported action/source pair fails
  closed before Kafka publish/consume.
* **Acceptance:** shared registry/fixture test rejects source spoofing and old
  workload/action mismatch; existing MAIL/STORAGE/HYPERVISOR routes stay unchanged.

### MS-061 — JO CDC outbox decode and command encoder

* **Owner:** JO changefeed owner.
* **Scope:** decode only `managed_service_outbox_records`, validate Zone UUID,
  payload/schema/hash bounds, and construct outer `JobCommandV1` with inner opaque
  Managed Service command exactly as committed by CP.
* **Ordering:** key command by `resource_id=instance_id`; `job_id` is command event ID;
  JO does not regenerate envelope, revision/hash or owner fields.
* **Acceptance:** WAL duplicate after broker ACK emits byte-compatible duplicate command;
  malformed row is quarantined/DLQ and terminally settled through P07, never silently
  skipped or retried forever.

### MS-062 — Kafka publish and checkpoint settlement

* **Owner:** JO changefeed owner.
* **Scope:** idempotent Kafka producer with `acks=all`, bounded retry and zstd; advance
  replication LSN/checkpoint only after durable broker ACK.
* **Dispatch projection:** after ACK, invoke the scoped CP dispatch-status CTE to advance
  the current operation `ACCEPTED/DISPATCHING → RUNNING` only when source event/fence
  still matches. This is transport progress, not JO deciding a terminal business
  outcome; duplicate projection is a no-op.
* **Timeline dependency:** reserve operation correlation at dispatch. Actual
  `PROCESSING` XADD is implemented/enabled together with P07 `MS-084`, so P05 does not
  introduce a partial notification shape; when enabled it occurs only after durable
  command ACK and XADD failure never undoes the command.
* **Acceptance:** crash injection proves ACK-before-LSN yields safe duplicate and
  broker failure does not advance LSN or falsely mark operation running/successful.

### MS-063 — Durable delayed automatic retry scheduler

* **Owner:** JO dispatch owner + CP result-settlement contract owner.
* **Scope:** treat CDC as primary admission. For retry outbox with future
  `available_at`, acknowledge CDC intake without early Kafka publish; a bounded,
  lease/fenced due scan over this module's pending records recovers timer/restart.
* **Invariants:** scan does not create aggregate, mutate desired state or bypass CTE;
  actual jittered timestamp was persisted by CP. It dispatches attempts 1–4 only;
  attempt 4 retryable becomes terminal via P07.
* **Acceptance:** restart before due time, duplicate scanner/rebalance and clock-skew
  tests produce at most idempotent duplicate dispatch and no early side effect.

### MS-064 — DP/JO contract rejection and DLQ path

* **Owner:** JO result/DLQ owners.
* **Scope:** classify malformed/oversize/version/route/Zone/hash mismatch as
  `COMMAND_CONTRACT_INVALID`, `COMMAND_ZONE_MISMATCH` or `COMMAND_HASH_MISMATCH`.
  Publish bounded `DeadLetterRecordV1` before source commit/checkpoint.
* **Acceptance:** DLQ excludes ciphertext plaintext/manifest/Secret, retains source
  event/correlation/taxonomy only; terminal current operation settlement is delegated
  to P07 and stale source is only audited.

### MS-065 — JO graceful shutdown, backpressure and observability

* **Owner:** JO runtime owner.
* **Scope:** bounded in-flight command/publish/retry queue, cancellation, jittered
  restart and fixed-cardinality metrics for outbox age/CDC lag/publish/DLQ/retry.
* **Acceptance:** SIGTERM does not ACK an uncommitted source record; Kafka outage grows
  durable outbox age but not RAM unboundedly; no NATS credential/subject is added.

### MS-066 — JO integration test matrix

* **Owner:** JO test owner.
* **Scope:** real PostgreSQL logical-replication fixture or faithful changefeed fixture,
  Kafka ACL/topic fixture, duplicate WAL, duplicate Kafka record, delayed retry, DLQ,
  Zone A/B cross-wire and trace propagation.
* **Exit evidence:** JO Rust format/clippy/test and protobuf byte-identity checks pass;
  result completion is intentionally not claimed until P07.

## 11. P06 — Dataplane YAML renderer và Kubernetes managed-service executor

**Mục tiêu:** consume only verified command for local Zone, render deterministic YAML,
apply/reconcile managed graph and emit a terminal result without leaking plaintext.

**Dependency:** P05 enabled route/command dispatch, P01 Zone binding, P02 immutable
blueprint/component contract.

**Exit gate:** sandbox Zone can safely execute duplicate create/update/delete command,
reject foreign/cross-Zone input and return a terminal result. No browser runtime path
is introduced.

### MS-070 — Exact-Zone command admission

* **Owner:** Dataplane job-runtime owner.
* **Scope:** consume only Zone command topic with Zone-specific ACL; decode outer/inner
  Protobuf and validate size/schema/source domain/job topic/target Zone/source event,
  all revision/hash fields and parameter-envelope digest before any Kubernetes call.
* **Fence:** `instance_id + operation_id + generation` is execution identity. Attempt
  identifies source command/result only and must not make a retry duplicate create a
  second external side effect.
* **Acceptance:** malformed, unsupported version, cross-Zone and hash mismatch reject
  before local lease/Kubernetes API; no CP DB/Shared Redis/Vault dependency appears.

### MS-071 — Zone-local envelope materialization

* **Owner:** Dataplane security/executor owner.
* **Scope:** materialize the opaque Zone-bound envelope only in short-lived execution
  RAM under the future Zone-local runtime secret contract. That contract has no
  Controlplane keyset, attestation or rotation API.
* **Failure semantics:** malformed or non-Zone-bound envelope is terminal contract
  taxonomy; there is no plaintext fallback or log.
* **Acceptance:** envelope fixture remains opaque; memory/logging test proves raw
  parameter/db name/password cannot leave execution scope.

### MS-072 — YAML AST renderer and static component-contract validator

* **Owner:** Dataplane renderer owner.
* **Scope:** support only `!aurora/param <key>` typed-node exact replacement and
  `!aurora/component <id>` deterministic naming. Reject text interpolation, loop,
  include, function, metadata parameterization, invalid tag location/missing key/type
  incompatibility and invalid post-render YAML as `SRE_TEMPLATE_INPUT_MISMATCH`.
* **Component contract:** validate document/component map, static apply/delete order,
  primary uniqueness and readiness rule/deadline from pinned revision; do not inspect
  CP latest catalog.
* **Acceptance:** golden YAML/hash/name/namespace tests, list/set canonical value tests
  and adversarial YAML/tag tests. Rendering never reads network/file outside exact
  command bundle or executes code.

### MS-073 — Namespace, protected metadata và ownership enforcement

* **Owner:** Dataplane Kubernetes adapter owner.
* **Scope:** derive `aur-ms-{t|p}-{base32(owner_uuid||workspace_uuid)}` namespace;
  force reserved annotations and protected instance/component labels; template/customer
  cannot override them.
* **Kubernetes boundary:** use Zone executor ServiceAccount/RBAC and pinned capability
  profile rather than a CP Kind allow-list; customer graph still only namespaced
  objects/CRDs. Cluster-scoped operator/CRD lifecycle stays SRE bootstrap.
* **Acceptance:** foreign same-name marker returns `K8S_OWNERSHIP_CONFLICT`; renderer
  does not adopt object; OTel annotations are present and customer traffic labels are
  not invented.

### MS-074 — Create/update apply and readiness workflow

* **Owner:** Dataplane executor owner.
* **Scope:** acquire Zone-local lease/fence only to reduce concurrent execution, then
  discovery/dry-run and server-side apply with fixed field manager in static order
  `network-policy → service → workload` unless revision contract explicitly differs.
* **Completion:** monitor static readiness rule/deadline. API apply ACK means
  `PROGRESSING` internally, not success. Kubernetes API outage, Pending/PVC/capacity
  and deadline report the taxonomy disposition declared in STAGING.
* **Partial side effect:** do not blind rollback; same desired hash/fence retry or
  reconcile converges. Lease loss/shutdown produces no `SUCCEEDED` result.
* **Acceptance:** duplicate/redelivery test creates one converged graph, partial apply
  test is recoverable, foreign field ownership reject is terminal, no unbounded wait.

### MS-075 — Delete executor workflow

* **Owner:** Dataplane executor owner.
* **Scope:** enforce pinned delete graph/order `workload → wait finalizer/gone → service
  → network-policy` (or explicit revision contract) and protected ownership checks.
* **Completion:** report success only when all managed components are absent; never
  delete workspace namespace because other instances may share it.
* **Failure semantics:** stuck finalizer/forbidden/foreign object map to sanitized
  taxonomy; retry remains same operation/generation automatically and manual retry
  never resurrects a new instance UUID.
* **Acceptance:** finalizer stall, duplicate delete, partial delete, cancellation and
  code-reuse-after-CP-hard-delete fixtures.

### MS-076 — Terminal Managed Service result producer

* **Owner:** Dataplane job completion owner.
* **Scope:** construct outer `JobExecutionResultProto` using source command event and
  route/domain/attempt, with inner `ManagedServiceResultV1` containing unique result
  event, all fences/hashes, outcome, taxonomy, bounded sanitized message and safe
  observed snapshot.
* **Transport:** publish to generic result topic keyed by `instance_id`; commit command
  offset only after durable result publish or durable DLQ. Do not emit progress via
  NATS/Redis/Centrifugo.
* **Acceptance:** duplicate result/publish crash tests, raw Secret/input/manifest
  redaction test, result key/order fixture and trace propagation test.

### MS-077 — Dataplane sandbox and HA test suite

* **Owner:** Dataplane test owner.
* **Scope:** kind/k3d or staging sandbox with RBAC capability profile, fake/operator
  CRD, trusted Zone binding, Kafka fixture and Kubernetes API fault injection.
* **Acceptance:** cold start, rebalance, lease overlap, process kill while applying,
  Kafka redelivery, Zone mismatch, slow API and graceful shutdown all
  preserve desired-state safety. Run Rust fmt/clippy/test before phase exit.

## 12. P07 — Result inbox, lifecycle settlement, reconciliation và timeline

**Mục tiêu:** convert DP terminal evidence into durable CP lifecycle without allowing
JO, Notification, telemetry or stale result to decide business state independently.

**Dependency:** P05 result route/DLQ and P06 terminal result producer.

**Exit gate:** create/update/delete can reach durable terminal state through result
inbox; replay, stale result, missing result and reconciliation preserve ordering and
do not duplicate external side effect.

### MS-080 — JO Managed Service result validation and routing

* **Owner:** JO result-worker owner.
* **Scope:** decode outer result then inner payload, verify source command/job topic/
  source domain/Zone/attempt/event mapping against authoritative module outbox before
  forwarding settlement intent.
* **Disposition:** malformed/cross-wire/unknown source goes sanitized DLQ/quarantine;
  source older than current fence becomes ignore + metric/audit. JO does not change
  desired state itself.
* **Acceptance:** duplicate result and out-of-order result test; source command event
  cannot settle a different instance/operation/revision.

### MS-081 — Personal result-inbox settlement vertical slice

* **Owner:** Controlplane `personal_instance` result application slice.
* **Entry:** internal JO-to-Controlplane application boundary, not a browser handler;
  it maps already validated transport entity to one service/repository function.
* **Repository CTE:** insert unique result event; lock instance/operation; verify source
  event, Zone, operation/generation/attempt/revision and all hashes; update bounded
  observed version; then success/terminal/retry transition atomically.
* **Lifecycle:** success promotes pending revision; update terminal failure clears
  pending while retaining active; create terminal failure remains `PROVISIONING`;
  delete terminal failure remains `DELETING`; retry creates delayed outbox only when
  attempt budget remains.
* **Acceptance:** all result paths preserve `read_at`/timeline identity behavior and
  cannot write raw result payload/provider error to PostgreSQL.

### MS-082 — Tenant result-inbox settlement vertical slice

* **Owner:** Controlplane `tenant_instance` result application slice.
* **Scope/acceptance:** independent tenant table/CTE/method, same all-fence logic as
  MS-081, no owner-type branching or shared result repo that can cross tenant scope.

### MS-083 — Durable hard-delete, fences and retention workflow

* **Owner:** Controlplane persistence owner.
* **Scope:** on valid DELETE success CTE writes deletion fence then hard-deletes
  instance/revision; operation/result inbox/fence evidence remains until at least
  `max(command retention, result retention, DLQ replay retention) + safety margin`.
* **Acceptance:** old command/result cannot affect a newly reused code because UUID/
  generation/fence differ; retention job cannot remove replay evidence;
  hard delete never occurs on DP apply acknowledgement alone.

### MS-084 — Job notification timeline projection

* **Owner:** JO + Notification Service owners.
* **Scope:** emit `PROCESSING` after durable command ACK and `SUCCESS|FAILED` only
  after CP settlement. Stable `notification_id=UUIDv5(operation_id)` updates one
  Scylla activity/inbox row with monotonic `status_version` and preserves `read_at`.
* **Boundary:** XADD/Notification/Centrifugo are delivery/projection, not rollback of
  business transaction. No Managed Service runtime event/channel is added.
* **Acceptance:** terminal without observed processing still upserts same identity;
  duplicate/reconnect does not create activity row per attempt/status.

### MS-085 — Reconcile planning and exact-operation redispatch

* **Owner:** JO reconciliation owner + Dataplane executor owner.
* **Scope:** bounded per-Zone/per-instance scan for missing result, pending outbox,
  Zone reconnect or observed mismatch. It uses lease/fence, cursor, small batch,
  deterministic jitter and work/CPU/API budget.
* **Invariant:** reconciler may redispatch exact current operation/generation or query
  protected Kubernetes readiness; it never creates revision, changes desired input,
  resurrects delete or promotes state without matching pending result/fence.
* **Acceptance:** cold start/rebalance/Zone outage test yields no reconciliation storm;
  stale worker/result cannot overwrite newer observed version.

### MS-086 — Safe connection output read workflow

* **Owner:** personal and tenant read slices.
* **Routes:** `GET .../instances/:code/connection` after P07 safe observed snapshot
  exists.
* **Rules:** return only output-contract allow-list (host/port/database/TLS name etc.);
  no Secret, token, password, raw URI with credentials, raw input or Kubernetes API
  fetch. Missing output is normal observed state, not a reason to query Kubernetes.
* **Acceptance:** output visibility/scope tests and proof that CP never decrypts
  envelope/read Kubernetes Secret.

### MS-087 — Result/reconcile integration and fault suite

* **Owner:** CP/JO/DP test owners.
* **Scope:** full command→apply→result fixture for personal and tenant, duplicate and
  late result, retry budget exhaustion, partial delete/finalizer, lost result,
  reconciliation, hard-delete/code reuse and timeline upsert.
* **Exit evidence:** durable state remains correct across process restarts; no manual DB
  repair is required for normal recovery; result path has trace from HTTP operation to
  terminal projection.

## 13. P08 — Cloud Console customer experience và SRE Admin completion

**Mục tiêu:** expose only durable desired/observed truth and approved actions; Console
does not become an authorization, runtime or secret-bearing client.

**Dependency:** P02/P03 API contracts, P04 mutation, P07 result/timeline. Mock work
may start earlier but production wiring waits for P07.

**Exit gate:** SRE can manage catalog via Admin UI and customer can operate a managed
instance through Console without raw YAML/input/Secret or optimistic runtime fiction.

### MS-090 — SRE Admin catalog UI completion

* **Owner:** Admin UI owner.
* **Scope:** category/definition/version/blueprint/draft/revision detail, validate,
  publish/default, retire/audit and in-use critical-action handoff.
* **Acceptance:** UI route selects normal versus `/admin/critical/...` only after
  backend taxonomy; critical proof is newly acquired and bound by ACR, never stored in
  client cache/log.

### MS-091 — Customer list, create and review experience

* **Owner:** Cloud Console managed-services feature owner.
* **Scope:** table-first list/quick detail, `/managed-services/new` catalog/configure/
  review, immutable code + display name, read-only workspace/Zone/revision context.
* **Acceptance:** submit duplicate intent safely rehydrates original operation; no
  optimistic `READY`, no manual Zone selector, no raw parameter echo in durable UI
  surface.

### MS-092 — Instance detail and action workflow

* **Owner:** Console feature owner.
* **Scope:** Overview, Configuration, safe Connection and Operations/activity tabs;
  rename, config update, delete and retry actions only when API exposes permission/
  lifecycle-compatible action.
* **Acceptance:** configuration update asks for new input per pinned schema rather than
  prefill ciphertext; delete terminal failure shows `DELETING` + retry delete, never a
  fake rollback to active.

### MS-093 — Realtime notification rehydration

* **Owner:** Console realtime owner.
* **Scope:** use existing central `job.notification` path; fence
  `(notification_id, status_version)` then invalidate/refetch scoped list/detail/
  operation queries. Do not create module-local WebSocket/Centrifugo/NATS adapter.
* **Acceptance:** duplicate/out-of-order/reconnect terminal notification rehydrates
  authoritative API; timeline status update does not create browser activity duplicates.

### MS-094 — Console privacy, responsiveness and accessibility suite

* **Owner:** Console quality owner.
* **Scope:** mobile/desktop layout, form keyboard/error behavior, stale scope clear,
  sensitive DOM/storage scan, trusted scope cache keys and failure/retry UX.
* **Acceptance:** no input/secret persists in browser storage or telemetry; slow/offline
  request handling explains accepted/unknown state without retrying non-idempotent HTTP
  mutation automatically.

## 14. P09 — Zone-local customer observability read path

**Mục tiêu:** customer xem metrics/logs của đúng managed instance qua two existing Zone
gateways và Victoria, nhưng path này không ảnh hưởng command/lifecycle/timeline.

**Dependency:** P01 Zone-binding/telemetry foundation, P06 protected Kubernetes
metadata, P07 durable instance scope. Public enablement waits for P10 security/drill.

**Exit gate:** a scoped browser session can open a bounded read-only stream for one
authorized instance; forged scope/raw query/slow client/Victoria outage fail safely.

### MS-100 — OTel Collector managed-service metadata enrichment

* **Owner:** Zone observability owner.
* **Scope:** read trusted Kubernetes annotations/labels and overwrite workload-supplied
  telemetry attributes `aurora_workspace_id`, `aurora_owner_id`,
  `aurora_managed_service_instance_id`, `aurora_component_id`.
* **Boundary:** attributes exist only in customer Victoria read plane; never platform
  health/edge/JO/DP metric labels, global alert aggregation or Kubernetes traffic policy.
* **Acceptance:** spoofed workload OTLP attribute is overwritten; query filter fixture
  cannot fan out outside one verified owner/workspace/Zone/instance.

### MS-101 — Generic scoped observability ticket preparation

* **Owner:** ACR/Zone Control Edge/Control Authorizer owners.
* **Scope:** reuse the generic assertion/ticket profile rather than invent a Managed
  Service gateway or security principal. Ticket audience is
  `zone-public-edge-gateway`, capability `observability.read`, TTL 5 minutes.
* **Scope binding:** `jti`, actor/auth generation, owner/workspace/Zone/instance,
  allowed component/panel, method/path, policy revision and expiry. Zone Control
  Authorizer remains sole Zone assertion verifier.
* **Acceptance:** missing/expired/mismatched assertion or access projection fails close;
  replay can at most open a quota-bounded second connection, never invoke business
  mutation.

### MS-102 — Zone Public Edge observability stream route

* **Owner:** Zone Public Edge/Envoy owner.
* **Scope:** named public stream route does one authorizer check at connection open,
  strips browser ticket/all caller-provided scope headers, injects trusted scope only to
  the stream service and disables automatic upstream retry.
* **Limits:** 5-minute maximum stream, bounded connections/pending requests/bytes/
  in-flight work; auth/workspace/Zone/instance/policy scope change closes connection.
* **Acceptance:** Public Edge has no Victoria/NATS/Kafka/Zone KV credential and cannot
  forward raw client owner/namespace/selector/query headers.

### MS-103 — `zone-observability-stream` Rust subproject

* **Owner:** Zone observability service owner.
* **Scope:** create separate root Rust project/Deployment with read-only egress only to
  Zone VictoriaMetrics/VictoriaLogs. It accepts trusted injected scope plus bounded
  `panel_id`, optional allowed component and time range/cursor.
* **Query model:** service derives allow-listed fixed Victoria query, appends scope
  filters, rejects raw PromQL/LogsQL/metric/label/namespace/upstream URL and never
  persists raw log/metric/customer payload.
* **Backpressure:** metrics may coalesce; slow logs client is cancelled/closed; client
  disconnect cancels upstream query/tail. Service has no CP DB, Kafka, NATS, Redis,
  Zone KV, Kubernetes API or Vault credential.
* **Acceptance:** unit/query-shaping/security tests plus load test prove bounded RAM,
  no cross-instance read and retryable outage without fake `SUCCESS`/`READY`.

### MS-104 — Victoria retention, access audit and alert policy

* **Owner:** Zone observability + security owners.
* **Scope:** set V1 customer retention metrics 7 days/logs 3 days and series budget;
  audit only stream open/deny/close with bounded scope/panel/outcome.
* **Invariant:** no raw query/log/sample/ticket/secret/raw parameter in audit/access
  logs/metric labels. Alerts distinguish Victoria/adapter/Public Edge outage,
  authorizer reject and stream backpressure.
* **Acceptance:** retention/series settings are Zone-local; observability access cannot
  become billing/authorization/completion evidence.

### MS-105 — Console metrics/logs panels

* **Owner:** Cloud Console + Zone edge integration owner.
* **Scope:** feature obtains generic scoped ticket, opens separate SSE/read transport,
  renders fixed panel allow-list and cancels/reacquires stream on scope/auth change.
* **Boundary:** stream data is never merged into desired/operation cache or notification
  timeline. Browser has no Victoria credentials and no raw query UI.
* **Acceptance:** reconnect after ticket expiry, slow/closed stream and Zone outage show
  diagnostic unavailable/stale state only; Console continues to use CP API for durable
  operation truth.

## 15. P10 — Verification, failure drills, staging pilot và release gate

**Mục tiêu:** prove the system recovers under at-least-once, failover and backpressure
conditions before enabling a limited pilot. This phase is not a code-cleanup bucket.

**Dependency:** P02–P09 implementation gates. Pilot service/Zone selection occurs only
inside this phase and cannot change contract.

**Exit gate:** S15–S17 evidence in `STAGING.md` is complete, selected pilot has passed
create/update/delete/retry/reconcile and there is no unresolved P0/P1 security,
data-loss or duplicate-side-effect defect.

### MS-110 — Cross-service contract and compatibility suite

* **Owner:** CP/JO/DP release owner.
* **Scope:** run protobuf compatibility/byte identity, migration fresh/rollback,
  route registry, Kafka ACL/topic, HTTP contract, catalog revision hash and Zone-bound
  envelope fixtures in CI.
* **Acceptance:** an older non-Managed-Service workload still uses existing wire
  behavior; additive managed-service fields do not break existing consumers; no
  checked-in contract copies drift.

### MS-111 — End-to-end personal and tenant staging suite

* **Owner:** module E2E owner.
* **Scope:** two ownership branches each execute catalog discovery → create → command
  → render/apply → result → detail/timeline → config update → delete → hard-delete
  code reuse.
* **Acceptance:** desired/observed/operation separation is visible at each checkpoint;
  DP has no CP DB access; output connection excludes Secret and browser never sees raw
  input.

### MS-112 — Failure and chaos drill matrix

* **Owner:** SRE/CP/JO/DP owners.
* **Drills:** kill CP after DB commit; kill JO after Kafka ACK/before LSN; duplicate
  CDC/Kafka/result; restart before retry `available_at`; Zone delay/down; K8s outage;
  partial apply; foreign object; stuck delete finalizer; malformed Zone-bound envelope;
  full retry/DLQ; Notification/Redis stream issue; Victoria/Public Edge outage.
* **Acceptance:** each drill names expected durable state, retries/DLQ, alert/trace,
  owner and recovery path. No normal-flow drill needs direct DB modification or an
  unsafe cache restore.

### MS-113 — Security and privacy review gate

* **Owner:** security/ACR/Zone runtime owners.
* **Scope:** threat-model review for forged Envoy context, cross-owner/workspace/Zone
  access, stale proof/ticket, critical catalog proof, envelope replay/Zone binding,
  template injection, foreign K8s object, Secret exfiltration, log/audit leakage and
  telemetry query escape.
* **Acceptance:** negative tests prove fail-close boundary behavior; required RBAC,
  NetworkPolicy, Kafka ACL and service identities match God View connection matrix.

### MS-114 — Capacity, backpressure and graceful shutdown gate

* **Owner:** platform runtime owners.
* **Scope:** load test CP CTE contention, JO CDC/Kafka lag, due retry scan, DP worker
  pool/Kubernetes API, Notification timeline stream and observability connection/query
  load. Exercise pod termination/PDB/HPA/rebalance.
* **Acceptance:** bounded queues/semaphores, per-instance ordering, jittered retry,
  no global hot lock/no unbounded RAM, readiness drain and metric/alert thresholds are
  measured. Throughput limits fail retryably rather than corrupting desired state.

### MS-115 — Pilot selection and runbook

* **Owner:** SRE principal + release owner.
* **Selection criteria:** stable image/bundle, namespaced graph with few objects,
  explicit readiness/cleanup, no complex data migration, no unstable secret topology,
  staging Zone with capacity/key/RBAC/observability evidence.
* **Runbook:** catalog hidden → internal SRE → one staging Zone → bounded customer
  scope → observe → expand Zone → public. Include dashboard links, alert owner,
  customer support path, pause conditions and exact rollback trigger.
* **Acceptance:** selected service/Zone is recorded without changing BlueprintRevision
  immutability or dynamically widening user permission.

### MS-116 — Rollback and release procedure

* **Owner:** release owner.
* **Rollback principles:** stop new create/update first; keep JO/DP reconcile for
  existing pinned instances; preserve command/result schema compatibility; do not delete
  pinned revision, instance revision or evidence just to roll back
  code.
* **Procedure:** disable catalog visibility/admission → drain API mutation → pause new
  dispatch only if safe → keep durable outbox/result recovery observable → deploy
  compatible rollback → reconcile existing operations → verify no stranded `DELETING`/
  `PROVISIONING` state.
* **Acceptance:** rollback rehearsal proves existing resource is not orphaned, code is
  not prematurely reusable, and no duplicate K8s graph is created on re-enable.

### MS-117 — Release evidence packet and sign-off

* **Owner:** module release owner.
* **Contents:** task checklist, test reports, migration evidence, Kafka/ACL proof,
  security review, drill outputs, capacity data, pilot outcome, rollback rehearsal,
  God View diff and known non-blocking limitations.
* **Acceptance:** all P10 tasks have evidence links/owners; any failed/retryable drill
  is explicitly resolved or blocks rollout. Only then may release status change from
  planned/staged to pilot-enabled.

## 16. Cross-phase test ownership matrix

| Test layer | Primary owner | Must prove |
| --- | --- | --- |
| Controlplane unit | Managed Service module | handler-only validation, taxonomy → HTTP/gin.H, entity isolation, no duplicate validation in service/repo |
| PostgreSQL integration | Managed Service module | CTE lock/order, constraints, outbox atomicity, personal/tenant separation, hard delete/fence/retention |
| JO transport | JO | WAL replay, command key/order, checkpoint after ACK, delayed retry, route/DLQ/Zone ACL |
| Dataplane unit/golden | DP | Zone-bound envelope validation, YAML AST/tag rejection, names/metadata, component order/readiness/taxonomy |
| Kubernetes sandbox | DP/SRE | RBAC/capability profile, SSA ownership, duplicate/partial/delete/finalizer/reconcile |
| Notification | JO/Notification | one stable timeline/inbox identity and monotonic status version across retry/replay |
| Console | Cloud Console/Admin UI | scoped cache, stale revision, privacy storage, action UX, reconnect rehydrate, responsive access |
| Zone observability | Zone edge/service | ticket scope/replay quota, query shaping, stream backpressure, no business-state mutation |
| E2E/chaos | release owner | personal + tenant end-to-end and all S15 failure semantics |

## 17. Phase gate checklist

| Gate | Required evidence before next phase |
| --- | --- |
| P00 → P01 | Managed Service lifecycle God View reviewed; exact outer/inner route fields and fixture vocabulary frozen. |
| P01 → P02/P04 | migrations fresh/rollback, trusted Zone binding, generated protobuf and Kafka ACL preparation verified. |
| P02 → P03 | published revision hash/default/retire/critical-in-use behavior is tested. |
| P03 → P04 | form/stale revision contract tests pass; UI cannot send trusted context/raw YAML. |
| P04 → P05 | personal/tenant CTE/outbox/dedupe/retry API integration tests pass with JO/DP down. |
| P05 → P06 | WAL→Kafka command replay/key/ACL/delayed retry/DLQ tests pass; route is no-NATS. |
| P06 → P07 | DP sandbox proves render/apply/delete result and duplicate/foreign/key failure semantics. |
| P07 → P08/P09 | result inbox/reconcile/timeline durable truth works for both ownership branches. |
| P08/P09 → P10 | UI and diagnostic stream respect scope/privacy; no diagnostic path mutates lifecycle. |
| P10 → pilot | all failure/security/capacity/rollback evidence reviewed; pilot service and Zone selected. |

## 18. Explicit non-goals for the first vertical slice

The first pilot must not expand scope with arbitrary Helm, raw customer YAML,
cluster-scoped customer object, customer-selected Zone, billing/quota, generic secret
Vault integration, arbitrary Secret output, automatic catalog upgrade, user runtime
stream over NATS/Centrifugo, or a third Zone gateway. Those require a new staged
contract after this plan, not a convenience task inside any existing phase.

## 19. Recommended first execution batch

After this plan is approved, start only these tasks in order:

1. `MS-000` — dedicated lifecycle God View.
2. `MS-010` — six baseline migrations.
3. `MS-012` — trusted Zone binding contract.
4. `MS-013` — byte-compatible protobuf baseline.
5. `MS-020` through `MS-024` — catalog/draft/publish only.

Do **not** start customer create, JO route enablement or DP Kubernetes executor until
the relevant phase gate is met. This keeps the first code changes reviewable and stops
a half-built controlplane from emitting command shape that Dataplane cannot yet fence.
