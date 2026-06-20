<!-- markdownlint-disable MD033 -->
# Outbound Email System - Create Mail Endpoint Workflow God View

> [!NOTE]
> Tài liệu này đóng vai trò là **Source of Truth (SoT) / God View** cho tính năng "Create Mail Endpoint" (Tạo Điểm Cuối Gửi Thư).
> Mọi thay đổi mã nguồn trong tương lai của phân hệ này phải tuân thủ nghiêm ngặt theo thiết kế và các ràng buộc được mô tả ở đây.

---

## <a id="intro"></a>🗺️ 1. Giới Thiệu

### 👥 Tài Liệu Này Dành Cho Ai?

Tài liệu này được biên soạn đặc biệt cho **USA (Ultimate System Administrator)**, SRE (Site Reliability Engineer) và Developers chịu trách nhiệm phát triển, vận hành, giám sát hạ tầng gửi Email Outbound ở môi trường **Cloud-Native & HA (High Availability)**.

### ❓ Tính Năng "Create Mail Endpoint" Là Gì?

**Create Mail Endpoint** là luồng thiết lập và lưu trữ chính thức thông tin cấu hình kết nối SMTP Server vật lý (bao gồm IP/Host, Port, Credentials, TLS Mode, CA/Client Certificates, routing priority, weights, và connection limit) cho một Zone cụ thể trong hệ thống.

Để đảm bảo khả năng cô lập mạng (Network Isolation) và tính tự trị (Autonomous Mode) của các Zone khi WAN link bị đứt hoặc Controlplane sập, cấu hình này không chỉ được lưu trữ tập trung tại Controlplane Database mà còn được phân phối và cache tại Zone đích thông qua mô hình lưu trữ 3 tầng bảo mật (L0 Database, L2 Redis Cache, L1 Memory Cache).

### 🎯 Tính Năng Này Làm Gì?

- Tiếp nhận và validate thông số SMTP đầy đủ từ Admin UI Form.
- Lưu trữ các trường cấu hình nhạy cảm như `password` và `client_key_pem` dưới dạng plain text thô tại tầng Controlplane trước khi ghi vào Database.
- Thực thi ghi đồng thời thông tin Endpoint vật lý vào bảng `mail_endpoints` và một transactional Outbox job sync vào bảng `mail_outbox_records` trong **cùng một Database Transaction** (đảm bảo tính nguyên tử - Atomicity).
- Trích xuất sự kiện phi chặn (non-blocking) qua PostgreSQL Logical Replication (CDC) bằng `job-proxy` và chuyển tiếp vào Redis Job Stream của Zone tương ứng.
- **Lưu trữ L2 (Distributed Cache)**: Dataplane worker tại Zone đích consume sự kiện sync từ Redis Job Stream, ghi cấu hình Endpoint vào Redis Zone cục bộ (L2 Cache) để chia sẻ giữa các dataplane trong cùng zone.
- **Lưu trữ L1 (In-Memory Cache)**: Dataplane worker nạp dữ liệu định tuyến nhẹ (`weight`, `priority`, `max_connections`) vào RAM (L1 Cache) phục vụ thuật toán chọn tuyến tối ưu và kết xuất (render) email tức thì. Dữ liệu L1 được đồng bộ giữa các node HA thông qua Redis Zone Pub/Sub.
- **Tầng gửi thư bất đồng bộ (Stalwart MTA Spool)**: Email sau khi được Dataplane định tuyến và render xong sẽ được chuyển tiếp trực tiếp qua **giao thức LMTP (Cổng 24)** tới **Stalwart Mail Server (MTA)**. Stalwart MTA tiếp nhận nhanh (< 1ms), lưu thư vào bộ đệm spool ghi đĩa cục bộ của nó và tự động xử lý gửi ra internet ngầm, giải phóng Dataplane khỏi việc chờ đợi kết nối SMTP vật lý mà không cần thêm hàng đợi Redis trung gian.

### 📍 Tính Năng Này Hoạt Động Ở Đâu?

Tính năng này hoạt động xuyên suốt các biên công nghệ:

1. **Frontend (Browser Client)**: `admin-ui/src/pages/mail/NewMailEndpoint.tsx`
2. **Controlplane (Go API)**: `controlplane/internal/mail/transport/http/handler/endpoint_handler.go`
3. **Database (PostgreSQL)**: Bảng `mail_endpoints` và `mail_outbox_records` thuộc schema `mail`.
4. **CDC & Inbound Consumer (Rust)**: Tiến trình `job-proxy` chạy ngầm.
5. **Redis Job System**: Redis Streams (`jobs:<zone_id>`) đóng vai trò vận chuyển task sync xuyên suốt hệ thống.
6. **Redis Zone (Local Cache)**: Redis Keyspace (`mail:zone:<zone_id>:endpoints:<endpoint_id>`) làm L2 Cache chia sẻ nội bộ Zone và Pub/Sub channel (`mail:zone:<zone_id>:endpoint_events`) để broadcast đồng bộ L1 cache giữa các Dataplane node trong zone.
7. **Dataplane Executor (Rust)**: Tiến trình `dataplane` chạy ở các Zone cụ thể.

---

### 📂 Mục Lục (Table of Contents)

- [1. Giới Thiệu Dành Cho USA & Mục Lục](#intro)
- [2. Sơ Đồ Hệ Thống & Ranh Giới Phase (Topology & Phase Boundaries)](#topology)
- [3. Chi Tiết Thực Thi Nghiệp Vụ & Ánh Xạ (Implementation Details)](#details)
- [4. State Machine Của Endpoint Cache (Cache State Machine)](#state-machine)
- [5. Xử Lý Race Condition & Bảo Mật (Race Condition & Security)](#race-condition)
- [6. Giám Sát Và Truy Vết - Grafana Runbook (Telemetry & Grafana Queries)](#telemetry)

---

## <a id="topology"></a>🏛️ 2. Sơ Đồ Hệ Thống & Ranh Giới Phase (Topology & Phase Boundaries)

### 🗺️ High-Level System Topology

```mermaid
graph TD
    %% Nodes
    UI["💻 Admin UI (Browser)"]
    CP["💼 Controlplane (Go App)"]
    DB[("🗄️ PostgreSQL (Mail Schema)")]
    JP_CDC["⚙️ Job-Proxy (CDC Streamer - Rust)"]
    RDS_JOB["⚡ Redis Job Stream (jobs:zone_id)"]
    DP1["🚀 Dataplane 1 (Active Sync Node)"]
    DP2["🚀 Dataplane 2 (HA Node)"]
    DPN["🚀 Dataplane n (HA Node)"]
    RDS_ZONE["⚡ Redis Zone (L2 Cache & Events)"]
    RDS_RES["⚡ Redis Stream (job_results_stream)"]
    JP_RC["⚙️ Job-Proxy (Result Consumer - Rust)"]
    RDS_NOTIF["⚡ Redis Stream (stream:job_notifications)"]
    NS["🔔 Notification Service (Axum/Rust)"]
    CF["📡 Centrifugo WebSocket Gateway"]
    STW["📧 Stalwart Mail Server (MTA)"]
    SMTP["📧 Destination SMTP Server (Gmail/Outlook...)"]

    %% Connections
    UI -- "1. POST /admin/mail/endpoints" --> CP
    CP -- "2. Write endpoint (initializing) & outbox (PENDING) in 1 Tx" --> DB
    DB -- "3. WAL Logical Replication Events" --> JP_CDC
    JP_CDC -- "4. Push sync task (XADD)" --> RDS_JOB
    RDS_JOB -- "5. Consume sync task (XREADGROUP)" --> DP1
    DP1 -- "6. Write config to L2 Cache (HMSET)" --> RDS_ZONE
    DP1 -- "7. Broadcast reload event (PUBLISH)" --> RDS_ZONE
    RDS_ZONE -- "8. Trigger reload (SUBSCRIBE)" --> DP2
    RDS_ZONE -- "8. Trigger reload (SUBSCRIBE)" --> DPN
    
    %% Feedback Loop
    DP1 -- "9. Push sync result (XADD)" --> RDS_RES
    RDS_RES -- "10. Consume result" --> JP_RC
    JP_RC -- "11. Update endpoint to active & outbox to SUCCEEDED" --> DB
    JP_RC -- "12. Publish notification (XADD)" --> RDS_NOTIF
    RDS_NOTIF -- "13. Read & decode Proto" --> NS
    NS -- "14. HTTP POST Publish" --> CF
    CF -- "15. WebSocket Broadcast (Real-Time UI Update)" --> UI
    
    DP1 -- "16. Render email & relay via LMTP (Port 24)" --> STW
    STW -- "17. Deliver SMTP mail to Internet" --> SMTP

    %% Styling
    classDef ui fill:#2b5c8f,stroke:#000,stroke-width:1px,color:#fff;
    classDef go fill:#1a662c,stroke:#000,stroke-width:1px,color:#fff;
    classDef rust fill:#8f1d1d,stroke:#000,stroke-width:1px,color:#fff;
    classDef db fill:#b35900,stroke:#000,stroke-width:1px,color:#fff;
    classDef redis fill:#5e3d7a,stroke:#000,stroke-width:1px,color:#fff;
    classDef external fill:#555,stroke:#000,stroke-width:1px,color:#fff;

    class UI,CF ui;
    class CP go;
    class JP_CDC,JP_RC,DP1,DP2,DPN,NS rust;
    class DB db;
    class RDS_JOB,RDS_ZONE,RDS_RES,RDS_NOTIF redis;
    class SMTP external;
```

---

### 🚧 Ranh Giới Và Ràng Buộc Các Phase (Phase Boundaries)

#### Phase 1: Ingestion & Outbox Persistence

- **Ranh giới (Boundary)**: Từ Admin UI Browser gửi qua mạng WAN/LAN đến HTTP Gateway của Controlplane (Go).
- **Đầu vào (Inputs)**: JSON body chứa thông số cấu hình SMTP đầy đủ (`name`, `host`, `port`, `username`, `password`, `tls_mode`, `ca_cert_pem`, `client_cert_pem`, `client_key_pem`, `max_connections`, `priority`, `weight`) kèm header cookie định danh và CSRF token.
- **Đầu ra (Outputs)**:
  - Bản ghi SMTP chính thức được lưu vào `mail_endpoints` trong trạng thái `initializing`.
  - Bản ghi `mail_outbox_records` có trạng thái `PENDING`, topic `"mail.create_endpoint"`, và payload chứa thông tin endpoint được tuần tự hóa (serialized) sang binary Protobuf.
  - Trả về HTTP 201 Created với payload chỉ trạng thái khởi tạo và id (ví dụ: {"status": "initializing", "id": "<endpoint_id>"}), tuyệt đối không trả về thông tin chi tiết của Endpoint ở phase này.
- **Ràng buộc & Giới hạn (Controls & Limits)**:
  - Phải vượt qua các Gateway Middleware (IP whitelist AdminCIDR, AdminAPIKeyAuth, UserZoneAuth, rate-limits Pre và Post Auth).
  - **Data-at-Rest Protection**: Hiện tại `password` và `client_key_pem` được lưu ở dạng plain text thô.
  - **Transactional Integrity**: Mọi thao tác ghi xuống `mail_endpoints` và `mail_outbox_records` phải chạy trong cùng một SQL Transaction block. Nếu một trong hai lỗi, rollback toàn bộ.
- **Mã nguồn thực thi (Code Callsites & Implementation)**:
  - **Kích hoạt từ Giao diện**: [NewMailEndpoint.tsx](file:///home/phucle/Desktop/New/admin-ui/src/pages/mail/NewMailEndpoint.tsx) gửi request POST đi.
  - **Bộ Xử Lý HTTP (Handler)**: [endpoint_handler.go](file:///home/phucle/Desktop/New/controlplane/internal/mail/transport/http/handler/endpoint_handler.go) $\rightarrow$ Phương thức `CreateEndpoint` phân tích JSON body và DTO validation.
  - **Tầng Nghiệp Vụ (Service)**: [endpoint_service_impl.go](file:///home/phucle/Desktop/New/controlplane/internal/mail/service/endpoint_service_impl.go) $\rightarrow$ Hàm `CreateEndpoint` chuẩn bị cấu trúc Entity và transaction DB.
  - **Tầng Lưu Trữ (Repository)**: [outbox_repo_postgres.go](file:///home/phucle/Desktop/New/controlplane/internal/mail/repository/postgres/outbox_repo_postgres.go) $\rightarrow$ Phương thức `Create` thực hiện ghi transaction.

#### Phase 2: Logical Replication & Transport

- **Ranh giới (Boundary)**: Từ Database Postgres Engine qua tiến trình logic `job-proxy` (Rust) đến cụm Redis Cluster.
- **Đầu vào (Inputs)**: Luồng WAL binary từ Postgres logical replication slot sử dụng plugin decode `pgoutput`.
- **Đầu ra (Outputs)**: Job payload được đẩy thành công vào Redis Stream `jobs:<zone_id>` qua lệnh `XADD`.
- **Ràng buộc & Giới hạn (Controls & Limits)**:
  - CDC Streamer chạy bất đồng bộ ngoài transaction của Go.
  - Phải đảm bảo nguyên tắc **At-Least-Once**: Chỉ cập nhật applied LSN lên Postgres *sau khi* ghi nhận thành công từ Redis Stream.
  - Concurrency limit: Chỉ duy nhất một node Job-Proxy được kết nối tích cực vào logical replication slot tại một thời điểm.
- **Mã nguồn thực thi (Code Callsites & Implementation)**:
  - **CDC Streamer**: [mod.rs (CDC)](file:///home/phucle/Desktop/New/job-proxy/src/cdc/mod.rs) $\rightarrow$ Hàm `run_replication_stream` quản lý luồng CDC, lắng nghe sự kiện WAL và đẩy vào Redis Stream.

#### Phase 3: Dataplane Execution & Lease Locking

- **Ranh giới (Boundary)**: Từ Redis Job Stream qua Dataplane active worker đến Redis Zone (L2 Cache) cục bộ và các node Dataplane HA trong Zone.
- **Đầu vào (Inputs)**: Task sync consume từ Redis Stream `jobs:<zone_id>` (XREADGROUP).
- **Đầu ra (Outputs)**:
  - Dữ liệu cấu hình Endpoint được ghi vào Redis Hash L2 của Zone: `mail:zone:<zone_id>:endpoints:<endpoint_id>`.
  - Sự kiện sync được publish qua kênh Pub/Sub của Redis Zone: `mail:zone:<zone_id>:endpoint_events`.
  - Connection Pool tương ứng với `endpoint_id` được nạp/cập nhật vào bộ nhớ RAM của tất cả Dataplane instances trong Zone (L1 Cache).
  - Kết quả đồng bộ hóa (thành công/thất bại) được đóng gói và gửi lên Redis Stream `job_results_stream` (XADD).
- **Ràng buộc & Giới hạn (Controls & Limits)**:
  - **Distributed Lease Lock**: Phải chiếm hữu lock `locks:job:<job_id>` trên Redis nội bộ để ngăn cản việc xử lý trùng lặp ở cụm HA.
  - **Dynamic Connection Pooling**: Tự động đóng/mở kết nối SMTP dựa trên tham số `max_connections` cấu hình. Nếu cấu hình thay đổi, connection pool cũ phải được drain và khởi tạo pool mới.
- **Mã nguồn thực thi (Code Callsites & Implementation)**:
  - **Dataplane Job Runner**: [runner.rs](file:///home/phucle/Desktop/New/dataplane/src/job_lifecycle/runner.rs) $\rightarrow$ Hàm `run_job` quản lý vòng đời chạy tác vụ ở Dataplane.
  - **Sync Config Executor**: [consumer.rs](file:///home/phucle/Desktop/New/dataplane/src/job_lifecycle/consumer.rs) thực thi logic đồng bộ, nạp cấu hình và ghi L2 cache.

#### Phase 4: Feedback Callback & Notification

- **Ranh giới (Boundary)**: Từ Redis Stream kết quả (`job_results_stream`) qua tiến trình `job-proxy` (Result Consumer) cập nhật PostgreSQL DB, đồng thời đẩy qua Redis Stream thông báo (`stream:job_notifications`) $\rightarrow$ `notification-service` $\rightarrow$ Centrifugo Gateway $\rightarrow$ Admin UI Browser.
- **Đầu vào (Inputs)**: Bản tin `JobExecutionResult` nhị phân được publish từ Dataplane chứa trạng thái `SUCCEEDED` hoặc `FAILED` kèm theo `trace_id`.
- **Đầu ra (Outputs)**:
  - Cập nhật trạng thái bản ghi `mail_outbox_records` thành `SUCCEEDED`/`FAILED`.
  - Cập nhật trường trạng thái của Endpoint trong DB `mail_endpoints` sang `active` nếu đồng bộ thành công.
  - Một `JobNotificationEvent` dạng nhị phân Protobuf được ghi vào Redis Stream `stream:job_notifications`.
  - Admin UI nhận được Toast Notification realtime qua kênh WebSocket của Centrifugo báo cáo kết quả hoàn thành việc đồng bộ hóa Endpoint tại Zone.
- **Ràng buộc & Giới hạn (Controls & Limits)**:
  - **End-to-End Tracing Continuity**: Phải bảo toàn `trace_id` ban đầu trong suốt hành trình phản hồi.
- **Mã nguồn thực thi (Code Callsites & Implementation)**:
  - **Result Consumer Worker**: [consumer.rs (Job-Proxy)](file:///home/phucle/Desktop/New/job-proxy/src/result_consumer/consumer.rs) $\rightarrow$ Hàm `run` lắng nghe `job_results_stream`, cập nhật DB và gọi Notifier.
  - **Real-Time Event Notifier**: [notifier.rs (Job-Proxy)](file:///home/phucle/Desktop/New/job-proxy/src/result_consumer/notifier.rs) $\rightarrow$ Hàm `notify_realtime` mã hóa Protobuf và đẩy vào `stream:job_notifications`.
  - **Notification HTTP Server**: [connect.rs (Notification Service)](file:///home/phucle/Desktop/New/notification-service/src/handler/connect.rs) & [redis.rs](file:///home/phucle/Desktop/New/notification-service/src/infra/redis.rs) xử lý kết nối, giải mã sự kiện và POST sang Centrifugo API.

---

## <a id="details"></a>🔍 3. Chi Tiết Thực Thi Nghiệp Vụ & Ánh Xạ (Implementation Details)

### 🛡️ Middleware Chain & Context Injections (Controlplane)

📌 **Mã nguồn kiểm soát tại:** [app.go](file:///home/phucle/Desktop/New/controlplane/internal/app/app.go) & [route.go](file:///home/phucle/Desktop/New/controlplane/internal/mail/route.go)

Before reaching the `CreateEndpoint` HTTP Handler, a request goes through a strict multi-layer security and telemetry chain.

#### 1. Global Middleware (defined in `controlplane/internal/app/app.go`)

| Middleware | Purpose / Action | Context Injections |
| :--- | :--- | :--- |
| **`gin.Recovery()`** | Prevents server crashes by catching panics and returning a `500 Internal Server Error`. | None |
| **`middleware.RequestID()`** | Extracts or generates a unique correlation ID: (1) Reads Envoy edge `X-Request-ID`, (2) Falls back to W3C `traceparent` Trace ID, (3) Falls back to generated UUID. | **Gin**: `c.Set("request_id", reqID)`; **HTTP Header**: `X-Request-ID: reqID` |
| **`middleware.OTelTraceContext(...)`** | Integrates OpenTelemetry tracing. Extracts span parent context from headers and starts a child span. | **Go Context**: `c.Request.Context()` is updated with the OTel Span context. |
| **`middleware.PrometheusHTTPMetrics(...)`** | Measures HTTP request count, latency, and active inflight count at global scope. | None |
| **`middleware.CookieOriginGuard(...)`** | CSRF prevention. Checks request `Origin` or `Referer` headers against allowed domain hosts. | None |
| **`middleware.RateLimitPreAuth(...)`** | Defends edge computing resources from DDoS. Checks IP token bucket before heavy parsing. | None |
| **`middleware.AccessLog()`** | Logs transaction data (URI, method, duration, client IP, Request ID) at log termination. | None |
| **`middleware.AdminXSSI()`** | Prevents Cross-Site Site Inclusion by prepending `)]}',\n` to JSON responses. | None |

#### 2. Route-specific Middleware (defined in `controlplane/internal/mail/route.go`)

| Middleware | Purpose / Action | Context Injections |
| :--- | :--- | :--- |
| **`middleware.AdminCIDR()`** | Evaluates client IP against compiled allowed CIDRs / static IP whitelist. Fail-Closed. | None |
| **`middleware.AdminAPIKeyAuth()`** | Performs session cookie verification: (1) Decodes JWT from `admin_api_token` candidate key rotation keys, (2) Checks session access secret hash in L2 Redis cache. | **Gin & Go Context**: `constant.ContextKeyUserID` ("user_id") -> `claims.Subject` (Value: `"sre"`); **HTTP Header**: `X-Session-Expires-In` |
| **`middleware.UserZoneAuth()`** | Multi-tenancy isolation. (1) Extracts `zone_code` cookie, (2) Queries L1 `"zone_by_code"` loader to resolve UUID, (3) Compares claims Zone ID with resolved Zone ID. | **Go Context**: `constant.ZoneIDCtxKey` -> resolved `uuid.UUID` (injected via `ContextWithZoneID`) |
| **`middleware.RateLimitPostAuth(...)`** | Anti-probing rate limit based on identity key: `path + clientIP + user_id / access_key`. | None |

---

### 🔄 End-to-End Sequences (Phase-by-Phase)

#### Phase 1: Ingestion & Outbox Persistence Sequence

📌 **Kích hoạt từ:** [NewMailEndpoint.tsx](file:///home/phucle/Desktop/New/admin-ui/src/pages/mail/NewMailEndpoint.tsx) qua [endpoint_handler.go](file:///home/phucle/Desktop/New/controlplane/internal/mail/transport/http/handler/endpoint_handler.go) $\rightarrow$ [endpoint_service_impl.go](file:///home/phucle/Desktop/New/controlplane/internal/mail/service/endpoint_service_impl.go)

```mermaid
sequenceDiagram
    autonumber
    participant UI as Admin UI
    participant MW as Middleware Chain
    participant H as Endpoint Handler
    participant S as Endpoint Service
    participant DB as PostgreSQL

    UI->>MW: POST /admin/mail/endpoints<br/>Payload: {name, host, port, username, password, certs, max_conns, ...}
    Note over MW: Run global middlewares:<br/>RequestID, OTel (TraceID setup), CORS, Pre-Auth RateLimit
    Note over MW: Run route middlewares:<br/>AdminCIDR, AdminAPIKeyAuth, UserZoneAuth, Post-Auth RateLimit
    MW-->>H: Forward Request with context (ZoneID, UserID, TraceID)
    
    H->>H: Bind JSON → CreateEndpointRequest DTO
    H->>H: Validate parameters (Host format, Port range, TLS certificates)
    H->>S: Call CreateEndpoint(ctx, DTO)
    
    S->>S: Extract ZoneID, UserID & OTel TraceID from Go context
    S->>S: Map credentials (password, client_key_pem) in plain text
    S->>S: Build SmtpConfig Protobuf payload & marshal to []byte
    S->>DB: Begin Transaction (Tx)
    S->>DB: INSERT INTO mail_endpoints (status: 'initializing')
    S->>DB: INSERT INTO mail_outbox_records (status: 'PENDING', topic: 'mail.create_endpoint', trace_id, payload)
    S->>DB: Commit Transaction (Tx)
    DB-->>S: Tx Committed Success
    S-->>H: Return success
    H-->>UI: Response HTTP 201 Created ({"status": "initializing", "id": "<endpoint_id>"})
```

#### Phase 2: CDC Logical Replication & Redis Job Ingestion Sequence

📌 **Kích hoạt từ:** `job-proxy` CDC Streamer [mod.rs](file:///home/phucle/Desktop/New/job-proxy/src/cdc/mod.rs)

```mermaid
sequenceDiagram
    autonumber
    participant WAL as PostgreSQL (WAL)
    participant SLOT as pgoutput (Slot)
    participant JP_CONN as pgwire (Replication Client)
    participant JP_STR as CdcStreamer (Loop)
    participant JP_MAP as relation_map (HashMap)
    participant JP_PROC as CdcStreamer (process_insert)
    participant RD as Redis (jobs:zone_id Stream)

    Note over WAL: INSERT transaction is committed<br/>WAL record is generated on disk
    WAL->>SLOT: Decode WAL record change details
    SLOT->>JP_CONN: Stream ReplicationEvent::XLogData via TCP stream (pgoutput format)
    JP_CONN->>JP_STR: Receive raw binary message bytes
    
    alt Tag Byte = 'R' (Relation Definition)
        JP_STR->>JP_STR: Parse relation info using parse_relation_message()
        JP_STR->>JP_MAP: Cache table schema mapping (relation_id -> PgOutputRelation)
        JP_MAP-->>JP_STR: Cache Updated
    else Tag Byte = 'I' (Insert Event)
        JP_STR->>JP_STR: Read relation_id (4 bytes)
        JP_STR->>JP_MAP: Lookup PgOutputRelation by relation_id
        JP_MAP-->>JP_STR: Return cached schema structure
        JP_STR->>JP_STR: Check if table name matches config.cdc_sources
        
        alt If relation_name is monitored (e.g. mail_outbox_records)
            JP_STR->>JP_STR: Decode field values using parse_insert_message()
            JP_STR->>JP_PROC: Call process_insert(fields)
            
            Note over JP_PROC: 1. Extract event_id, zone_id, job_topic, payload, trace_id<br/>2. Decode trace_id bytea string -> 32-char hex trace_id
            
            JP_PROC->>RD: XADD jobs:zone_id *<br/>Payload: {event_id, job_topic, payload, trace_id, ...}
            
            alt Redis Write Success
                RD-->>JP_PROC: Return generated Stream ID (e.g. "171879...-0")
                JP_PROC->>JP_PROC: Log receipt step: cdc.recv_wal (Logger::job_log)
                JP_PROC->>JP_PROC: Increment MetricsManager::inc_wal_records_read()
                JP_PROC-->>JP_STR: Return Ok(())
                JP_STR->>JP_CONN: Call client.update_applied_lsn(wal_end)
                JP_CONN->>SLOT: Send Standby Status Update (ACK LSN position)
                Note over SLOT: Advance slot LSN pointer on disk<br/>Release WAL segments safely
            else Redis Write Failed (Network / OOM / Connection Lost)
                RD-->>JP_PROC: Return Err(RedisError)
                JP_PROC-->>JP_STR: Return Err(error)
                Note over JP_STR: Abort run_replication_stream loop without calling update_applied_lsn
                JP_STR->>JP_STR: Trigger reconnect loop (sleep 5s, reconnect to pg slot)
                Note over SLOT: WAL remains unacknowledged.<br/>Re-streams from last ACKed LSN on reconnect.
            end
        else Relation is ignored
            Note over JP_STR: Discard message and advance LSN to skip
            JP_STR->>JP_CONN: Call client.update_applied_lsn(wal_end)
        end
    end
```

#### Phase 3: Dataplane Execution

📌 **Kích hoạt từ:** `dataplane` active worker [runner.rs](file:///home/phucle/Desktop/New/dataplane/src/job_lifecycle/runner.rs)

##### Phase 3.1: Macro-level Cluster Flow

Sơ đồ phân phối tổng quan giữa các Node Dataplane trong cụm High Availability:

```mermaid
sequenceDiagram
    autonumber
    participant RJ as Redis Job System (Stream)
    participant DP1 as Dataplane 1 (Active Node)
    participant RZ as Redis Zone (L2 Cache)
    participant DP2 as Dataplane 2 (HA Node)
    participant DPN as Dataplane n (HA Node)
    participant RJ_RES as Redis Stream (job_results_stream)

    RJ->>DP1: IngestionDaemon consumes sync task (XREADGROUP jobs:zone_id)
    DP1->>RZ: Acquire Distributed Lease Lock (SET locks:job:<job_id> NX PX 30000)
    Note over DP1: If acquired, start JobRunner::run_job<br/>and scope trace_id into Context
    DP1->>DP1: Register lock in ActiveLockRegistry (Watchdog extending lock key every 10s)
    DP1->>DP1: Dispatch to SmtpSyncExecutor (sync_endpoint.rs)
    Note over DP1: Decode SmtpEndpointSync Protobuf payload
    
    DP1->>RZ: Write binary config (SET mail:zone:<zone_id>:endpoints:<endpoint_id>)
    DP1->>RZ: Write routing metadata JSON (HSET mail:zone:<zone_id>:server_pool <endpoint_id> <metadata_json>)
    DP1->>RZ: Broadcast sync event (PUBLISH mail:zone:<zone_id>:endpoint_events)
    Note over DP1: Payload: {event_type: "sync", endpoint_id: <id>, metadata: <metadata>}
    
    DP1->>DP1: Reload L1 Cache (remove_endpoint & update_metadata in MailServerPool)
    DP1->>RJ_RES: Report PROCESSING & outcome status (XADD job_results)
    Note over DP1: ExecutionCleanupGuard is dropped:<br/>Deregister from Watchdog, DEL lease lock, decrement active_jobs
    DP1->>RJ: Acknowledge stream message (XACK)
    
    Note over DP2, DPN: Pub/Sub Listener thread (app.rs) is subscribing to endpoint_events
    RZ-->>DP2: Receive Sync event via Pub/Sub
    Note over DP2: Directly parse metadata from Pub/Sub payload JSON
    DP2->>DP2: Update local metadata cache (update_metadata in MailServerPool L1 RAM)
    
    RZ-->>DPN: Receive Sync event via Pub/Sub
    Note over DPN: Directly parse metadata from Pub/Sub payload JSON
    DPN->>DPN: Update local metadata cache (update_metadata in MailServerPool L1 RAM)
```

##### Phase 3.2: Micro-level Single Node Flow

Sơ đồ tuần tự chi tiết biểu diễn các thành phần nội bộ (Internal Components) bên trong một node Dataplane xử lý luồng đồng bộ:

```mermaid
sequenceDiagram
    autonumber
    participant R_JOB as Redis Job Stream
    participant ING as Ingestion Loop (JobConsumer)
    participant RUN as Runner Task (JobRunner)
    participant REG as ActiveLockRegistry
    participant WD as Watchdog Loop
    participant ROUT as Mail Router (delivery)
    participant EXEC as SmtpSyncExecutor (sync_endpoint)
    participant L1 as L1 Cache (MailServerPool)
    participant R_ZONE as Redis Zone (L2 Cache)
    
    ING->>R_JOB: XREADGROUP jobs:zone_id
    R_JOB-->>ING: Return JobPayload (Protobuf)
    ING->>R_ZONE: SET locks:job:<job_id> NX PX 30000
    R_ZONE-->>ING: Lock acquired successfully
    ING->>RUN: Send payload via MPSC Channel
    
    Note over RUN: Spawn async task & scope trace_id to thread-local Context
    RUN->>REG: register(lock_key)
    Note over WD, REG: Background Watchdog loop runs every 10s:<br/>Reads active locks from Registry,<br/>extends Redis TTL via renew_lease_lock()
    WD->>REG: check active locks
    WD->>R_ZONE: renew_lease_lock()
    
    RUN->>R_JOB: Report PROCESSING status (XADD job_results)
    RUN->>ROUT: dispatch_mail_job(action, payload)
    ROUT->>EXEC: execute(payload)
    
    Note over EXEC: decode SmtpEndpointSync Protobuf bytes
    EXEC->>R_ZONE: SET mail:zone:<zone_id>:endpoints:<endpoint_id> (binary config)
    EXEC->>R_ZONE: HSET mail:zone:<zone_id>:server_pool <endpoint_id> <metadata_json>
    EXEC->>R_ZONE: PUBLISH mail:zone:<zone_id>:endpoint_events <sync_event_json>
    EXEC->>L1: remove_endpoint(endpoint_id) (Close old SMTP Actor connections)
    EXEC->>L1: update_metadata(endpoint_id, metadata) (Save to L1 RAM)
    EXEC-->>ROUT: Return Ok(ExecutionResult)
    ROUT-->>RUN: Return Ok(ExecutionResult)
    
    RUN->>R_JOB: Report SUCCEEDED/FAILED status (XADD job_results)
    
    Note over RUN: ExecutionCleanupGuard drop triggers:
    RUN->>REG: deregister(lock_key) (Stop Watchdog renewals)
    RUN->>R_ZONE: release_lease_lock (DEL lock_key)
    RUN->>R_JOB: XACK jobs:zone_id message
```

##### Phase 3.3: Giải thích kiến trúc & Tối ưu hóa trên Dataplane

Để đạt được tiêu chuẩn **Cloud Native** và tính **High Availability (HA)** cao nhất, hệ thống phân tách bộ nhớ đệm thành L1 Cache (RAM cục bộ), L2 Cache (Redis Zone) và kênh truyền tin đồng bộ Pub/Sub theo sơ đồ so sánh dưới đây:

| Thành phần | Cơ chế lưu trữ | Dữ liệu lưu trữ | Thời điểm Đọc / Ghi | Vai trò & T�##### Phase 3.4: Mô hình Định tuyến & Phát Thư Trực Tiếp (Direct LMTP Delivery)

Nhằm tối ưu hóa hiệu năng, giải phóng hoàn toàn Dataplane khỏi việc chờ đợi kết nối SMTP vật lý ra ngoài internet, hệ thống áp dụng cơ chế kết xuất (render) và đẩy trực tiếp (MIME/EML stream) sang Stalwart MTA qua giao thức LMTP:

> [!TIP]
> **Cơ cấu phân tầng của Mô hình Direct LMTP:**
>
> - **Lớp Định tuyến & Render (Dataplane Node):** Định tuyến chọn Endpoint tối ưu từ RAM L1 và tiến hành render nội dung email thô (MIME) trong RAM. Tác vụ chạy bất đồng bộ phi chặn cực nhanh.
> - **Lớp Chuyển tiếp (LMTP Connection):** Kết nối TCP nội bộ LMTP (cổng 24) truyền tải dòng email thô vừa render sang Stalwart.
> - **Lớp Bộ đệm & Phát thư (Stalwart Spool):** Stalwart lưu email nhận được vào thư mục hàng gửi trên đĩa (disk spool queue) và tự động xử lý gửi ra internet ngầm.

```mermaid
stateDiagram-v2
    [*] --> Dataplane_Idle : Chờ job gửi mail (jobs:zone_id)
    Dataplane_Idle --> Render_Mail : Nhận job từ Redis Stream
    Render_Mail --> LMTP_Relay : Định tuyến & Render HTML/MIME hoàn tất
    
    state Stalwart_MTA_Delivery {
        LMTP_Relay --> Write_Spool : Giao tiếp LMTP (Cổng 24) nội bộ
        Write_Spool --> Deliver_Internet : Spool xuống đĩa và trả về 250 OK
        Deliver_Internet --> [*] : Gửi ra Internet thành công
    }
    
    LMTP_Relay --> Dataplane_ACK : Stalwart báo nhận thành công (250 OK)
    Dataplane_ACK --> Dataplane_Idle : XACK và xóa job trên jobs:zone_id
    
    LMTP_Relay --> Job_Retry : Stalwart offline / Lỗi kết nối TCP LMTP
    Job_Retry --> Dataplane_Idle : NFI (Không XACK) -> Job quay lại stream để thử lại
```

**Chi tiết Cơ chế vận hành:**

1. **Băng thông phi chặn (Non-blocking Outbound):** Node Dataplane không trực tiếp mở kết nối SMTP ra ngoài internet. Nhờ đó, luồng xử lý job của Dataplane kết thúc cực nhanh (tổng thời gian render + gửi LMTP nội bộ < 2ms), giải phóng CPU/Worker cho các job khác.
2. **LMTP Relay hiệu năng cao:** LMTP (Local Mail Transfer Protocol) được thiết kế riêng cho việc chuyển tiếp thư nội bộ cục bộ, bỏ qua lớp xác thực SMTP phức tạp và cơ chế đàm thoại bắt tay nặng nề, cho phép nạp trực tiếp luồng email thô vào MTA với tốc độ mạng nội bộ.
3. **Bền vững dữ liệu (Fault Tolerance):** Nếu cụm Stalwart MTA sập hoặc quá tải đĩa cứng, kết nối LMTP sẽ thất bại. Dataplane phát hiện lỗi, từ chối XACK job và thả trôi job trên Redis Stream. Job sẽ tự động được gán lại cho node Dataplane khác hoặc thử lại sau khi Stalwart phục hồi.
4. **Phân tách luồng gửi thư (Hybrid Routing Paths):**
   - **Luồng gửi chính (Stalwart MTA Cluster):** Dataplane render email và đẩy trực tiếp qua LMTP vào Stalwart nội bộ. Toàn bộ phần hàng đợi gửi ra ngoài internet (Spool) được ủy nhiệm hoàn toàn cho Stalwart quản lý.
   - **Luồng gửi ngoại vi (External Providers - SES, Sendgrid, Mailgun):** Chỉ khi gửi qua các SMTP Endpoint bên ngoài, Dataplane mới kích hoạt các Actor SMTP để đàm thoại SMTP trực tiếp đến Provider tương ứng.

##### Phase 3.5: Nhánh xử lý L2 Cache Miss & Cơ chế Lazy Reload (Đối với External SMTP)

Trong trường hợp cấu hình Endpoint bên ngoài bị mất tại Redis L2 Cache (ví dụ do Redis khởi động lại hoặc hết bộ nhớ dẫn đến eviction), khi Dataplane cần lấy credentials để kết nối SMTP ngoại vi, cơ chế **Lazy Reload** tự chữa lành (Self-Healing) sẽ được kích hoạt:

```mermaid
sequenceDiagram
    autonumber
    participant DP as Dataplane Node
    participant R_ZONE as Redis Zone (L2 Cache)
    participant JP as Job-Proxy (Control Ingestion)
    participant DB as PostgreSQL
    participant R_JOB as Redis Job Stream (jobs:zone_id)

    DP->>R_ZONE: GET mail:zone:<zone_id>:endpoints:<endpoint_id>
    R_ZONE-->>DP: Trả về Nil/None (Cache Miss)
    
    DP->>R_ZONE: PUBLISH mail:zone:<zone_id>:sync_request { "endpoint_id": "<id>" }
    Note over DP: Báo lỗi tạm thời & đưa job gửi mail vào trạng thái Chờ (Pending)
    
    Note over JP: Job-Proxy lắng nghe kênh sync_request
    R_ZONE-->>JP: Nhận yêu cầu Lazy Reload
    JP->>DB: SELECT * FROM mail_endpoints WHERE id = <endpoint_id>
    DB-->>JP: Trả về cấu hình SMTP chi tiết
    JP->>JP: Đóng gói dữ liệu thành SmtpEndpointSync Protobuf
    JP->>R_JOB: XADD jobs:zone_id * payload: SmtpEndpointSync (Action: sync_endpoint)
    
    Note over DP: Dataplane Node consume job sync_endpoint từ Redis
    R_JOB->>DP: Consume & nạp lại Redis L2 Cache
    
    Note over DP: Dataplane thử lại (Retry) job gửi mail ban đầu
    DP->>R_ZONE: GET mail:zone:<zone_id>:endpoints:<endpoint_id> (Cache Hit!)
    R_ZONE-->>DP: Trả về binary config và kết nối SMTP Actor thành công
```

**Mô tả chi tiết luồng xử lý:**

1. **Phát hiện Cache Miss:** Khi Dataplane cần đọc cấu hình Endpoint bên ngoài mà lệnh `GET` từ Redis L2 trả về rỗng, tiến trình xác định đây là sự cố thất lạc cache.
2. **Yêu cầu Nạp lại (Lazy Reload Request):** Node phát tín hiệu yêu cầu nạp lại lên kênh Pub/Sub `mail:zone:<zone_id>:sync_request`. Đồng thời, tin nhắn gửi thư hiện tại tạm thời được giữ trong bộ nhớ và thử lại sau ít giây.
3. **Đồng bộ tự động từ L0:** `job-proxy` nhận sự kiện, truy vấn PostgreSQL để lấy lại cấu hình gốc và đẩy job `sync_endpoint` vào Redis Job Stream của zone.
4. **Tái nạp cache:** Node Dataplane consume job, ghi lại cấu hình vào Redis L2. Lần thử lại tiếp theo của Dataplane sẽ truy xuất thành công cấu hình mới và tiến hành gửi thư qua SMTP Actor bình thường.

📌 **Phạm vi áp dụng của Lazy Reload:**

- **Dành cho các SMTP Endpoint bên ngoài (External Providers - SES, Sendgrid, Mailgun):** Do số lượng và cấu hình chứng chỉ/credentials của bên thứ 3 có thể rất lớn và thay đổi liên tục, cơ chế Lazy Reload giúp hệ thống không cần nạp sẵn tất cả credentials nhạy cảm lên RAM L1/L2 của các Node Dataplane, tránh quá tải tài nguyên RAM khi chưa dùng đến.
- **Đối với cụm Stalwart Mail Server nội bộ:** Thông số kết nối nội bộ được nạp sẵn khi khởi động cụm. Dataplane chỉ đùn dữ liệu email thô đã render trực tiếp sang cổng LMTP mà không cần nạp đi nạp lại credentials, tối ưu tuyệt đối thông lượng gửi thư chính.

#### Phase 4: Feedback Callback & Real-Time Notification Sequence

📌 **Kích hoạt từ:** `job-proxy` Result Consumer [consumer.rs](file:///home/phucle/Desktop/New/job-proxy/src/result_consumer/consumer.rs) $\rightarrow$ `notification-service` $\rightarrow$ Centrifugo WebSocket Gateway $\rightarrow$ Admin UI Browser

```mermaid
sequenceDiagram
    autonumber
    participant RJ_RES as Redis Stream (job_results_stream)
    participant JP_RC as Job-Proxy (Result Consumer)
    participant DB as PostgreSQL
    participant RDS_NOTIF as Redis Stream (stream:job_notifications)
    participant NS as Notification Service
    participant CF as Centrifugo WebSocket Gateway
    participant UI as Admin UI Browser

    RJ_RES->>JP_RC: Consume sync result (XREADGROUP)
    Note over JP_RC: Decode JobExecutionResultProto & extract trace_id
    JP_RC->>DB: UPDATE mail_outbox_records SET status = 'SUCCEEDED', completed_at = NOW() WHERE event_id = <job_id>
    JP_RC->>DB: UPDATE mail_endpoints SET status = 'active' WHERE id = <endpoint_id>
    
    Note over JP_RC: Bind OTel Span context using trace_id
    JP_RC->>JP_RC: Map outcome to JobNotificationEvent Protobuf
    JP_RC->>RDS_NOTIF: Push binary event data (XADD stream:job_notifications)
    JP_RC->>RJ_RES: Acknowledge message (XACK)
    
    RDS_NOTIF->>NS: Consume event (XREADGROUP)
    Note over NS: Decode binary Protobuf into JobNotificationEvent & parse trace_parent
    NS->>NS: Construct simplified client JSON payload
    NS->>CF: Publish to channel personal:<user_id> (HTTP POST /api/publish)
    CF-->>NS: Return publish success
    NS->>RDS_NOTIF: Acknowledge message (XACK)
    
    CF->>UI: Broadcast real-time message via persistent WebSocket
    Note over UI: Display Toast notification (SUCCESS / FAILED)<br/>Unlock initializing state & reload grid
```

---

---

### 📊 Database Schema & Field Mappings

Bảng lưu trữ chính thức **`mail_endpoints`** và cấu trúc đồng bộ thông qua **`mail_outbox_records`**:

#### 1. Bảng `mail_endpoints` (Source of Truth L0)

Lưu trữ thông tin cấu hình vật lý. Các trường nhạy cảm hiện tại được lưu dưới dạng plain text.

| Tên Cột | Kiểu Dữ Liệu | Ràng Buộc | Ý Nghĩa / Cách Xử Lý |
| :--- | :--- | :--- | :--- |
| `id` | `VARCHAR(64)` | `PRIMARY KEY` | Sinh bằng UUID v7 phía Go App |
| `zone_id` | `VARCHAR(64)` | `NOT NULL` | Zone ID sở hữu endpoint này |
| `name` | `VARCHAR(255)` | `NOT NULL` | Tên gợi nhớ của SMTP Endpoint |
| `host` | `VARCHAR(255)` | `NOT NULL` | Địa chỉ IP/Domain của SMTP Server |
| `port` | `INT` | `NOT NULL` | Cổng SMTP (25, 465, 587) |
| `username` | `VARCHAR(255)` | `NULL` | Tài khoản đăng nhập SMTP |
| `password` | `TEXT` | `NULL` | Mật khẩu SMTP. Hiện tại lưu dưới dạng plain text |
| `tls_mode` | `mail_tls_mode` | `NOT NULL` | none, starttls, tls, mtls |
| `status` | `mail_endpoint_status` | `NOT NULL` | planned, active, disabled |
| `max_connections` | `INT` | `NOT NULL (Default 10)` | Giới hạn Connection Pool |
| `priority` | `INT` | `NOT NULL (Default 100)` | Độ ưu tiên định tuyến |
| `weight` | `INT` | `NOT NULL (Default 1)` | Trọng số phục vụ thuật toán load balancing |
| `ca_cert_pem` | `TEXT` | `NULL` | CA certificate block dạng PEM |
| `client_cert_pem` | `TEXT` | `NULL` | Client certificate block dạng PEM |
| `client_key_pem` | `TEXT` | `NULL` | Client private key block. Hiện tại lưu dưới dạng plain text |
| `is_active` | `BOOLEAN` | `NOT NULL (Default TRUE)` | Cờ kích hoạt nhanh |
| `created_at` | `TIMESTAMP WITH TZ` | `NOT NULL DEFAULT NOW()` | Thời điểm khởi tạo |
| `updated_at` | `TIMESTAMP WITH TZ` | `NOT NULL DEFAULT NOW()` | Thời điểm cập nhật gần nhất |

#### 2. Bảng `mail_outbox_records` (Transactional Outbox)

Được chèn đồng thời trong transaction để làm kênh vận chuyển CDC.

- `job_topic`: `"mail.create_endpoint"`
- `resource_id`: `endpoint_id` của bản ghi vừa chèn.
- `payload`: Binary Protobuf chứa toàn bộ thông tin của endpoint (bao gồm credentials dạng plain text).
- `status`: Khởi tạo là `'PENDING'`, chuyển thành `'SUCCEEDED'` sau khi CDC push thành công sang Redis Job Stream.

---

### ⚡ Redis L2 Storage Schema (Zone Cache)

Tại Redis của từng Zone, cấu hình Endpoint được tổ chức dưới dạng **Hash** để tối ưu hóa hiệu năng đọc/ghi và tiết kiệm bộ nhớ:

- **Key**: `mail:zone:<zone_id>:endpoints:<endpoint_id>`
- **TTL**: Vô hạn (chỉ bị xóa khi nhận sự kiện DELETE).

**Chi tiết các Field trong Hash:**

```json
{
  "id": "<endpoint_id>",
  "name": "Target-SMTP-Production",
  "host": "smtp.sendgrid.net",
  "port": "587",
  "username": "apikey",
  "password": "raw-smtp-password", // Dạng plain text
  "tls_mode": "starttls",
  "max_connections": "50",
  "priority": "10",
  "weight": "3",
  "ca_cert_pem": "-----BEGIN CERTIFICATE-----\n...",
  "client_cert_pem": "-----BEGIN CERTIFICATE-----\n...",
  "client_key_pem": "-----BEGIN PRIVATE KEY-----\n...", // Dạng plain text
  "updated_at": "1781938918231" // Epoch millisecond phục vụ Last-Write-Wins
}
```

---

## <a id="state-machine"></a>📊 4. State Machine Của Endpoint Cache (Cache State Machine)

Trạng thái cấu hình của Endpoint tại Dataplane L1 Memory Cache và Redis L2 Cache được điều phối thông qua các sự kiện đồng bộ.

### ⚙️ Biểu Đồ Chuyển Đổi Trạng Trạng Thái Cache (Cache State Diagram)

```mermaid
stateDiagram-v2
    [*] --> L2_MISS : Endpoint created in DB (No Cache yet)
    
    L2_MISS --> L2_HIT_L1_MISS : job-proxy writes L2 Hash (CDC Sync)
    
    L2_HIT_L1_MISS --> L1_ACTIVE : Dataplane loads L2 configuration into Memory (Sync event / Lazy load)
    
    L1_ACTIVE --> L2_HIT_L1_MISS : Config updated in DB -> Cache Invalidation triggered -> L1 cleared
    L1_ACTIVE --> L2_MISS : Endpoint deleted in DB -> L2 deleted & L1 cleared
    
    L1_ACTIVE --> [*]
```

### 📋 Bảng Chuyển Trạng Thái (Cache State Transition Matrix)

| Trạng Thái Đầu | Sự Kiện (Trigger) | Trạng Thái Đích | Hành Động Thực Thi | Kiểm Soát Điều Kiện (Guard) |
| :--- | :--- | :--- | :--- | :--- |
| **`L2_MISS`** | Dataplane consume từ Redis Job Stream | **`L2_HIT_L1_MISS`** | Dataplane ghi Hash vào Redis L2 Zone, Publish event `SYNC` | Không có |
| **`L2_HIT_L1_MISS`** | Nhận event `SYNC` (via Zone PubSub) hoặc Gửi mail (Cache miss) | **`L1_ACTIVE`** | Dataplane đọc L2 Redis Zone, giải mã credentials, khởi tạo Connection Pool và đưa vào RAM (L1) | `updated_at` (L2) >= `updated_at` (L1) |
| **`L1_ACTIVE`** | DB Endpoint Update (Dataplane consume sync) | **`L2_HIT_L1_MISS`** | Dataplane ghi đè L2 Redis Zone. Các node hủy Connection Pool cũ, dọn RAM L1. | Phải drain toàn bộ active TCP connections trước khi giải phóng pool |
| **`L1_ACTIVE`** / **`L2_HIT_L1_MISS`** | DB Endpoint Delete (Dataplane consume delete) | **`L2_MISS`** | Xóa Hash L2 Redis Zone, các node hủy pool L1, dọn sạch RAM | Không có |

---

## <a id="race-condition"></a>⚡ 5. Xử Lý Race Condition & Bảo Mật (Race Condition & Security)

Để đảm bảo tính bền vững của dữ liệu và an toàn bảo mật trong môi trường Cloud-Native phân tán có độ trễ mạng biến động, các giải pháp sau được áp dụng:

### 1. Giải quyết cập nhật đồng thời (Last-Write-Wins via Timestamp)

- **Bài toán**: Admin cập nhật cấu hình Endpoint liên tiếp 2 lần trong thời gian ngắn. Do độ trễ mạng hoặc CDC lag, sự kiện cập nhật thứ 1 đến Dataplane muộn hơn sự kiện thứ 2, dẫn đến nguy cơ Dataplane ghi đè cấu hình mới bằng cấu hình cũ.
- **Giải quyết**:
  - Mỗi khi ghi nhận thay đổi ở Database, trường `updated_at` dạng Epoch Milliseconds sẽ được đính kèm vào payload Outbox.
  - Khi nhận task sync, Dataplane cập nhật L2 Redis Zone sử dụng script LUA thực hiện kiểm tra so sánh: Chỉ ghi đè nếu `updated_at_incoming > updated_at_redis_current`.
  - Tương tự tại L1 cache (Dataplane RAM), pool kết nối chỉ được thay thế nếu metadata version/timestamp của cấu hình nạp vào mới hơn timestamp hiện tại của pool đang chạy.

### 2. Ngăn ngừa nghẽn cổ chai khi khởi động đồng loạt (Thundering Herd Mitigation)

- **Bài toán**: Khi cụm Dataplane scale-out (ví dụ từ 2 lên 10 instances) hoặc sau khi Redis Zone L2 cục bộ bị sập và khởi động lại, hàng loạt Dataplane node bị cache miss đồng thời và cùng gửi yêu cầu đồng bộ cấu hình, gây quá tải hệ thống truyền tải (Redis Job System và Job-Proxy).
- **Giải quyết**:
  - Áp dụng **Singleflight Pattern** hoặc **Distributed Mutex** tại Dataplane. Khi xảy ra cache miss, chỉ duy nhất 1 thread đại diện thực hiện gửi một yêu cầu đồng bộ `"mail.reconcile_endpoint"` lên Redis Job System, các thread/request khác chờ cấu hình được đồng bộ thông qua Redis Stream.
  - Khi cấu hình được nạp lại thành công vào L2 Redis và L1 RAM qua stream, tất cả các thread đang chờ sẽ được giải phóng và đọc trực tiếp từ cache.

### 3. Giải phóng kết nối an toàn (Connection Pool Draining)

- **Bài toán**: Khi đổi thông số Host/Port hoặc credentials của Endpoint, Dataplane cần cập nhật pool kết nối. Nếu hủy ngang pool kết nối ngay lập tức, các email đang trong tiến trình gửi dở dang sẽ bị đứt gãy kết nối (network reset error).
- **Giải quyết**:
  - Khi nhận event `SYNC` cập nhật cấu hình, Dataplane đánh dấu Connection Pool cũ là `Draining`.
  - Một connection pool mới được khởi tạo với cấu hình mới để xử lý các email mới nạp vào.
  - Pool cũ (ở trạng thái `Draining`) sẽ từ chối nhận email mới, nhưng cho phép các email đang chạy hoàn tất gửi thư trong một khoảng thời gian grace-period (ví dụ: 15 giây). Sau thời gian grace-period, pool cũ sẽ đóng hoàn toàn tất cả TCP sockets còn lại và giải phóng bộ nhớ.

### 4. An toàn bảo mật thông tin nhạy cảm (Security & Zero Trust)

- **Lưu trữ thông tin nhạy cảm**:
  - **At-Rest (DB)**: `password` và `client_key_pem` tạm thời được lưu ở dạng plain text thô trong cơ sở dữ liệu Postgres (sẽ áp dụng cơ chế mã hóa toàn bộ payload ở các phiên bản tiếp theo).
  - **In-Transit (CDC & Redis)**: Truyền tải giữa Controlplane -> Postgres -> job-proxy -> Redis qua kết nối TLS bảo mật. Dữ liệu trong Redis L2 được giữ ở dạng plain text thô.
  - **In-Memory (RAM)**: Dataplane nạp credentials thô trực tiếp vào bộ nhớ RAM phục vụ cho việc khởi tạo Connection Pool kết nối SMTP và không bao giờ ghi logs hay in ra màn hình các giá trị nhạy cảm này.
- **Cô lập Zone (Zone-level Isolation)**:
  - Cấu hình của Endpoint chỉ được đồng bộ và phân phối về cụm cache Redis của Zone sở hữu. Dataplane tại Zone khác không có quyền truy cập hay nhìn thấy thông số của Endpoint này.

### 5. Cơ chế Tự Phục Hồi & Tải Chậm (Self-Healing & Lazy Loading Fallback)

- **Bài toán**: Trong môi trường Cloud-Native, các sự cố như scale-up đột ngột (node mới chưa có L1 RAM cache), hoặc cụm Redis Zone L2 cục bộ bị sập/reset (mất L2 cache) có thể khiến Dataplane không tìm thấy cấu hình Endpoint khi cần xử lý gửi thư. Do nguyên tắc Zero-Trust, Dataplane tuyệt đối không được phép gọi gRPC/HTTP trực tiếp ngược lên Controlplane. Đồng thời, Job-Proxy không kết nối trực tiếp đến Redis Zone L2 cục bộ (chúng là các thực thể Redis khác nhau, xem [docker-compose.dev.yml](file:///home/phucle/Desktop/New/controlplane/docker-compose.dev.yml)).
- **Giải quyết**:
  - **Mô hình Tự Phục Hồi Bất Đồng Bộ Một Chiều (One-way Self-Healing via Redis Job System)**:
    - **Sự cố mất dữ liệu toàn Zone (Zone-level Recovery)**: Khi Dataplane (node active hoặc watchdog) phát hiện Redis Zone L2 cục bộ vừa online trở lại (hoặc phát hiện cache bị rỗng do flush/restart), nó sẽ đẩy một yêu cầu khôi phục lên Redis Job System (`redis-job` thông qua `job_results_stream`) với topic `"mail.reconcile_zone"`.
    - **Sự cố thiếu cấu hình đơn lẻ (Endpoint-level Lazy Loading)**: Khi Dataplane nhận email task yêu cầu `endpoint_id` nhưng gặp cache-miss ở cả L1 RAM và Redis Zone L2 cục bộ, nó sẽ đẩy một yêu cầu đồng bộ Endpoint đơn lẻ lên Redis Job System với topic `"mail.reconcile_endpoint"`.
    - **Xử lý tại Job-Proxy**: Job-Proxy Result Consumer lắng nghe `job_results_stream` trên Redis Job System. Khi nhận được yêu cầu troubleshoot:
      - Đối với `"mail.reconcile_zone"`: Job-Proxy truy vấn Postgres DB lấy toàn bộ các Endpoint đang hoạt động của Zone đó và phát các sync tasks tương ứng vào Redis Stream `jobs:<zone_id>` trên Redis Job System.
      - Đối với `"mail.reconcile_endpoint"`: Job-Proxy truy vấn Postgres DB lấy cấu hình Endpoint cụ thể đó và đẩy sync task vào Redis Stream `jobs:<zone_id>`.
    - **Dataplane tái tạo cache**: Dataplane consume các sync tasks từ Redis Job System, ghi cấu hình mới nhận được vào Redis Zone L2 cục bộ của nó, và reload RAM L1 cục bộ để tiếp tục phục vụ tác vụ gửi thư.

---

## <a id="telemetry"></a>📡 6. Giám Sát Và Truy Vết - Grafana Runbook (Telemetry & VictoriaMetrics Stack)

SRE và USA sử dụng hệ sinh thái **VictoriaMetrics Stack** để kiểm tra và khắc phục sự cố liên quan đến luồng đồng bộ cấu hình Endpoint.

```mermaid
graph TD
    Grafana["📊 Grafana"] --> VM["📈 VictoriaMetrics (Cache Hit/Miss Metrics)"]
    Grafana --> VL["📝 VictoriaLogs (Sync Event Logs)"]
    Grafana --> VT["🕸️ VictoriaTraces (OTel trace_id tracking)"]
```

### 🔍 Hướng dẫn trích xuất logs qua Trace ID (VictoriaLogs)

Mọi yêu cầu tạo Endpoint từ UI đều mang theo một `trace_id` duy nhất. USA/SRE dùng LogsQL để gom toàn bộ logs liên quan:

```sql
# Truy vết logs đồng bộ hóa Endpoint theo trace_id cụ thể
container_name:~"controlplane-dev-.*|aurora-job-proxy|dataplane-.*" AND "trace_id" AND "<trace_id>"
```

#### Logs lỗi ghi Database tại Controlplane

```sql
container_name:~"controlplane-dev-.*" AND "CreateEndpoint" AND (error OR fail)
```

#### Logs job-proxy đẩy dữ liệu sang Redis Job Stream

```sql
container_name:"aurora-job-proxy" AND "sync_endpoint" AND (error OR success)
```

#### Logs Dataplane cập nhật Redis Zone L2

```sql
container_name:~"dataplane-.*" AND "redis_zone.sync_endpoint" AND (error OR success)
```

#### Logs Dataplane nhận tín hiệu sync và reload pool

```sql
container_name:~"dataplane-.*" AND "endpoint_cache.reload"
```

---

### 📈 Chỉ số đo đạc hiệu năng cache (VictoriaMetrics PromQL)

#### Tỉ lệ hit/miss cache của Endpoint tại Dataplane

```promql
# Tỉ lệ cache hit L1 Memory
sum(rate(dataplane_endpoint_cache_hit_total{level="L1"}[5m])) / 
(sum(rate(dataplane_endpoint_cache_hit_total{level="L1"}[5m])) + sum(rate(dataplane_endpoint_cache_miss_total{level="L1"}[5m])))
```

```promql
# Tỉ lệ cache hit L2 Redis
sum(rate(dataplane_endpoint_cache_hit_total{level="L2"}[5m])) / 
(sum(rate(dataplane_endpoint_cache_hit_total{level="L2"}[5m])) + sum(rate(dataplane_endpoint_cache_miss_total{level="L2"}[5m])))
```

#### Thời gian trễ đồng bộ (CDC Sync Lag)

Đo khoảng thời gian từ lúc Endpoint được commit ở DB cho tới khi Dataplane hoàn tất reload cache:

```promql
# Độ trễ đồng bộ hóa cấu hình (giây)
histogram_quantile(0.99, sum(rate(dataplane_endpoint_sync_latency_seconds_bucket[5m])) by (le, zone_id))
```

#### Số lượng Connection Pool đang hoạt động

```promql
# Tổng số connection pools SMTP đang chạy trên Dataplane
dataplane_active_connection_pools_count
```

---

### 🕸️ Kế thừa Vết Tracing (VictoriaTraces / Jaeger)

Sơ đồ Spans thời gian thực thể hiện luồng đồng bộ cấu hình bất đồng bộ:

```text
[HTTP POST /admin/mail/endpoints] (Controlplane - Go)
  |
  +-- [db.transaction.commit] (Postgres Tx)
  |
  +-- [cdc.sync.mail.create_endpoint] (Job-Proxy CDC Extractor)
        |
        +-- [redis_job.push.jobs_stream] (XADD into Redis Job Stream)
              |
              +-- [dataplane.consume.create_endpoint] (Dataplane Rust - Active Worker)
                    |
                    +-- [redis_zone.write.l2_cache] (HMSET into local Redis Zone)
                    +-- [redis_zone.publish.sync_signal] (PUBLISH event)
                    +-- [dataplane.reload_connection_pool] (DP1 - Re-build active SMTP connections)
                    |
                    +-- [dataplane.on_sync_event] (DP2 - Local Subscriber via Redis Zone PubSub)
                          |
                          +-- [dataplane.reload_connection_pool] (DP2 - Re-build active SMTP connections)
```

Nếu span `dataplane.reload_connection_pool` bị trễ hoặc báo đỏ, SRE cần kiểm tra:

1. Các thông số kết nối SMTP (host, port, username, password, certificates) cấu hình tại Endpoint có chính xác hay không.
2. Dataplane có bị cạn kiệt tài nguyên (CPU/RAM) dẫn đến việc khởi tạo Connection Pool bị nghẽn hay không.
