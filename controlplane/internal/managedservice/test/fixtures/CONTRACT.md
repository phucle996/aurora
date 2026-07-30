# Managed Service Fixture Contract

> **Test location:** `internal/managedservice/test/fixtures`; this document follows
> the same test-boundary layout as IAM and remains non-runtime documentation.

> **Status:** P00 vocabulary only. These are stable cross-service test identities and
> expected semantics, not a shared runtime fixture package or a permission to create
> database/Kafka/Kubernetes state.

Each CP, JO, DP and Console test suite materializes its own local data from this
contract. No package may expose a generic fixture builder, generic mutator or shared
business helper across ownership/workflow boundaries.

## 1. Stable identity registry

All UUIDs below are valid fixed UUID values. Test code must use the exact value for the
named fixture, never generate one at runtime. Byte-oriented protobuf fixtures use the
16 raw UUID bytes of these values.

| Fixture | Fixed value | Meaning |
| --- | --- | --- |
| `PERSONAL_USER_A` | `10000000-0000-4000-8000-000000000001` | Personal owner and authenticated actor |
| `TENANT_A` | `10000000-0000-4000-8000-000000000002` | Tenant owner of tenant workspace |
| `TENANT_MEMBER_A` | `10000000-0000-4000-8000-000000000003` | Authorized tenant actor, distinct from tenant ID |
| `PERSONAL_WORKSPACE_A` | `10000000-0000-4000-8000-000000000011` | Workspace owned by `PERSONAL_USER_A` |
| `TENANT_WORKSPACE_A` | `10000000-0000-4000-8000-000000000012` | Workspace owned by `TENANT_A` |
| `PERSONAL_WORKSPACE_B` | `10000000-0000-4000-8000-000000000013` | Separate workspace used for cross-workspace denial |
| `ZONE_A` | `10000000-0000-4000-8000-000000000021` | Current Zone for success path |
| `ZONE_B` | `10000000-0000-4000-8000-000000000022` | Foreign Zone for trusted-binding mismatch tests |
| `CATEGORY_MESSAGING` | `10000000-0000-4000-8000-000000000031` | System catalog category |
| `DEFINITION_APACHE_KAFKA` | `10000000-0000-4000-8000-000000000032` | System catalog definition |
| `VERSION_KAFKA_3_8` | `10000000-0000-4000-8000-000000000033` | System catalog version |
| `BLUEPRINT_KAFKA_STANDARD` | `10000000-0000-4000-8000-000000000034` | Blueprint line |
| `BLUEPRINT_REVISION_PUBLISHED` | `10000000-0000-4000-8000-000000000035` | Published, provisionable revision |
| `BLUEPRINT_REVISION_RETIRED` | `10000000-0000-4000-8000-000000000036` | Retired revision; cannot be newly selected |
| `PERSONAL_INSTANCE_A` | `10000000-0000-4000-8000-000000000041` | Personal `orders-kafka` instance |
| `TENANT_INSTANCE_A` | `10000000-0000-4000-8000-000000000042` | Tenant `orders-kafka` instance |
| `INSTANCE_REVISION_A1` | `10000000-0000-4000-8000-000000000051` | Initial immutable configuration revision |
| `INSTANCE_REVISION_A2` | `10000000-0000-4000-8000-000000000052` | Pending update revision |
| `CREATE_OPERATION_A` | `10000000-0000-4000-8000-000000000061` | CREATE operation for `PERSONAL_INSTANCE_A` |
| `UPDATE_OPERATION_A` | `10000000-0000-4000-8000-000000000062` | UPDATE operation for `PERSONAL_INSTANCE_A` |
| `DELETE_OPERATION_A` | `10000000-0000-4000-8000-000000000063` | DELETE operation for `PERSONAL_INSTANCE_A` |
| `COMMAND_EVENT_A0` | `10000000-0000-4000-8000-000000000071` | Attempt 0 source command event |
| `COMMAND_EVENT_A1` | `10000000-0000-4000-8000-000000000072` | Attempt 1 retry command event |
| `RESULT_EVENT_A0` | `10000000-0000-4000-8000-000000000081` | Valid terminal result for attempt 0 |
| `RESULT_EVENT_STALE` | `10000000-0000-4000-8000-000000000082` | Validly encoded but stale/mismatched result |
| `RESULT_EVENT_DUPLICATE` | `10000000-0000-4000-8000-000000000083` | Same semantic source replay with a different result event |

## 2. Canonical catalog and blueprint vocabulary

| Contract item | Fixed value |
| --- | --- |
| Category code | `messaging` |
| Definition code | `apache-kafka` |
| Application version code | `3.8` |
| Blueprint code | `kafka-standard` |
| Published blueprint revision | `3` |
| Customer instance code | `orders-kafka` |
| Customer display name | `Orders Kafka` |
| Component 1 | `network-policy`, apply order `10`, delete order `30` |
| Component 2 | `service`, apply order `20`, delete order `20` |
| Component 3 | `workload`, apply order `30`, delete order `10` |
| Default namespace, personal | Derived from `PERSONAL_USER_A + PERSONAL_WORKSPACE_A` by canonical namespace formula |
| Default namespace, tenant | Derived from `TENANT_A + TENANT_WORKSPACE_A` by canonical namespace formula |

The three-component graph is only a fixture. A runtime SRE revision can declare a
different graph, but it must supply explicit component IDs, document allocation, apply/
delete order and readiness rules.

The fixture template uses only `!aurora/component` and exact typed
`!aurora/param` value nodes. It must not parameterize `apiVersion`, `kind`, metadata,
namespace, reserved annotation/label or generated resource name. It has no literal
`v1/Secret.data`/`stringData`.

P03 customer catalog/form tests materialize the following contract locally. It is a
transport fixture, not a shared runtime type:

```json
{
  "input_schema": {
    "fields": [
      {"key": "replicas", "value_type": "INT64", "cardinality": "ONE", "required": true, "mutable": true},
      {"key": "storage", "value_type": "BYTE_SIZE", "cardinality": "ONE", "required": true, "mutable": false},
      {
        "key": "exposure",
        "value_type": "ENUM",
        "cardinality": "ONE",
        "required": true,
        "mutable": true,
        "enum_values": ["private", "public"]
      },
      {"key": "allowed_cidr", "value_type": "CIDR", "cardinality": "ONE", "required": false, "mutable": true}
    ]
  },
  "ui_schema": {
    "groups": [{"key": "capacity", "label_i18n": {"en": "Capacity"}, "order": 10}],
    "fields": [
      {"key": "replicas", "group": "capacity", "widget": "NUMBER", "label_i18n": {"en": "Replicas"}, "order": 10},
      {"key": "storage", "group": "capacity", "widget": "TEXT", "label_i18n": {"en": "Storage"}, "order": 20},
      {"key": "exposure", "group": "capacity", "widget": "SELECT", "label_i18n": {"en": "Exposure"}, "order": 30},
      {"key": "allowed_cidr", "group": "capacity", "widget": "TEXT", "label_i18n": {"en": "Allowed CIDR"}, "order": 40}
    ]
  }
}
```

Customer fixture suites must also cover unknown widget, widget/type mismatch, stale
`expected_revision_id`, inactive catalog object, unpublished or retired revision,
foreign Zone allow-list, missing required Zone capability and personal/tenant scope
cross-over. Each case fails closed without returning YAML/component data or creating
business state, outbox, Kafka/NATS/Redis traffic or Kubernetes side effects.

## 3. Parameter/envelope/hash conventions

The P00 fixture never contains a real secret or a decryptable production envelope.

| Fixture | Value/constraint |
| --- | --- |
| Input fields | `replicas=3`, `storage=100Gi`, `exposure=private`, `allowed_cidr=10.0.0.0/8` |
| Canonical input shape | Flat typed map only; no `null`, nested map/object, raw YAML/JSON fragment or unknown key |
| Input limit exercise | 64 fields, 64 KiB canonical map, 4 KiB/string, 64 list items and 128 enum values are boundary cases |
| Zone binding | `ZONE_A` is persisted on the workspace and must match trusted Envoy context; `ZONE_B` must fail before durable intent or DP side effect |
| Envelope fixture | Opaque non-decryptable test bytes marked `fixture-envelope-v1`; tests assert opacity and Zone binding, never plaintext recovery |
| Hash convention | SHA-256 lower-case hex of each checked-in canonical fixture asset; test names use `bundle`, `component-contract`, `input` and `desired-spec` separately |

P01 introduces exact checked-in blueprint/input asset bytes and their expected SHA-256
digest values. A suite must derive digest from its exact local asset and compare it with
the documented asset expectation; it must not hard-code a fictitious digest unrelated
to the bytes it sends.

The Zone-bound envelope fixture binds:

```text
ZONE_A + PERSONAL_INSTANCE_A + CREATE_OPERATION_A + generation 1
+ INSTANCE_REVISION_A1 + bundle hash
```

Changing any one of Zone, instance, operation, generation, revision or bundle hash must
make validation fail closed. `attempt` is deliberately absent from the binding and from
external side-effect identity.

## 4. Required fixture scenarios and expected durable outcome

| Scenario | Setup | Expected durable outcome |
| --- | --- | --- |
| `create-success` | Personal A, Zone A, published revision, command attempt 0, graph ready | Instance `ACTIVE`; CREATE `SUCCEEDED`; active revision A1; one timeline row terminal success |
| `create-duplicate-intent` | Repeat exact canonical create with same workspace/code | Return same instance + CREATE operation; no second outbox/graph |
| `create-code-conflict` | Same workspace/code, different canonical intent | Stable conflict; existing desired state unchanged |
| `tenant-isolation` | Tenant A request against personal instance/workspace | Scoped denial/no row disclosure; no outbox |
| `zone-cross-wire` | Zone B topic/key/command for Zone A instance | DP rejects before Kubernetes side effect; quarantine/DLQ semantics |
| `published-vs-retired` | Select retired revision for new create | CP rejects; no revision/outbox created |
| `update-success` | Active A1, pending A2, matching expected generation | Only matching success promotes A2; old A1 remains until then |
| `update-terminal-failure` | Valid fenced terminal error for A2 | Instance stays `ACTIVE` on A1; pending A2 cleared; UPDATE terminal failed |
| `retryable-result` | Attempt 0 returns `K8S_API_UNAVAILABLE` | Same operation/generation/revision; retry outbox with persisted jittered `available_at`, command event A1/attempt 1 |
| `retry-budget-exhausted` | Attempt 4 returns retryable failure | Operation terminal failed; no sixth command |
| `duplicate-command` | Redeliver `COMMAND_EVENT_A0` after ACK/LSN crash | One Kubernetes graph/side effect convergence under same execution fence |
| `duplicate-result` | Reprocess result/source command | Inbox/timeline converge; no second operation transition or timeline row |
| `stale-result` | Mismatched source event, generation/revision/hash or attempt | Ignore + sanitized metric/audit; no desired/observed mutation |
| `foreign-object` | Same Kubernetes name with foreign ownership marker | Terminal `K8S_OWNERSHIP_CONFLICT`; never adopt/force over foreign object |
| `template-input-mismatch` | Missing/typed-incompatible tag value | Terminal `SRE_TEMPLATE_INPUT_MISMATCH`; no empty fallback |
| `delete-success` | Instance `DELETING`, all components/finalizers gone | Write deletion fence then hard-delete instance/revision; DELETE succeeds; code becomes reusable by new UUID |
| `delete-finalizer-stuck` | Workload/finalizer remains | Instance stays `DELETING`; DELETE terminal/retry state per taxonomy; no workspace namespace delete |

Each scenario is implemented independently in the owning test layer: CP CTE integration,
JO transport, DP golden/Kubernetes sandbox, Notification, Console and E2E. Shared names
and UUIDs do not authorize shared test setup helpers.

## 5. Fixture safety and lifecycle

* Fixture payloads, logs, screenshots and snapshots must never contain a real customer
  secret, Zone private key, decryptable envelope or raw rendered Secret.
* Test databases/Kafka topics/Kubernetes namespaces are ephemeral and unique per test
  run; fixture IDs are logical values, not a reason to share mutable runtime state.
* A test that later requires Zone-local encryption material creates a disposable secret
  only within its Zone test runtime. No public key metadata, keypair or rotation state
  belongs to this contract.
* No test marks a fixture as complete solely on Kubernetes apply ACK, notification
  delivery or Victoria telemetry. Completion uses the durable result settlement contract.
* Adding a fixture requires documenting owner, wire version, expected durable state,
  retry/DLQ disposition and cleanup semantics in this file and the relevant God View.
