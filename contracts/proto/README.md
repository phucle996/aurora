# Aurora Proto Contracts — Managed Service Registry

> **Status:** P01 foundation. `managed_service.proto` is the canonical root source and
> JO/Dataplane build scripts generate its bindings. Route registration, producer/consumer
> activation and compatibility CI remain later-phase work.
>
> This document is the canonical registry for the future inner Managed Service protobuf.
> It prevents JO/Dataplane copies from independently choosing package, field number or
> evolution policy.

## 1. Canonical source ownership

| Concern | Decision |
| --- | --- |
| Canonical source path | `contracts/proto/managed_service.proto` |
| Proto package | `aurora.managedservice.v1` |
| Canonical owners | Job Orchestrator + Dataplane transport owners; Controlplane owns business field semantics |
| Consumers | JO and Dataplane generate from the exact root source; Controlplane serializes/deserializes through the same generated contract binding |
| Local copies | Forbidden in `job-orchestrator/proto/`, `dataplane/proto/` or any service-local `proto/` directory |
| Change control | Append-only field evolution; changes require CP + JO + DP review and descriptor compatibility evidence |

The existing outer envelopes are **not** moved by Managed Service P00:

| Outer contract | Current source | Managed Service use |
| --- | --- | --- |
| `aurora.transport.v1.JobCommandV1` | byte-identical `job-orchestrator/proto/platform_transport.proto` and `dataplane/proto/platform_transport.proto` | JO → DP outer command envelope |
| `job_lifecycle.JobExecutionResultProto` | `job-orchestrator/proto/job_result.proto` and Dataplane-compatible result contract | DP → JO outer result envelope |

Their existing platform ownership/migration is outside this module. Managed Service only
pins how it fills them; it does not copy or modify them in P00.

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
| Outer `JobCommandV1.attempt` | `0..4`; must equal inner attempt |
| Outer `JobExecutionResultProto.job_id` | raw UUID `source_command_event_id` |
| Outer result status | `SUCCEEDED` for inner success; `FAILED` for inner retryable/terminal failure |
| Outer/inner schema version | `1` for the initial contract |
| Maximum outer record/payload | 1,000,000 bytes, enforced producer + broker + consumer |

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
  uint32 attempt = 11;
  bytes instance_revision_id = 12;
  bytes blueprint_revision_id = 13;
  string template_yaml = 14;
  repeated ManagedServiceComponentV1 components = 15;
  bytes bundle_hash = 16;
  bytes component_contract_hash = 17;
  bytes input_hash = 18;
  bytes desired_spec_hash = 19;
  bytes parameter_envelope = 20;
  bytes parameter_envelope_sha256 = 21;
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
  reserved 25 to 31;
}
```

### 3.1 Field semantics and validation

| Field group | Contract |
| --- | --- |
| UUID fields | Exactly 16 raw bytes; UUID string encoding belongs only in log/API presentation, not protobuf payload |
| `owner_*`, `workspace_id`, `zone_id` | Snapshot from trusted Controlplane state; DP compares it against authenticated Zone/command route but never derives authorization from it |
| `generation`, revision IDs and hashes | Exact execution fence. Result must echo source command values; any mismatch is stale/quarantine, never a lifecycle mutation |
| `template_yaml` + `components` | Pinned immutable SRE artifact/contract only. It is not an HTTP catalog response and must hash-match `bundle_hash` + `component_contract_hash` |
| `parameter_envelope` | Opaque payload bound to the trusted `zone_id`; no per-Zone public-key/key-rotation record is carried by this contract. Neither JO, result, DLQ nor observability decrypts/logs it |
| `safe_observed_output` | Only output declared safe in the published revision. It cannot contain raw parameter, rendered YAML, Secret, credential-bearing URI or arbitrary provider payload |
| `traceparent`/`tracestate` | W3C context propagated from outer envelope; malformed context is rejected/sanitized by boundary policy, not reconstructed from user input |

`schema_version` must be `1` in both inner messages for V1. `attempt` must be
`0..4`, equal outer attempt and be correlated with `source_command_event_id`; it is not
part of Kubernetes external-side-effect dedupe. `instance_id + operation_id + generation`
is the execution fence retained through the command/result/DLQ replay window.

`error_code` is a stable taxonomy identifier. `sanitized_message` is bounded diagnostic
text only; it must not carry a Kubernetes object, provider response, parameter or
credential. `outcome=RETRYABLE_FAILURE` permits a new delayed command only while the
budget remains; at attempt 4 Controlplane settles terminal failure.

## 4. P01 generation and compatibility gate

This P01 change-set implements the following local gates:

1. Add only `contracts/proto/managed_service.proto`; do not add service-local copies.
2. Make JO and Dataplane `build.rs` compile that root source with root include path.
3. Add Controlplane binding from the same canonical contract rather than a
   handwritten protobuf byte layout.
4. Keep a Controlplane reflection/round-trip test that pins field numbers 20/21 and the
   reserved result field 14; incompatible descriptor evolution fails that test.
5. Add JO and DP generated-binding round-trip tests using the same fixed Zone-bound
   command fixture while route admission remains disabled.

P10 release CI still needs to compare generated descriptors and reject a local
`managed_service.proto` copy. That CI wiring is intentionally not replaced by a runtime
producer/consumer in P01.

P01 does not reserve a Kafka topic by deploying it, register a route or publish an
event. Those actions remain forbidden until P05, after P01 schema and P04 outbox
contracts have passed their gates.
