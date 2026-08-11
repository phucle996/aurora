# Aurora Proto Contracts — Platform Transport Registry

> **Status:** `platform_transport.proto`, `managed_service.proto`, `zone_report.proto` and the
> storage metering report are canonical root
> sources. Mọi service phải generate binding từ registry này; không đặt `.proto`
> source trong subproject.
>
> This document is the canonical registry for the future inner Managed Service protobuf.
> It prevents JO/Dataplane copies from independently choosing package, field number or
> evolution policy.

## 0. Source layout and generated bindings

All `.proto` source belongs under this root registry. The first directory is the
workflow owner, not a generated-language target:

| Path | Source owner |
| --- | --- |
| `proto/{platform_transport,managed_service,zone_report,iam_auth}.proto` | Cross-service contracts |
| `proto/acr/` | ACR edge contracts |
| `proto/controlplane/` | Controlplane-owned contracts |
| `proto/dataplane/`, `proto/job-orchestrator/` | Zone durable transport owners |
| `proto/notification-service/`, `proto/cost-manager/` | Their respective Central workflow owners |

Rust consumers compile these sources to Cargo `OUT_DIR`; generated Rust bindings
are not committed. Generated Go `.pb.go` bindings remain in their import package,
but no `.proto` source is kept beside them. Moving a source file must preserve its
`package`, field numbers, reserved fields and wire-compatibility policy.

## 1. Canonical source ownership

| Concern | Decision |
| --- | --- |
| Canonical source path | `proto/platform_transport.proto`, `proto/managed_service.proto`, `proto/zone_report.proto` |
| Proto packages | `aurora.transport.v1`, `aurora.managedservice.v1`, `zone` |
| Canonical owners | Job Orchestrator + Dataplane transport owners; Controlplane owns business field semantics |
| Consumers | JO and Dataplane generate from the exact root source; Controlplane serializes/deserializes through the same generated contract binding |
| Source layout | Root registry nhóm theo workflow owner; subproject chỉ giữ generated binding cần cho ngôn ngữ của nó |
| Change control | Append-only field evolution; changes require CP + JO + DP review and descriptor compatibility evidence |

The platform outer command is now canonical too:

| Contract | Canonical source | Use |
| --- | --- | --- |
| `aurora.transport.v1.JobCommandV1` | `proto/platform_transport.proto` | JO → DP outer command envelope |
| `aurora.zone.transfer.v1.TransferGrantV1` / `TransferTicketV1` | `proto/zone/transfer_ticket.proto` | Control Authorizer → Zone Control → Public Edge ticket workflow |
| `aurora.storage.metering.v1.StorageUsageReportV1` | `proto/cost-manager/engine/storage_usage_report.proto` | Zone-local storage journal → Kafka → JO → Cost Engine settlement |
| `aurora.transport.v1.ProtectedPayloadV1` | `proto/platform_transport.proto` | Opaque CP outbox payload and byte-identical JO relay |
| `zone.ZoneReport` | `proto/zone_report.proto` | Dataplane key readiness and Zone telemetry report consumed by JO |
| `job_lifecycle.JobExecutionResultProto` | `proto/job-orchestrator/job_result.proto` and Dataplane-compatible result contract | DP → JO outer result envelope |

## 1.1 Generic metering event envelope

Structured metering events use one bounded envelope across Zone modules. The
envelope is an OTel log-attribute contract, not a universal billing table:

| Attribute | Contract |
| --- | --- |
| `log_type` | Exactly `metering` |
| `module` | Bounded module owner such as `storage`; never selected by a client |
| `metering_schema` | Versioned module event, for example `storage.access.completed.v1` |
| `event_id` | Stable producer event identity; storage maps the same value to its `request_id` dedup projection |
| `zone_id` | Injected/overwritten by the Zone collector from its runtime identity |
| `resource_id` | Trusted resource UUID when the module requires one |

The envelope filter is module-agnostic. Each module still owns its ClickHouse
projection, validation, report contract, retention and settlement semantics.
Unknown module/schema combinations remain raw telemetry and cannot enter a
module projection or mutate a wallet. `owner_id`, ticket material, cookies,
credentials, raw authorization and object paths are not envelope authority.

Every Zone-bound `JobCommandV1.payload` is serialized `ProtectedPayloadV1`. Controlplane
serializes one complete domain command then seals that complete byte slice. JO validates only
public envelope metadata and never decrypts; Dataplane opens it before decoding the domain
command. Field-level or nested payload encryption is forbidden.

`ProtectedPayloadV1` pins HPKE Base mode with X25519/HKDF-SHA256/AES-256-GCM. Its AAD is
versioned and contains `key_id`, recipient `zone_id`, `source_domain`, `job_topic`,
`resource_id`, `job_version` and `payload_schema_version`. Delivery attempt, trace context,
Kafka offset and reconcile generation are excluded so an at-least-once retry can reuse the exact
ciphertext without weakening route authentication. The canonical Go-seal/Rust-open vector is
hard-coded in the owner tests `controlplane/internal/security/job_payload_test.go` and
`dataplane/src/security/jobpayload.rs`; both values change together only with wire/AAD
compatibility evidence.

Private keys remain Zone-local in a read-only Dataplane mount. Controlplane stores only public
X25519 keys in Hierarchy. A key becomes producer-ready only after the Zone report proves every
fresh Dataplane replica loaded the same `key_id + public-key fingerprint`; stale readiness makes
new mutations fail closed. Retiring a key is rejected while any retained outbox/projection still
references it.

## 2. Route, topic and outer envelope registry

| Field | Pinned value |
| --- | --- |
| Source domain | `MANAGED_SERVICE` |
| Job topic | `managed_service.instance.execute` |
| Command topic | `aurora.jobs.commands.zone.<zone_uuid>.v1` |
| Result topic | `aurora.jobs.results.v1` |
| DLQ topic | `aurora.jobs.dlq.v1` |
| Command/result Kafka key | raw UUID `instance_id` |
| Outer `JobCommandV1.job_id` | raw UUID `command_event_id` |
| Outer `JobCommandV1.resource_id` | canonical UUID string `instance_id` |
| Outer `JobCommandV1.attempt` | bounded delivery attempt; no inner copy because retry reuses exact protected bytes |
| Outer `JobCommandV1.delivery_epoch` | manual replay cycle; automatic retries preserve it while changing only `attempt` |
| Outer `JobExecutionResultProto.job_id` | raw UUID `source_command_event_id` |
| Outer result status | `SUCCEEDED` for inner success; `FAILED` for inner retryable/terminal failure |
| Outer/inner schema version | `1` for the initial contract |
| Maximum plaintext payload | 1,000,000 bytes, enforced producer + consumer |
| Protected payload bound | 1,000,256 bytes, enforced JO + Dataplane before decode |

Managed Service never emits outer `PROCESSING`; JO creates the durable timeline
`PROCESSING` only after a command Kafka ACK. A result must be terminal and carry the
precise outcome in `ManagedServiceResultV1`.

## 3. Reserved protobuf declarations and field numbers

The following is the field-number authority for the canonical source. Numeric
fields may not be renumbered, retyped or reused. New optional fields append after the
current highest field number; removed fields are reserved permanently.

```proto
syntax = "proto3";

package aurora.managedservice.v1;

enum ManagedServiceOwnerTypeV1 {
  MANAGED_SERVICE_OWNER_TYPE_UNSPECIFIED = 0;
  MANAGED_SERVICE_OWNER_TYPE_PERSONAL = 1;
  MANAGED_SERVICE_OWNER_TYPE_TENANT = 2;
}

enum ManagedServiceOperationKindV1 {
  MANAGED_SERVICE_OPERATION_KIND_UNSPECIFIED = 0;
  MANAGED_SERVICE_OPERATION_KIND_CREATE = 1;
  MANAGED_SERVICE_OPERATION_KIND_UPDATE = 2;
  MANAGED_SERVICE_OPERATION_KIND_DELETE = 3;
  MANAGED_SERVICE_OPERATION_KIND_RESIZE = 4;
}

enum ManagedServiceOutcomeV1 {
  MANAGED_SERVICE_OUTCOME_UNSPECIFIED = 0;
  MANAGED_SERVICE_OUTCOME_SUCCEEDED = 1;
  MANAGED_SERVICE_OUTCOME_RETRYABLE_FAILURE = 2;
  MANAGED_SERVICE_OUTCOME_TERMINAL_FAILURE = 3;
}

enum ManagedServiceObservedStateV1 {
  MANAGED_SERVICE_OBSERVED_STATE_UNSPECIFIED = 0;
  MANAGED_SERVICE_OBSERVED_STATE_UNKNOWN = 1;
  MANAGED_SERVICE_OBSERVED_STATE_PROGRESSING = 2;
  MANAGED_SERVICE_OBSERVED_STATE_READY = 3;
  MANAGED_SERVICE_OBSERVED_STATE_DEGRADED = 4;
}

message ManagedServiceComponentV1 {
  string component_id = 1;
  repeated uint32 document_indexes = 2;
  uint32 apply_order = 3;
  uint32 delete_order = 4;
  string readiness_rule = 5;
  uint32 readiness_deadline_seconds = 6;
  reserved 7 to 15;
}

message ManagedServiceSafeObservedFieldV1 {
  string key = 1;
  oneof value {
    string string_value = 2;
    bool bool_value = 3;
    int64 int64_value = 4;
    string decimal_value = 5;
  }
  reserved 6 to 15;
}

message ManagedServiceCommandV1 {
  bytes command_event_id = 1;
  bytes operation_id = 2;
  bytes instance_id = 3;
  ManagedServiceOwnerTypeV1 owner_type = 4;
  bytes owner_id = 5;
  bytes workspace_id = 6;
  bytes zone_id = 7;
  string instance_code = 8;
  ManagedServiceOperationKindV1 operation_kind = 9;
  uint64 generation = 10;
  reserved 11;
  bytes instance_revision_id = 12;
  bytes blueprint_revision_id = 13;
  string template_yaml = 14;
  repeated ManagedServiceComponentV1 components = 15;
  bytes bundle_hash = 16;
  bytes component_contract_hash = 17;
  bytes input_hash = 18;
  bytes desired_spec_hash = 19;
  bytes parameter_values = 20;
  bytes parameter_values_sha256 = 21;
  uint32 schema_version = 22;
  int64 issued_at_unix_ms = 23;
  string traceparent = 24;
  string tracestate = 25;
  reserved 26 to 31;
}

message ManagedServiceResultV1 {
  bytes result_event_id = 1;
  bytes source_command_event_id = 2;
  bytes operation_id = 3;
  bytes instance_id = 4;
  bytes zone_id = 5;
  uint64 generation = 6;
  uint32 attempt = 7;
  bytes instance_revision_id = 8;
  bytes blueprint_revision_id = 9;
  bytes bundle_hash = 10;
  bytes component_contract_hash = 11;
  bytes input_hash = 12;
  bytes desired_spec_hash = 13;
  reserved 14;
  ManagedServiceOutcomeV1 outcome = 15;
  string error_code = 16;
  string sanitized_message = 17;
  ManagedServiceObservedStateV1 observed_state = 18;
  repeated ManagedServiceSafeObservedFieldV1 safe_observed_output = 19;
  uint64 observed_state_version = 20;
  uint32 schema_version = 21;
  int64 completed_at_unix_ms = 22;
  string traceparent = 23;
  string tracestate = 24;
  uint64 delivery_epoch = 25;
  reserved 26 to 31;
}
```

### 3.1 Field semantics and validation

| Field group | Contract |
| --- | --- |
| UUID fields | Exactly 16 raw bytes; UUID string encoding belongs only in log/API presentation, not protobuf payload |
| `owner_*`, `workspace_id`, `zone_id` | Snapshot from trusted Controlplane state; DP compares it against authenticated Zone/command route but never derives authorization from it |
| `generation`, revision IDs and hashes | Exact execution fence. Result must echo source command values; any mismatch is stale/quarantine, never a lifecycle mutation |
| `template_yaml` + `components` | Pinned immutable SRE artifact/contract only. It is not an HTTP catalog response and must hash-match `bundle_hash` + `component_contract_hash` |
| `parameter_values` | Canonical values are plaintext only inside the inner command; the complete serialized command is protected by outer `ProtectedPayloadV1` and opened only in Dataplane memory |
| `safe_observed_output` | Only output declared safe in the published revision. It cannot contain raw parameter, rendered YAML, Secret, credential-bearing URI or arbitrary provider payload |
| `traceparent`/`tracestate` | W3C context propagated from outer envelope; malformed context is rejected/sanitized by boundary policy, not reconstructed from user input |

`schema_version` must be `1` in both inner messages for V1. Delivery `attempt` exists only
in outer `JobCommandV1`; automatic retries preserve the exact inner ciphertext and
`delivery_epoch` while changing only `attempt`. Manual retry reuses the same event,
operation, generation and ciphertext but increments `delivery_epoch`; result echoes it so
an earlier attempt cycle cannot collide in the inbox. Neither outer field is part of
Kubernetes external-side-effect dedupe. `instance_id + operation_id + generation`
is the execution fence retained through the command/result/DLQ replay window.

Enum value `UPDATE=2` remains reserved for byte compatibility with the frozen V1 source;
new Controlplane workflows emit only `CREATE`, `RESIZE` and `DELETE` and expose no generic
configuration/runtime-metadata patch.

`error_code` is a stable taxonomy identifier. `sanitized_message` is bounded diagnostic
text only; it must not carry a Kubernetes object, provider response, parameter or
credential. `outcome=RETRYABLE_FAILURE` permits a new delayed command only while the
budget remains; at attempt 4 Controlplane settles terminal failure.

## 4. P01 generation and compatibility gate

This P01 change-set implements the following local gates:

1. Keep every `.proto` source under this root registry; generated bindings belong to their language consumer.
2. Make JO and Dataplane `build.rs` compile those root sources with the root include path.
3. Add Controlplane binding from the same canonical contract rather than a
   handwritten protobuf byte layout.
4. Keep a Controlplane reflection/round-trip test that pins parameter field numbers 20/21,
   reserved command field 11 and reserved result field 14; incompatible evolution fails.
5. Add JO and DP generated-binding round-trip tests using the same fixed Zone-bound
   command fixture while route admission remains disabled.

P10 release CI still needs to compare generated descriptors and reject a local
`managed_service.proto` copy. That CI wiring is intentionally not replaced by a runtime
producer/consumer in P01.

P01 does not reserve a Kafka topic by deploying it, register a route or publish an
event. Those actions remain forbidden until P05, after P01 schema and P04 outbox
contracts have passed their gates.
