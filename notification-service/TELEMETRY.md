# Notification Service Telemetry

This is the implementation-level telemetry contract for Notification Service.
Logs, metrics, and traces diagnose the delivery pipeline; none is authorization
state, a durable command path, or proof of browser delivery.

## Resource identity and exporter behavior

Every OTLP signal carries:

```text
service.namespace=aurora
service.name=aurora-notification-service
service.version=<APP_VERSION or Cargo version>
service.instance.id=<boot UUID>
deployment.environment.name=<DEPLOYMENT_ENVIRONMENT or APP_ENV>
host.name=<hostname>
process.pid=<PID>
aurora.component.scope=central
```

The boot UUID distinguishes counter resets and log records from different
process incarnations. Trace and metric exporters use the configured OTLP gRPC
endpoint with bounded queues and timeouts. Exporter construction failure is
logged and disables that signal only; HTTP, Stream consumption, Scylla writes,
and Centrifugo publishes remain available.

## Structured logging

Logs are JSON NDJSON on stdout through a bounded lossy writer. The collector,
not the application, forwards them. VictoriaLogs has one stream label only:

```text
{service_name="aurora-notification-service"}
```

The stable fields are `event_code`, `severity_text`, `severity_number`, `op`,
`message`, optional `error_cause`, `trace_id`, `span_id`, `service_name`,
`service_version`, and `service_instance_id`. Access records additionally carry
bounded `http_request_method`, normalized `url_route`, status code, and
`duration_ms`.

`APP_LOG_MAX_FIELD_BYTES` defaults to 16 KiB and preserves UTF-8 on truncation.
Do not log raw cookies, tokens, request bodies, Centrifugo credentials, Vault
responses, or customer envelope content. Warning and error paths use a fixed
1,024-slot atomic rate limiter keyed by level and operation; the default window
is `APP_LOG_WARN_RATE_LIMIT_MS=1000`. Collisions may reduce logs, but the path
never allocates an unbounded map or takes a mutex.

## Metrics

All metrics use meter `aurora-notification-service`. Labels are normalized to a
finite vocabulary; dynamic IDs, routes, offsets, error strings, tenants,
workspaces, and resource identifiers are forbidden labels.

| Metric | Type | Labels |
|---|---|---|
| `notification_http_requests_total` | Counter | `http.route`, `http.response.status_class` |
| `notification_http_request_duration_seconds` | Histogram | `http.route`, `http.response.status_class` |
| `notification_shared_redis_realtime_events_total` | Counter | `event.kind`, `outcome` |
| `notification_shared_redis_calls_total` | Counter | `rpc.operation`, `outcome` |
| `notification_shared_redis_call_duration_seconds` | Histogram | `rpc.operation`, `outcome` |
| `notification_shared_redis_stream_events_total` | Counter | `stream.kind`, `outcome` |
| `notification_centrifugo_publishes_total` | Counter | `outcome` |
| `notification_event_age_at_centrifugo_publish_seconds` | Histogram | `outcome` |
| `notification_telemetry_events_total` | Counter | `signal`, `outcome` |

`http.route` is one of the explicit connect/timeline routes or `other`;
statuses are status classes. Redis RPC operations normalize to user/admin
Trinity verification or `other`. Stream kinds are `job_notification`,
`user_activity`, or `other`. The log pipeline sampler exports attempted,
dropped, and suppressed deltas every ten seconds.

`notification_event_age_at_centrifugo_publish_seconds` ends at a successful
Centrifugo HTTP acknowledgement. It is not browser-delivery latency and not
durable business-completion latency.

## Tracing and correlation

W3C `traceparent` and `tracestate` are accepted only up to 128 and 512 bytes.
Malformed or partial context is rejected as a remote parent. Span names are
bounded to 128 bytes and each span keeps at most 32 attributes.

```text
Centrifugo connect HTTP
  -> Notification connect span
  -> Shared Redis auth request/reply span
  -> channel grant response

JO job notification Stream
  -> extracted producer context
  -> Stream consumer/projector span
  -> Scylla projection
  -> Centrifugo publish span

Central activity Stream
  -> extracted producer context
  -> activity projection span

Shared Redis realtime Pub/Sub
  -> runtime update span
  -> Centrifugo publish span
```

Connect, job Stream, activity Stream, and Centrifugo adapters create or extract
OpenTelemetry contexts through `observability/traces.rs`. The adapter injects
the derived context into Centrifugo publish metadata where supported. `trace_id`
and `span_id` connect a structured log to its trace; Stream event identity and
notification identity remain the durable correlation keys across redelivery.

## Failure interpretation

| Signal | Meaning | What it does not mean |
|---|---|---|
| Stream `delivery_failed` or invalid outcome | Consumer could not complete the projection path | A producer aggregate was rolled back |
| Centrifugo publish failure | Realtime wake-up failed | The Scylla projection was not stored |
| Stream backlog and PEL recovery | At-least-once repair is active | Exactly-once delivery is guaranteed |
| Telemetry `log:dropped` or `log:suppressed` | Diagnostic visibility degraded | A business event was lost |
| OTLP exporter warning | Traces or metrics may be absent/stale | Stream/HTTP processing must stop |

Alert on persistent Stream failure/invalid-contract growth, Centrifugo publish
failure, Redis auth timeouts, Scylla errors, and telemetry loss. Use Scylla and
the owning producer's durable state—not a trace, log, or realtime message—to
confirm business completion.

