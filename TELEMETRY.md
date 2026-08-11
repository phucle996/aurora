# Aurora Telemetry Architecture

This document defines the platform-wide diagnostic contract. Individual service
implementations own their exact instruments in service-local `TELEMETRY.md`
files. Telemetry is never business state, command transport, authorization
proof, billing input, or completion evidence.

```text
Central applications -> NDJSON stdout -> Central collector -> VictoriaLogs
Zone applications    -> NDJSON stdout -> Zone collector    -> VictoriaLogs

Applications -> OTLP traces and metrics -> matching collector/backend
```

Each VictoriaLogs backend uses exactly one stream label:

```text
{service_name="<bounded service identity>"}
```

`service_instance_id`, pod/node/container identity, trace ID, operation/event
ID, actor, tenant, workspace, resource, Kafka coordinates, and operation are
searchable attributes when present, never stream labels. Unknown application
identity is routed to a bounded `service_name=infra` quarantine stream.

Canonical application fields are `service_name`, `service_version`,
`service_instance_id`, bounded `op`, bounded `event_code`, canonical
`severity_text`/`severity_number`, and normalized message body. Optional
correlation fields appear only at the relevant boundary; emit neither empty
sentinels nor synthetic default values.

Logs must redact credentials, cookies, tokens, protected payloads, raw customer
bodies, and secrets. Logger/collector queues are bounded; telemetry backpressure
may drop diagnostics but cannot block a durable database, Kafka, or Zone side
effect. Repeated warnings/errors are rate-limited or sampled, and hot polling,
health, heartbeat, and no-change loops are not event logs.

`trace_id` correlates a live distributed trace, `span_id` identifies one span,
`operation_id` persists across retry/reconcile, and `event_id` identifies an
at-least-once message. None provides exactly-once semantics or replaces durable
state.

