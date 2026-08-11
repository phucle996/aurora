# Controlplane Telemetry

This is the source of truth for Controlplane's internal telemetry architecture:
traces, metrics, structured logs, and their ownership. It is not an API
workflow or authorization contract; those remain in the relevant God Views.

## Runtime topology

~~~text
Browser / service
  | W3C traceparent, X-Request-ID
  v
Envoy / ACR
  |
  v
+---------------------------------------------------------------------+
| Controlplane                                                        |
|  Gin middleware: RequestID -> trace context -> HTTP metrics -> log  |
|       |                                                             |
|       v                                                             |
|  handler -> service -> repository / adapter                         |
|              |        |          |          |                       |
|              |        |          |          +--> Kafka producer     |
|              |        |          +-------------> Redis hook         |
|              |        +------------------------> pgx tracer         |
|              +--> one terminal workflow outcome                     |
|                                                                     |
|  JSON logs ----------------------------------------------> stdout   |
|  server/client spans + metrics -> OTel SDK -> OTLP/gRPC -----------+----> OTel Collector
+---------------------------------------------------------------------+          |
                                                                                 v
                                                                    traces, metrics, logs,
                                                                    dashboards and alerts
~~~

The global Gin middleware owns request ID creation, trace extraction, server
span creation, HTTP metrics, and access logs. The handler puts a static
operation in the request context. The service records exactly one terminal
workflow outcome. PostgreSQL, Redis, and Kafka adapters each own their client
span and dependency metric; service code must not duplicate them.

## Bootstrap and shutdown

~~~text
Vault identity
  -> OTel resource, trace exporter, metric exporter, instruments
  -> PostgreSQL / shared Redis / auth-state Redis / Kafka adapters
  -> migrations -> L1 cache engine -> modules -> HTTP and gRPC transports

mark not-ready
  -> drain HTTP (20 s) -> stop gRPC -> stop module workers
  -> flush OTel metrics and traces (10 s) -> cancel root context
  -> close PostgreSQL, Redis, and Kafka clients
~~~

Both OTLP exporters must initialize before either global OpenTelemetry provider
is installed. With otel.fail_strategy != fail_open, an OTel initialization
failure aborts bootstrap. With fail_open, the application logs the failure and
installs no-op telemetry. otel.enabled=false intentionally selects the same
no-op boundary. The PostgreSQL, Redis, migration, cache, module, and transport
boundaries stay fail-close regardless of this setting.

Kafka validates local configuration at bootstrap but does not ping a broker.
Its publish failure is handled by the calling workflow, rather than restarting
the entire Controlplane for a transient broker outage.

## Resource identity and correlation

| Attribute | Value |
| --- | --- |
| service.name | app.info.name, default controlplane |
| service.version | app.info.version, default dev |
| service.instance.id | local hostname, default unknown |
| aurora.component | controlplane |

The app uses W3C Trace Context. Middleware extracts traceparent, starts the
Controlplane server span, injects the updated context into outbound carriers,
and returns the current traceparent header when available. RequestID prefers
the edge-provided X-Request-ID, then the trace ID, and finally creates a random
value for direct or offline traffic.

For investigation, begin with a bounded metric dimension, open the matching
trace/exemplar, then use the request ID to find structured JSON logs. Never add
request IDs, trace IDs, UUIDs, user/tenant/workspace/zone IDs, Redis keys,
Kafka topics, SQL text, raw paths, or raw provider errors as metric labels.

## Signal ownership

| Boundary | Owner | Signals |
| --- | --- | --- |
| Incoming HTTP | Gin middleware | server span; request total, duration, and in-flight metrics |
| Business workflow | module-injected WorkflowRecorder in service | one workflow result, duration, and span outcome |
| PostgreSQL query | pgx query tracer | client span; dependency result and duration |
| Redis command/pipeline | go-redis hook | client span; dependency result and duration |
| Kafka publish | Kafka producer | producer span; dependency result and duration |
| L1 cache operation | cache metrics decorator | cache-operation counter |
| Application event | logger | structured JSON log with request/trace correlation |

iam, hierarchy, mail, managedservice, storage, and hypervisor receive a recorder
scoped to their module. The recorder is not a global metric singleton or a
generic application context.

## Metrics contract

| Metric | Type | Bounded dimensions |
| --- | --- | --- |
| aurora_controlplane_http_requests_total | counter | method, route, status_code |
| aurora_controlplane_http_request_duration_seconds | histogram | method, route, status_code |
| aurora_controlplane_http_in_flight_requests | up-down counter | method |
| aurora_controlplane_workflow_calls_total | counter | module, op, result, reason |
| aurora_controlplane_workflow_duration_seconds | histogram | module, op, result, reason |
| aurora_controlplane_dependency_calls_total | counter | module, op, system, operation, result, reason |
| aurora_controlplane_dependency_duration_seconds | histogram | module, op, system, operation, result, reason |
| aurora_controlplane_cache_operations_total | counter | layer, namespace, operation, result |
| aurora_controlplane_system_time_drift_seconds | gauge | none |
| aurora_controlplane_system_time_sync_state | one-hot gauge | state |

HTTP uses Gin's registered route template; an unmatched route is always
__unmatched__, never the raw client path. Operation and token labels are
validated against a bounded lowercase character set and fall back to unknown.
Duration histograms use second buckets from 1 ms through 30 s.

| Result | Allowed reasons |
| --- | --- |
| success | none |
| rejected | invalid_argument, not_found, already_exists, conflict, precondition_failed, invalid_transition, unauthenticated, forbidden, rate_limited, busy, empty, constraint |
| failure | timeout, canceled, unavailable, internal |

Invalid combinations normalize to bounded failure values. The same module,
operation, result, and reason are placed on the active span; a failure marks
that span as an error without copying raw provider errors.

## Dependency, cache, and logs

~~~text
request context + operation
  +--> pgx tracer       -> postgres.<verb> client span -> dependency metric
  +--> Redis hook       -> redis.<command> client span -> dependency metric
  +--> Kafka producer   -> kafka.publish producer span -> dependency metric
  +--> L1 cache wrapper -> cache-operation metric
~~~

PostgreSQL classifies no rows and constraint violations as rejections; timeout,
cancellation, availability, and unknown faults as failures. Redis classifies
redis.Nil as an empty rejection. Kafka records only the stable publish
operation; topic, key, and value are deliberately excluded. Cache metrics use
bounded namespaces and hit, miss, success, or error results.

Logs are structured JSON on stdout. The deployment's collector ships them to
the observability backend. Log fields retain safe request and trace correlation,
module, operation, and terminal outcome, but never credentials, session secrets,
proof material, authorization headers, raw payloads, or unbounded provider
errors.

## Operational rules

- Alert on failure rate and p95/p99 workflow or dependency latency using stable
  module, op, system, and reason dimensions.
- Watch HTTP in-flight requests for saturation and time-drift/state gauges for
  clock-health regressions.
- The OTel SDK's queue, batching, sampling, and exporter timeouts bound
  telemetry work. Collector backpressure may lose telemetry but must not block a
  business request.
- A new workflow metric needs one static operation, one terminal owner, the
  bounded outcome taxonomy, and a boundary test. A new dependency metric belongs
  in its adapter, not in service-level copy-and-paste instrumentation.

## Code map

| Concern | Implementation |
| --- | --- |
| Bootstrap and shutdown | internal/app/app.go |
| OTel providers, exporters, propagation | internal/observability/otel.go |
| Instruments and normalization | internal/observability/metrics.go |
| HTTP trace, metrics, request ID | internal/http/middleware/observability.go |
| PostgreSQL and Redis adapters | internal/observability/pgx_tracer.go, internal/observability/redis_hook.go |
| Kafka adapter | infra/kafka/kafka.go |
| L1 cache instrumentation | internal/cacheengine/metrics.go |
