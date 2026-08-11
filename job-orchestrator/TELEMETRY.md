# Job Orchestrator Telemetry

This document is the operational telemetry contract for Job Orchestrator. Logs,
metrics, and traces are diagnostic signals only: they must never delay,
substitute for, or roll back PostgreSQL/Kafka durability.

## Resource identity and transport

OTLP exports carry one Central process resource:

```text
service.namespace=aurora
service.name=aurora-job-orchestrator
service.version=<APP_VERSION or Cargo version>
service.instance.id=<boot UUID>
deployment.environment.name=<DEPLOYMENT_ENVIRONMENT or APP_ENV>
aurora.component.scope=central
host.name=<node hostname>
process.pid=<process PID>
```

`service.instance.id` is a boot UUID, not a pod name. A Zone is a job/span
attribute, never a JO resource attribute. OTLP is explicitly enabled with
`OTEL_ENABLED=true`; the endpoint, TLS trust, client identity, sampling, queue,
and export bounds are validated by `src/config/otel.rs`. Exporter failure loses
diagnostics only.

## Structured logs

The logger writes NDJSON to stdout through a bounded lossy non-blocking queue.
Every record includes the following stable fields:

| Field | Meaning |
|---|---|
| `level`, `service_name`, `service_version`, `service_instance_id` | Process identity and severity |
| `op`, `event_code`, `outcome` | Bounded operation, event taxonomy, and result |
| `trace_id`, `span_id` | Active OpenTelemetry correlation when a span exists |
| `event_id`, `operation_id` | Stable durable event and business-operation correlation |
| `source_domain`, `job_version` | Bounded domain and contract version |
| `kafka_topic`, `kafka_partition`, `kafka_offset` | Transport coordinates when applicable |
| `retryable`, `duration_ms`, `suppressed_count` | Retry, duration, and warning/error coalescing evidence |

`job_log_with_fields` additionally emits `job_id`, `job_topic`, and `attempt`.
The log stream has exactly one VictoriaLogs label:

```text
{service_name="aurora-job-orchestrator"}
```

No tenant, workspace, user, resource, Kafka coordinate, node, container, or
trace value is a stream label. Fields are bounded by `APP_LOG_MAX_FIELD_BYTES`
(default 16 KiB), and messages/fields matching credentials, tokens, passwords,
or URL userinfo are replaced with `[REDACTED_SENSITIVE_LOG_FIELD]`.

Warnings and errors are rate-limited by `(op, event_code, error)` with a bounded
2,048-key table. The default window is five seconds and the next emitted record
reports `suppressed_count`. Successful poll, heartbeat, lease-renewal, and
no-change reconciliation events must not become routine logs.

## Metrics

All instruments use meter `aurora-job-orchestrator`. The following names and
attribute sets are the current contract:

| Instrument | Type | Attributes |
|---|---|---|
| `job_orchestrator_wal_records_accepted_total` | Counter | none |
| `job_orchestrator_kafka_commands_published_total` | Counter | none |
| `job_orchestrator_results_received_total` | Counter | none |
| `job_orchestrator_notifications_enqueued_total` | Counter | none |
| `job_orchestrator_record_outcomes_total` | Counter | `record_type`, `outcome` |
| `job_orchestrator_kafka_operations_total` | Counter | `operation`, `outcome` |
| `job_orchestrator_kafka_operation_duration_seconds` | Histogram | `operation` |
| `job_orchestrator_worker_terminations_total` | Counter | `worker` |
| `job_orchestrator_managed_service_outbox_age_seconds` | Histogram | `source_domain=MANAGED_SERVICE` |
| `job_orchestrator_changefeed_lag_bytes` | Histogram | `source_domain=MANAGED_SERVICE` |
| `job_orchestrator_managed_service_pending_outbox_records` | Histogram | `source_domain=MANAGED_SERVICE` |
| `job_orchestrator_logs_dropped_total` | Counter | none |
| `job_orchestrator_logs_suppressed_total` | Counter | none |
| `job_orchestrator_logs_emitted_total` | Counter | none |

The pipeline sampler exports logger counter deltas every ten seconds. A restart
is disambiguated by `service.instance.id`; counters must not be interpreted as a
single process lifetime across boot IDs. `record_type`, `outcome`, `operation`,
and `worker` are static bounded vocabularies. Do not add IDs, topic names,
tenant/workspace/user/resource values, errors, or offsets as metric labels.

## Tracing and propagation

JO uses W3C `traceparent` and `tracestate`, bounded respectively to 128 and 512
bytes. Invalid or partial context creates no remote parent. Span names are
bounded to 128 bytes; spans accept at most 32 attributes and 32 events.

```text
PostgreSQL outbox/WAL
  -> Producer span: send <zone topic>
  -> Kafka JobCommandV1 traceparent/tracestate
  -> Zone execution trace
  -> Kafka result traceparent/tracestate
  -> Client span: verify job result authority
  -> Client span: apply controlplane job result
  -> Producer span: stream:{resource_ownership} or job notification
```

Current span attributes use OpenTelemetry messaging/database conventions plus
bounded Aurora identifiers:

```text
messaging.system
messaging.operation.type
messaging.destination.name
messaging.message.id
db.system
db.operation.name
aurora.job.id
aurora.job.topic
aurora.source.domain
aurora.zone.id
aurora.reconcile.generation
```

The changefeed creates a producer span before Kafka publication and injects its
context into `JobCommandV1`. Result consumption extracts the returned context,
creates an authority-check span, then a Controlplane settlement span. Mail
reconciliation and ownership publication create new producer spans and inject
their contexts into their respective Kafka/Redis messages.

`operation_id` remains the durable business correlation across retries and
reconcile. `event_id`/`job_id` remains the stable message identity. Kafka
topic/partition/offset explain one delivery attempt; they are not a replacement
for either durable identity. A trace can be sampled out while logs and metrics
remain available.

## Failure signals and response

| Condition | Primary signal | Durable interpretation |
|---|---|---|
| Invalid WAL or result contract | `record_outcomes_total` rejected plus structured quarantine log | Source is committed only after durable DLQ/quarantine action |
| Kafka publish failure | Kafka operation outcome, producer span error, error log | WAL/repair marker cannot advance; replay is expected |
| Authority or settlement failure | Result failure outcome, settlement span error, error log | Kafka offset remains unsettled for retry |
| Notification/ownership enqueue failure | Failed outcome and producer span error | Business result is already durable; follow-up repair/redelivery applies |
| Worker terminal exit | `worker_terminations_total{worker}` and terminal log | Process treats it as fatal and begins bounded shutdown |
| Log queue loss/suppression | `logs_dropped_total` or `logs_suppressed_total` | Diagnostic loss only, never a business failure signal |
| OTLP exporter failure | Bootstrap/shutdown error log | Diagnostic export degraded only |

Alert on sustained Kafka failures, rising changefeed age/backlog, result
rejection/DLQ growth, and any worker termination. Alerting on log loss detects
observability degradation but must not page as a data-loss claim without the
durability signals above.

