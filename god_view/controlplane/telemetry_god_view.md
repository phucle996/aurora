# Controlplane Telemetry God View

## 1. Scope

Tài liệu này là Source of Truth cho metrics, tracing và structured logs do tiến trình
Controlplane phát ra.
Nó không thay đổi business topology: Controlplane vẫn chỉ nối PostgreSQL của mình,
Auth-State Redis, Shared L2 Redis, Kafka transport và OTel Collector.

Observability là read/diagnostic path. Metric, trace hoặc log không được dùng làm
authorization proof, billing input, business completion hay durable state.

## 2. End-to-end topology

```text
Browser / SDK
    |
    v
Envoy + ACR
    |
    v
HTTP middleware --------------------------> HTTP metrics
    |
    v
Handler -- verified operation in context
    |
    v                                      +--> active span attributes
Module service -- module-bound recorder ---> workflow metrics + exemplar context
    |
    +--> cache decorator ------------------> cache metrics
    |
    +--> pgx / Redis / Kafka adapter ------> dependency metrics
    |
    `--> trace spans and context

All instruments --> OTel SDK periodic reader --> OTel Collector --> metrics backend
All spans       --> OTel SDK batch processor --> OTel Collector --> trace backend
JSON stderr     --> filelog receiver         --> OTel Collector --> logs backend
```

Controlplane không mở Prometheus HTTP collector riêng và không duy trì global metric
singleton theo module. Metrics và traces dùng cùng OTel resource identity.

## 3. Bootstrap and failure semantics

Thứ tự bootstrap bắt buộc:

```text
Vault identity
  -> OTel exporters/providers/instruments
  -> PostgreSQL / Redis / Kafka adapters
  -> migrations
  -> cache engine
  -> module graph
  -> HTTP and internal transports
```

Khởi tạo cả trace exporter và metric exporter phải hoàn tất trước khi cài global
providers. Không cho phép trạng thái nửa tracing, nửa metrics.

- `fail_close`: exporter, provider hoặc instrument init lỗi thì process không khởi động.
- `fail_open`: cài no-op tracer và no-op metrics; dependency graph vẫn nhận object hợp lệ.
- Service không kiểm tra nil telemetry hay infrastructure dependency lại trong request path.
- Module constructor fail-fast nếu OTel dependency không tồn tại.

Khi shutdown, HTTP drain trước, module worker dừng sau, OTel metrics/traces flush có
timeout hữu hạn, sau đó mới đóng PostgreSQL và Redis.

## 4. Resource identity

Mọi signal có resource attributes tĩnh:

| Attribute | Contract |
|---|---|
| `service.name` | Tên app từ config |
| `service.version` | Release/build immutable của app |
| `service.instance.id` | Hostname/pod identity; `unknown` nếu OS không cung cấp |
| `aurora.component` | Chỉ dùng trong trace/metric resource nếu signal cần; không lặp vào log |

Application JSON logs emit one normalized backend identity: `service_name`,
`service_version` and `service_instance_id`. Container name/pod identity is diagnostic
metadata only and is never a VictoriaLogs stream dimension.

Pod identity chỉ là resource attribute. Không nhân nó vào business metric labels.

## 5. Instrument contract

| Instrument | Type | Labels |
|---|---|---|
| `aurora_controlplane_http_requests_total` | Counter | `method`, `route`, `status_code` |
| `aurora_controlplane_http_request_duration_seconds` | Histogram | `method`, `route`, `status_code` |
| `aurora_controlplane_http_in_flight_requests` | UpDownCounter | `method` |
| `aurora_controlplane_workflow_calls_total` | Counter | `module`, `op`, `result`, `reason` |
| `aurora_controlplane_workflow_duration_seconds` | Histogram | `module`, `op`, `result`, `reason` |
| `aurora_controlplane_dependency_calls_total` | Counter | `module`, `op`, `system`, `operation`, `result`, `reason` |
| `aurora_controlplane_dependency_duration_seconds` | Histogram | cùng dependency labels |
| `aurora_controlplane_cache_operations_total` | Counter | `layer`, `namespace`, `operation`, `result` |
| `aurora_controlplane_system_time_drift_seconds` | Gauge | không label |
| `aurora_controlplane_system_time_sync_state` | Gauge | `state` |

Ba latency histogram dùng cùng bounded bucket policy, từ 1ms đến 30s. Request hay
workflow dài hơn vẫn được ghi vào overflow bucket.

## 6. Workflow ownership

Mỗi module nhận một `WorkflowRecorder` đã bind module name từ module constructor.
Handler đặt operation tĩnh vào `context.Context`; service tin operation đã được
transport xác định và ghi đúng một result cho một workflow.

Module allow-list hiện tại:

- `iam`
- `hierarchy`
- `mail`
- `managedservice`
- `storage`
- `hypervisor`

Operation theo dạng `<module>.<object>.<behavior>` và chỉ chứa chữ thường, số,
dấu chấm hoặc underscore. Operation rỗng, quá dài hay có ký tự lạ bị đưa về
`unknown` thay vì tạo series tùy ý.

Service-to-service composition không được tạo hai sample cho cùng logical outcome. Luồng
Mail change-state ủy quyền phần persist cho update workflow: pre-check failure được ghi tại
change-state, còn delegated outcome chỉ được ghi tại update.

Workflow recorder cũng enrich active transport span bằng bốn thuộc tính bounded:

```text
aurora.module, aurora.operation, aurora.result, aurora.reason
```

Chỉ `failure` đặt span status thành Error. Business `rejected` giữ span status bình
thường để SRE không nhầm client/security rejection với system outage.

## 7. Result and reason taxonomy

`result` chỉ có ba giá trị:

- `success`: durable/read boundary mà workflow yêu cầu đã hoàn tất.
- `rejected`: request hợp lệ về transport nhưng bị business/security rule từ chối.
- `failure`: hệ thống không thể hoàn tất workflow.

`success` luôn đi cùng `reason=none`.

Rejected reasons:

```text
invalid_argument, not_found, already_exists, conflict,
precondition_failed, invalid_transition, unauthenticated,
forbidden, rate_limited, busy, empty, constraint
```

Failure reasons:

```text
timeout, canceled, unavailable, internal
```

Recorder normalize cặp không hợp lệ về bounded fallback. Không đưa raw error,
SQLSTATE, UUID, owner, workspace, zone, topic, bucket name hay provider message vào label.

### 7.1 Correlation contract

Mỗi transport root sở hữu một request-scoped correlation carrier được đồng bộ hóa.
Handler ghi canonical operation; workflow recorder ghi đúng một module/result/reason;
access và handler logger chỉ đọc snapshot sau khi service trả về. Carrier là soft state
trong memory, không phải authorization proof hay business completion và không được persist.

Chuỗi điều tra chuẩn:

```text
metric module/op/result/reason
  -> trace exemplar trace_id
  -> span cùng aurora.* attributes
  -> logs cùng service_name + trace_id + op + result/reason
```

Không đưa `trace_id` hoặc `request_id` vào metric labels. Exemplar là liên kết
high-cardinality duy nhất từ metric sample sang trace.
Chỉ sample có active sampled span mới có exemplar; trace bị sampling bỏ là hành vi bình
thường, không phải metric hay workflow failure.

## 8. HTTP metrics

HTTP middleware là owner duy nhất của request count, latency và in-flight gauge.

- `route` lấy từ Gin full route template sau routing.
- Request không match dùng `__unmatched__`; không dùng raw URL path.
- `method` chỉ cho phép HTTP method chuẩn, còn lại là `OTHER`.
- `status_code` là ba chữ số 1xx-5xx, còn lại là `000`.
- In-flight giảm trong defer kể cả panic path mà outer recovery middleware xử lý.
- Access log dùng Gin route template và canonical operation; không dùng raw URL path làm
  correlation key.
- HTTP 5xx đặt root span status Error. HTTP 4xx không tự động được coi là system error.

## 9. Dependency metrics and double-count rule

Dependency metrics chỉ được ghi tại adapter thật sự thực hiện I/O:

| Adapter | `system` | `operation` |
|---|---|---|
| pgx query tracer | `postgresql` | bounded SQL command tag |
| go-redis hook | `redis` | command name hoặc `pipeline` |
| Kafka producer | `kafka` | `publish` |

Service không ghi dependency metrics thủ công. Một DB/Redis/Kafka call chỉ tạo một
counter sample và một histogram observation.

Khi adapter có active span, cùng sample đó ghi thêm vào dependency span các thuộc tính
bounded `aurora.module`, `aurora.operation`, `aurora.dependency.system`,
`aurora.dependency.operation`, `aurora.dependency.result` và
`aurora.dependency.reason`. PGX và Redis đã có client span; Kafka producer tạo producer
span. Không ghi SQL text, Redis key, Kafka topic/key/value hay provider error vào span.
Metric được observe trước khi adapter đóng span để exemplar trỏ đúng trace/span đó.
Dependency span chỉ có status Error khi `result=failure`; no-row hay constraint là
`rejected` và không được trình bày như outage hệ thống.

PGX classification:

- no rows: `rejected/empty`
- SQLSTATE class 23: `rejected/constraint`
- context deadline/cancel: `failure/timeout|canceled`
- connection class 08 và shutdown class 57P: `failure/unavailable`
- còn lại: `failure/internal`

Redis `Nil` là `rejected/empty`; timeout/cancel tách riêng; transport error là unavailable.
Kafka publish timeout/cancel tách riêng, còn broker error là unavailable.

Dependency metric không phải retry controller. Kafka, Redis và PostgreSQL vẫn tuân retry,
idempotency, ordering và failure semantics của workflow God View tương ứng.

## 10. Cache metrics

Cache decorator là owner duy nhất của cache operation metric. L1/L2 implementation không
tự tạo meter. `namespace` là catalog tĩnh do registry cấp, không phải cache key.

Cache là rebuildable state. Cache miss, stale hay decode rejection có thể hiện trong cache
metric nhưng không tự biến thành business failure nếu workflow recovery từ SoT thành công.

## 10.1 Structured log contract

Structured log fields dùng schema tối giản: `service_name`, `service_version`,
`service_instance_id`, `op`, `event_code`, `_msg`, `severity_text`, `severity_number`.
`trace_id`, `span_id`, `operation_id`, `event_id`, `actor_id`, `tenant_id`,
`workspace_id`, `resource_id`, `target_user_id`, `result`, `reason`, retry và dependency
fields chỉ xuất hiện khi workflow thực sự có ngữ cảnh đó; không emit empty/default sentinel.

VictoriaLogs stream chỉ có `{service_name}`. `op` và ownership identity là searchable
attributes, không phải stream dimensions.

- Success HTTP workflow dùng một access log; service không emit thêm success log.
- Runtime handler/background có context phải dùng context-aware logger để giữ trace ID.
- `Sys*` không context chỉ dành cho bootstrap/process lifecycle.
- `AppError` diagnostic class không thay thế result/reason và không là metric label.
- Raw cause chỉ được log sau redaction và length bound; response không nhận cause.
- Logs có thể chứa trace/operation và business identity cần thiết để debug nhưng các field đó không trở thành
  VictoriaLogs stream dimensions.

## 11. Scale-out and backpressure

Mỗi replica ghi signal độc lập với cùng resource schema. Backend aggregate theo
`service.name` và business labels; không yêu cầu leader cho metrics.

Trace batch processor và metric periodic reader có bounded queue/export timeout. Telemetry
không được chặn vô hạn request path. Khi collector chậm:

- fail-open runtime có thể mất sample theo OTel SDK semantics;
- business commit không rollback chỉ vì export sau commit thất bại;
- exporter error phải hiển thị qua internal OTel diagnostics/logs;
- không buffer telemetry vào PostgreSQL, Redis Stream, Kafka hay outbox.
- Log export tiếp tục qua bounded persistent collector queue; application không chờ log
  backend ACK trên request path.

## 12. Security and cardinality boundary

Metric labels không được chứa:

- user, tenant, workspace, zone hoặc resource ID;
- email, username, IP, user-agent;
- token, secret, credential, policy hoặc ciphertext;
- raw HTTP path, SQL text, Redis key, Kafka topic;
- raw error hoặc provider response.

User, tenant, workspace và resource ID có thể xuất hiện trong log boundary của business
workflow sau khi đã được verified upstream. Chúng không được lặp vào mọi dependency/poll
log và không được dùng làm metric label hoặc stream dimension.

Chi tiết nhạy cảm chỉ đi vào log/traces khi đã sanitize và theo sampling policy.
Internal header do client gửi không được dùng để tạo label; operation đến từ code
path tĩnh của handler.

## 13. Baseline alerts

Dashboard/alert query phải dựa trên contract trên:

- workflow failure ratio theo `module,op,reason`;
- p95/p99 workflow latency theo `module,op`;
- HTTP 5xx ratio theo route template;
- dependency unavailable/timeout ratio theo `system,operation`;
- PostgreSQL/Redis/Kafka dependency p99;
- cache miss/rejection ratio theo bounded namespace;
- time sync state warning/critical;
- sustained in-flight saturation kèm latency/error increase.

Không alert trực tiếp trên `rejected/not_found` hoặc `rejected/unauthenticated` nếu không
có rate/baseline guard, vì đó có thể là hành vi client bình thường hoặc attack traffic.

## 14. Verification invariants

Change-set telemetry phải chứng minh:

1. Không còn module-local metric package hay process-global `CurrentMetrics`.
2. Các module constructor nhận OTel và service nhận module-bound recorder.
3. PGX, Redis, Kafka call không bị service đếm lại.
4. Unmatched HTTP request không tạo raw-path series.
5. Invalid result/reason/op được normalize về bounded fallback.
6. OTel metric exporter init error thực sự tuân fail-open/fail-close.
7. Workflow metric, active span và structured log dùng cùng operation/result/reason.
8. AppError cause bị sanitize và runtime context log giữ trace ID.
9. Grafana datasource map metric exemplar và log trace field sang VictoriaTraces.
10. Full Controlplane test, race test của telemetry adapters/correlation state và
    `go vet` thành công.
