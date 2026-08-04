# Job Orchestrator Telemetry God View

## Scope

Job Orchestrator emits diagnostic logs and OTel traces for Central durability and
Central-to-Zone transport. It does not own Controlplane business state and its logs are
not a replacement for PostgreSQL outbox/result settlement.

## Log identity

Every platform log carries:

```text
service_name=aurora-job-orchestrator
service_version=<immutable build version>
service_instance_id=<process incarnation/boot id>
op=<bounded static operation>
event_code=<bounded lifecycle event>
severity_text + severity_number
```

`trace_id` and `span_id` come from the active OTel context. Durable async records also
carry `operation_id` and `event_id` when present. These fields are ordinary attributes,
not stream labels.

VictoriaLogs uses exactly one stream field:

```text
{service_name="aurora-job-orchestrator"}
```

Container ID, pod name, node name, Kafka offset, workspace ID and resource ID must never
be stream dimensions.

## Event volume

- Bootstrap/shutdown, fencing, decode rejection, durable publish, settlement, retry and DLQ
  transitions are retained.
- Successful polling, heartbeat, lease renew and no-change reconciliation are not logged.
- Transport fields are emitted only when the event has real transport coordinates; no `-1`,
  empty string or synthetic zero sentinel is serialized.
- Error cause is bounded and redacted. Protected payload bytes, secrets and broker credentials
  are never logged.

## Backpressure and failure

The bounded lossy application queue and persistent Collector queue remain diagnostic-only.
Collector or Victoria failure may drop logs, but cannot rollback PostgreSQL commit or delay
Kafka/result durability. Drop and suppression counters are exported through OTel metrics.

## End-to-end correlation

```text
Controlplane request
  -> outbox operation_id/event_id/traceparent
  -> JO dispatch span
  -> Kafka command
  -> Dataplane execution span
  -> Kafka result
  -> JO/Controlplane settlement
```

`trace_id` connects one live trace. `operation_id` remains the stable business identity
across retry/reconcile, and `event_id` remains the stable message identity for at-least-once
delivery and deduplication.
