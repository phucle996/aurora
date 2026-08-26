# Dataplane Telemetry

This is the operational telemetry contract for the Zone Dataplane process. Logs,
metrics, and traces are diagnostic signals only. They must never replace Kafka
settlement, Zone KV coordination, or another durable business boundary.

Job Orchestrator is a separate Central process and owns
[its telemetry contract](../job-orchestrator/TELEMETRY.md). The customer-facing
Victoria read-plane is a separate service documented in
[zone-runtime-stream](../zone-runtime-stream/README.md).

## Runtime topology

~~~text
Tokio task
  | typed tracing event                         trace and metric instruments
  +--> bounded non-blocking logger              +--------------------------+
  |        |                                    |                          |
  |        v                                    v                          v
  |   one stdout NDJSON stream             OTel spans                 OTel metrics
  |        |                                    |                          |
  |        v                                    +-------------+------------+
  |   Docker JSON log wrapper                                |
  |        |                                                  v
  |        v                                           Zone OTel Collector
  |   filelog receiver                                      |
  |        |                                    +-------------+-------------+
  |        +--> Zone VictoriaLogs                  |                           |
  |                                                 v                           v
  |                                           trace backend              Zone VictoriaMetrics

NodeRuntimeSampler
  -> immutable in-memory NodeRuntimeSample
  -> local admission and Zone Control scaling input
  -> Zone Health KV snapshot
  -> OTel gauges
~~~

Dataplane installs one tracing subscriber and one stdout stream.
tracing-subscriber serializes NDJSON; call sites never assemble JSON strings.
tracing-appender writes through a bounded lossy queue so a slow stdout collector
cannot block a Tokio worker. Queue loss is recorded by
dataplane_logs_dropped_total and does not delay execution, result publication,
or Kafka settlement.

The application does not export the same stdout event through OTLP Logs. The
collector owns stdout ingestion, checkpointing, and forwarding. Development uses
a Zone-local Collector and Zone Victoria storage; Dataplane never falls back to
the Central Collector.

## Identity, correlation, and confidentiality

Every OTLP resource contains:

~~~text
service.namespace=aurora
service.name=aurora-dataplane
service.version=<APP_VERSION or Cargo version>
service.instance.id=<boot UUID>
deployment.environment.name=<DEPLOYMENT_ENVIRONMENT or development>
aurora.zone.id=<configured Zone UUID>
host.name=<node hostname>
~~~

The boot UUID identifies one process incarnation. It does not establish a
global ordering across nodes and is distinct from the durable event ID.

Logs retain the same service identity plus event-specific fields only when they
exist:

| Field group | Fields |
| --- | --- |
| Event | op, event_code, outcome, retryable, duration_ms |
| Trace | trace_id, span_id |
| Durable correlation | event_id, operation_id, job_version |
| Kafka delivery | kafka_topic, kafka_partition, kafka_offset, assignment_epoch |
| Fencing | fencing_token, assignment_epoch, runtime_generation, slot |

A DLQ event ID is deterministically derived from source topic, partition,
offset, and error code. It remains stable across a publish-before-settle replay.
DLQ telemetry stores a bounded/redacted error, payload length, and SHA-256
fingerprint, never the raw command.

Raw payloads, encrypted envelopes, credentials, authorization headers, customer
secrets, recipient addresses, message bodies, and raw infrastructure endpoints
are forbidden from logs and spans. APP_LOG_MAX_FIELD_BYTES bounds serialized
string fields without cutting UTF-8 code points. Configuration that contains
secrets must not derive or emit Debug output.

## Structured logging

~~~text
tracing call site
  -> one JSON subscriber
  -> bounded lossy writer queue
  -> stdout NDJSON
  -> Docker JSON file
  -> Zone Collector filelog receiver
  -> Zone VictoriaLogs
~~~

A full queue drops the log and increments a pipeline counter. Warning and error
events are rate-limited by bounded operation/event/error state; a later emitted
record includes suppressed_count. Routine successful polls, heartbeats, lease
renewals, and no-change ticks must not be logged.

Required diagnostic events are:

1. Bootstrap, shutdown, and telemetry-pipeline health.
2. Assignment epochs, worker scale application, and mail-runtime state transitions.
3. Kafka contract rejection, durable DLQ publication, and source settlement.
4. Zone KV failures that can leave health, snapshot, or scale state stale.
5. External infrastructure transition, timeout, and recovery.

Log records are at-least-once diagnostics, not durable state. The collector
uses persistent fingerprint/offset checkpoints for the canonical Docker log
path. A malformed JSON-looking record is marked as a parse error rather than
silently treated as a valid structured record.

## Metrics and local runtime sampling

~~~text
cgroup v2 (/proc fallback) --+
Kafka lag cache -------------+--> NodeRuntimeSampler every 5 seconds
Worker registry -------------+              |
                                             +--> ArcSwap NodeRuntimeSample
                                             |       |          |
                                             |       |          +--> local admission
                                             |       +-------------> Zone Health KV zone.node.<node_id>
                                             +---------------------> OTel gauges

Zone Control scale worker <------------------ Zone Health KV
  -> assignment-fenced, resource-aware scale directive -> worker scale follower
~~~

There is one sampler per Dataplane process. It produces an immutable snapshot
of CPU, memory, throttling, working set, active/starting/ready/draining workers,
admitted jobs, Kafka lag, freshness, validity, and loaded payload-key readiness.

Admission reads that RAM snapshot directly. It never reads the Collector.
The sampler writes the same snapshot to Zone Health KV, where the assigned Zone
Control scale worker aggregates node health. OTel is an export path only.

A sample is stale when it is invalid, has a future timestamp, or is older than
15 seconds. Admission then fails closed instead of treating unreadable cgroup
data as idle capacity. Zone Control keeps its current scale target rather than
scaling blindly from stale input.

| Metric family | Purpose | Bounded dimensions |
| --- | --- | --- |
| dataplane_node_* | CPU, memory, throttling, working set, sample age/validity | zone_id |
| dataplane_worker_slots | worker state inventory | zone_id, state |
| dataplane_stream_lag | Kafka lag snapshot | zone_id |
| dataplane_job_execution_* | execution latency and completed attempts | stable topic/status taxonomy |
| dataplane_watchdog_* | active locks, queue depth, bounded watchdog events | stable event taxonomy |
| dataplane_kafka_unsettled_records | completed records awaiting settlement | zone_id |
| dataplane_logs_emitted_total | structured log throughput | none |
| dataplane_logs_suppressed_total | rate-limiter visibility loss | none |
| dataplane_logs_dropped_total | bounded queue visibility loss | none |
| aurora_runtime_health | customer-facing Mail slot state in Zone Victoria only | consumer, owner, workspace, local Zone, slot |
| aurora_runtime_metric | customer-facing Mail consumer lag in Zone Victoria only | consumer, owner, workspace, local Zone, slot |

Operational `dataplane_*` metrics may not contain a tenant/workspace/user/resource
ID, job/event ID, Kafka coordinate, raw error text, payload, recipient or
endpoint. The two `aurora_runtime_*` families are a separate Zone-local customer
read contract: they may carry only the bounded registration dimensions listed
above, never leave the Zone Victoria boundary, and are always queried through
the signed Zone runtime scope.

## Tracing and propagation

Dataplane uses W3C traceparent and tracestate. Both are bounded to 128 and 512
bytes. It intentionally refuses baggage at the JO-to-Dataplane security
boundary: customer-controlled keys must not become platform telemetry.

~~~text
Controlplane outbox
  -> Job Orchestrator producer span
  -> Kafka JobCommandV1 with trace context
  -> Dataplane consumer span
  -> executor client spans: Zone KV, JMAP, Proxmox, S3, STS
  -> Dataplane producer span: result, retry, or DLQ
  -> Kafka result topic
  -> Job Orchestrator consumer and durable settlement spans
~~~

A valid parent context continues the trace. A missing legacy context creates a
new root consumer trace. Invalid or oversized context is rejected or quarantined
according to the message contract; it must not become a fake remote parent.

A job span reaches OK only after its terminal result or retry is durable and
the source Kafka offset is settled. Executor success with failed result
publication or settlement is an error. Kafka redelivery creates a new consumer
span because it is a real execution attempt; durable job ID, version, attempt,
and delivery coordinates distinguish it from the original attempt.

Span names and attribute/event/link counts are bounded. Payload, secret,
recipient, bucket name, raw URL, and unbounded error values are prohibited.
Job, Zone, version, attempt, and Kafka coordinates may be trace attributes but
never unbounded metric labels.

Mail messages start a fresh consumer trace at their untrusted broker boundary.
The broker cannot supply baggage or sampling control. A mail batch keeps each
message context and uses bounded span links rather than choosing an arbitrary
message parent. Its JMAP client span propagates W3C context but never credentials
or message body.

## Failure and shutdown semantics

| Condition | Telemetry result | Business effect |
| --- | --- | --- |
| stdout or collector slow | bounded logging/trace queues may drop and counters rise | execution and settlement continue |
| Collector/backend outage | export is stale or unavailable | no admission, Zone Control, or executor decision reads Collector state |
| Zone Health KV write failure | bounded warning; subsequent snapshot retry | Zone Control may treat that node as stale |
| stale sampler | sample validity/age signal | admission fails closed; Zone Control retains target |
| Kafka/Zone KV retry exhaustion | bounded error, span error, retry/DLQ signal | durable workflow owns retry and source settlement |
| normal SIGTERM | worker drain, OTel provider shutdown, logger guard flush | no new work; tracked work follows shutdown barrier |
| SIGKILL/process abort | tail diagnostics can be lost | Kafka, PostgreSQL, and Zone KV durability rules are unchanged |
| stale assignment worker resumes | fencing fields on diagnostics | assignment epoch blocks stale side effect |

The termination grace period must cover worker drain, OTel shutdown, and the
logger guard. Telemetry remains an observer throughout; it never becomes a
command path, retry authority, authorization source, or business source of
truth.

## Code map

| Concern | Implementation |
| --- | --- |
| OTLP resource, W3C propagation, exporter shutdown | src/observability/otel.rs |
| NDJSON schema, bounded writer, redaction, rate limiting | src/observability/logger.rs |
| sampler, immutable snapshot, OTel runtime instruments | src/observability/metrics.rs |
| Kafka intake and consumer span | src/job_runtime/intake.rs, src/job_runtime/execution.rs |
| result/retry/DLQ and settlement spans | src/job_runtime/completion.rs |
| execution lease and watchdog telemetry | src/job_runtime/coordination/ |
| Zone Control assignment, health, and scale signals | ../zone-control/src/{orchestrator,zone_health,zone_scaling}.rs |
| Zone Collector and Victoria development path | ../dev/zone/otel/otel-collector.yml |
