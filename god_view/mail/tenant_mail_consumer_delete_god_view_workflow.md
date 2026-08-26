# Tenant Mail Consumer Delete & Drain — Master God View

Workflow này là **Source of Truth (SoT) duy nhất** quản lý toàn bộ quy trình xóa và xả cạn (`Delete & Drain`) một Mail Consumer doanh nghiệp/tổ chức (Tenant Mail Consumer) trong hệ thống Aurora:
- Tiếp nhận yêu cầu xóa từ giao diện Web Console của Tenant hoặc Backend SDK qua route trung lập `DELETE /api/v1/mail/consumers/:id`.
- Xác thực phiên làm việc, kiểm tra bằng chứng phiên (`Session Proof`), xác minh tư cách thành viên tổ chức (`Active Tenant Membership`), bảo vệ chống replay và rewrite URL sang nhánh nội bộ `/api/v1/tenant/critical/mail/consumers/:id` tại vùng biên ACR ExtAuthz.
- Xử lý toàn bộ logic nghiệp vụ tại Controlplane: thẩm định quyền hạn RBAC `email:consumer:delete` trên phạm vi Tenant Workspace, kiểm tra `expected_config_version`, cấp phát hàng rào phiên bản đơn điệu (`Monotonic Tombstone Fence = current.NextConfigVersion`), niêm phong mã hóa payload Outbox bằng X25519 (`jobpayload.Protector`), và thực thi giao dịch CTE nguyên tử (CTE-First) trên PostgreSQL với **Khóa Thẩm Quyền 4 Chiều kết hợp Ràng buộc Tenant Membership**.
- Job Orchestrator (JO) quét Outbox qua CDC Changefeed, xử lý hàng đợi phân tán chống tranh chấp, định tuyến theo `ZoneID` và phát lệnh vào Kafka Zone Topic với Partition Key = `consumer_id`.
- Dataplane tại Zone tiêu thụ lệnh từ Kafka, giải mã mật mã X25519 bằng Zone Private Key, dừng nhận message mới từ Broker, thực hiện Graceful Drain các message in-flight trong khoảng thời gian `drain_timeout_seconds`, commit offset cuối cùng, đóng TCP connection và giải phóng hoàn toàn bộ nhớ Registry.
- Dataplane đóng gói bản tin kết quả Protobuf `MailJobResultV1`, đẩy sang Kafka Result Topic; Job Orchestrator Result Listener tiêu thụ và thực hiện tất toán nguyên tử (`status = 'SETTLED'`) trên PostgreSQL, sau đó chuyển giao sang Phase 6.
- Nhánh Timeline & Notification thực hiện lưu trữ dòng thời gian sự kiện (`timeline.workspace_events`), ghi nhật ký kiểm toán an ninh (`audit.system_audit_logs`) và đẩy thông báo thời gian thực (Real-time Notification qua WebSocket/SSE) tới giao diện Web Console của Tenant.

---

## 🏛️ API-Scope Contract & Boundary Matrix

| Ranh giới (Boundary) | Thẩm quyền xác thực (Authority) | Trạng thái bền vững (Durable State) |
|---|---|---|
| **Client Browser / SDK** | Cookie `aurora_session`, Header `X-Session-Proof`, Body JSON | Không lưu trữ (Ephemeral in-memory) |
| **Envoy / ACR ExtAuthz** | Xác thực Session Token, Session Proof, Tenant Membership, Rate Limit, Route Rewrite | Auth Redis (`iam:session:{id}`), Rate Limit Sliding Window |
| **Controlplane Mail Service** | Kiểm tra quyền RBAC `email:consumer:delete`, Tombstone Fence, CTE Tx | PostgreSQL (`mail.tenant_mail_consumers`, `mail.mail_outbox_records`) |
| **Job Orchestrator (JO)** | Bắt sự kiện CDC, định tuyến phân vùng Kafka, tất toán Outbox | PostgreSQL (`status = 'SETTLED'`), Kafka Dispatcher |
| **Timeline & Audit Worker** | Lưu vết lịch sử Tenant Workspace và kiểm toán an ninh tổ chức | PostgreSQL (`timeline.workspace_events`, `audit.system_audit_logs`) |
| **Notification Gateway** | Chuyển giao Real-time Event tới WebSocket/SSE Client của Tenant | Ephemeral WebSocket Connections |
| **Dataplane (Zone Worker)** | Thực hiện Graceful Drain, commit final broker offset, giải phóng worker | Broker Consumer Group Lock, Memory Instance Registry |

---

## 🔑 Key & Transport Contract Table

| Khóa / Bảng / Kênh truyền | Vị trí lưu trữ | Thao tác (Operation) | Chủ sở hữu & Ràng buộc bất biến (Owner & Invariant) |
|---|---|---|---|
| `hierarchy.tenant_memberships` | PostgreSQL (Hierarchy DB) | `SELECT 1 WHERE tenant_id=$tenant_id AND user_id=$actor_id AND status='active'` | Xác nhận tư cách thành viên hợp lệ và còn hoạt động trong Tenant. |
| `hierarchy.tenant_workspaces` | PostgreSQL (Hierarchy DB) | `SELECT 1 WHERE id=$workspace_id AND zone_id=$zone_id AND tenant_id=$tenant_id` | Ranh giới chủ quyền Workspace thuộc Tenant và Zone vật lý. |
| `mail.tenant_mail_consumers` | PostgreSQL (Mail DB) | `SELECT config_version FOR UPDATE` | Khóa độc quyền bản ghi consumer; đảm bảo `config_version == expected`. |
| `mail.mail_outbox_records` | PostgreSQL (Mail DB) | `INSERT (job_topic='mail.consumer.delete', status='PENDING')` & `UPDATE SETTLED` | Bản ghi Outbox chứa Protobuf `MailConsumerDeleteV1` đã mã hóa niêm phong X25519. |
| `aurora.jobs.commands.zone.{zone_id}.v1` | Kafka Topic | `Publish` bởi Job Orchestrator | Kênh phát lệnh At-least-once tới Dataplane Zone mục tiêu (Key = `consumer_id`). |
| `aurora.jobs.results.v1` | Kafka Topic | `Publish` bởi Dataplane | Kết quả thực thi từ Zone Dataplane để JO cập nhật trạng thái outbox sang `SETTLED`. |
| `aurora.notifications.events.v1` | Kafka Topic | `Publish` bởi Job Orchestrator | Kênh phát sự kiện realtime cho Notification Gateway và Timeline Worker. |
| `timeline.workspace_events` | PostgreSQL (Timeline DB) | `INSERT` bởi Timeline Worker | Lưu trữ dòng thời gian hiển thị lịch sử hoạt động của Tenant Workspace trên UI. |
| `audit.system_audit_logs` | PostgreSQL (Audit DB) | `INSERT` bởi Audit Logger | Lưu vết kiểm toán bảo mật phục vụ Compliance và Security Audit tổ chức. |

---

## 🌐 Phase 1 — Client → Central Envoy → ACR ExtAuthz Admission

### 1. Phase 1 Input Contract

#### HTTP Request từ Client (Browser / SDK)
- **Method & Path**: `DELETE /api/v1/mail/consumers/0194f83a-8b1e-7d34-92c1-382a1d820001`
- **Headers**:
  - `Cookie: aurora_session=<session_jwt>`
  - `X-Session-Proof: <proof_hash>`
  - `X-Client-Device-ID: <device_uuid>`
  - `Content-Type: application/json`
- **JSON Request Body**:
  ```json
  {
    "expected_config_version": 3,
    "drain_timeout_seconds": 30,
    "reason": "Decommissioning legacy order stream"
  }
  ```

### 2. Phase 1 Processing & Local Output Contract

- **Envoy $\to$ ACR**: Envoy gửi `CheckRequest` chứa method, path, headers và JSON body sang ACR ExtAuthz.
- **ACR Kiểm tra & Phân quyền Biên**:
  1. Xác thực tính hợp lệ của Cookie `aurora_session` và giải mã User `user_id`.
  2. Xác thực tính hợp lệ của Header `X-Session-Proof`.
  3. **Xác minh Tenant Authority**: Kiểm tra quan hệ thành viên hoạt động (`Active Tenant Membership`) của user đối với Tenant được yêu cầu.
  4. Kiểm tra hạn mức Rate Limit theo `(user_id, tenant_id, IP, device_id)`. Vượt ngưỡng $\to$ Trả về **Local 429 Too Many Requests**.
  5. **Path Rewrite**: Chuyển đổi đường dẫn công khai trung lập thành đường dẫn nội bộ nhánh Tenant:
     `DELETE /api/v1/mail/consumers/:id` $\to$ `DELETE /api/v1/tenant/critical/mail/consumers/:id`
  6. **Header Injection (Trusted Context)**:
     - `x-user-id: <actor_user_uuid>`
     - `x-tenant-id: <tenant_uuid>`
     - `x-workspace-id: <workspace_uuid>`
     - `x-zone-id: <zone_uuid>`
     - Loại bỏ toàn bộ header giả mạo từ client.
- **Forward sang Controlplane**: Chuyển tiếp request đã rewrite và inject headers sang Controlplane Mail cluster.

```mermaid
sequenceDiagram
    autonumber
    actor Client as Client (Tenant Web / SDK)
    participant Envoy as Central Envoy Gateway
    participant ACR as ACR ExtAuthz Filter
    participant RL as ACR RateLimiter
    participant Mail as Controlplane Mail

    Client->>Envoy: 1. DELETE /api/v1/mail/consumers/:id (JSON Payload)
    Envoy->>ACR: 2. CheckRequest (Method, Path, Headers, Body, Session Cookie, Proof)

    Note over ACR,RL: Xác thực biên an toàn & Phân quyền Tenant
    ACR->>ACR: 3. Kiểm tra Session JWT, Session Proof & Active Tenant Membership
    alt Session không hợp lệ, thiếu Proof hoặc không thuộc Tenant
        ACR-->>Envoy: Deny 401 Unauthorized / 403 Forbidden
        Envoy-->>Client: 401 Unauthorized / 403 Forbidden
    end

    ACR->>RL: 4. Kiểm tra Rate Limit (User, Tenant & Device)
    alt Vượt ngưỡng Rate Limit
        RL-->>ACR: Rate Exceeded
        ACR-->>Envoy: Deny 429 Too Many Requests
        Envoy-->>Client: 429 Too Many Requests
    end

    Note over ACR: 5. Path Rewrite & Header Injection
    ACR->>ACR: Rewrite path sang /api/v1/tenant/critical/mail/consumers/:id
    ACR->>ACR: Inject x-user-id, x-tenant-id, x-workspace-id, x-zone-id
    ACR-->>Envoy: 6. Allow (Forward upstream with injected headers)
    Envoy->>Mail: 7. Forward request tới Controlplane Mail Cluster
```

---

## ⚡ Phase 2 — Controlplane Processing & Atomic PostgreSQL CTE Deletion

Toàn bộ quy trình xác thực quyền hạn, kiểm tra phiên bản cấu hình, cấp phát hàng rào fence, niêm phong mật mã X25519 và thực thi giao dịch CTE trên cơ sở dữ liệu được xử lý nguyên tử tại Controlplane:

### 1. Khóa Thẩm Quyền 4 Chiều trong CTE (4-Dimension Authority Locking)

Mọi thao tác truy vấn và biến đổi trạng thái trong CTE bắt buộc phải xác lập và kiểm tra đồng thời trên **4 chiều thẩm quyền cô lập của Tenant**:

| Chiều thẩm quyền (Dimension) | Nguồn trích xuất (Source) | Ràng buộc kiểm tra trong PostgreSQL (CTE Condition) | Mục đích bảo mật (Security Invariant) |
|---|---|---|---|
| **1. Tenant ID & Membership** | Trusted Header `x-tenant-id` & `x-user-id` | `hierarchy.tenant_memberships.tenant_id = $tenant_id AND user_id = $actor_id AND status = 'active'` | Ngăn chặn việc xóa consumer khi user không còn là thành viên active của Tenant. |
| **2. Workspace ID** | Trusted Header `x-workspace-id` | `hierarchy.tenant_workspaces.id = $workspace_id AND tenant_id = $tenant_id` | Ranh giới cô lập không gian làm việc của Tenant. |
| **3. Zone ID** | Trusted Header `x-zone-id` | `hierarchy.tenant_workspaces.zone_id = $zone_id` | Bảo đảm tài nguyên thuộc đúng Zone của Workspace. |
| **4. Consumer ID** | Path Param `:id` (UUID) | `mail.tenant_mail_consumers.id = $consumer_id` | Khóa độc quyền đúng dòng Consumer (`FOR UPDATE`). |

### 2. Bảng Ánh Xạ Lỗi Taxonomy (Taxonomy Error Mapping Table)

Khi câu lệnh CTE không chèn được bản ghi Outbox delete (`outboxInserted == false`), tầng Repository phân loại kết quả và ánh xạ chính xác sang lỗi Taxonomy:

| Điều kiện CTE thất bại (Condition) | Mã lỗi Taxonomy (Domain Error) | HTTP Status | Thông điệp phản hồi Client (Client Response) |
|---|---|---|---|
| `!authorized \|\| currentVersion == 0` | `mailTaxonomy.ErrConsumerNotFound` | `404 Not Found` | Consumer không tồn tại hoặc User không có tư cách thành viên active trong Tenant. |
| `live_operation` | `mailTaxonomy.ErrOperationInProgress` | `409 Conflict` | Đang có một tác vụ Outbox dở dang chưa hoàn tất cho Consumer này. |
| `current_version != expected` | `mailTaxonomy.ErrVersionConflict` | `409 Conflict` | Xung đột phiên bản cấu hình (Optimistic Concurrency Control). |
| `drain_timeout_seconds < 1 \|\| > 3600` | `mailTaxonomy.ErrInvalidArgument` | `400 Bad Request` | Thời gian xả cạn `drain_timeout_seconds` phải nằm trong khoảng từ 1 đến 3600 giây. |

```mermaid
sequenceDiagram
    autonumber
    participant Envoy as Central Envoy Gateway
    participant Mid as Middleware (Proof & RBAC)
    participant H as TenantConsumerHandler.Delete
    participant S as tenantConsumerServiceImpl
    participant Prot as jobpayload.Protector
    participant Repo as tenantConsumerRepoPostgres
    participant DB as PostgreSQL Transaction

    Envoy->>Mid: DELETE /api/v1/tenant/critical/mail/consumers/:id
    Mid->>Mid: 1. SessionProof & Authorize("email:consumer:delete")
    alt Thiếu quyền hoặc Proof sai
        Mid-->>Envoy: 403 Forbidden
    end

    Mid->>H: Chuyển giao Handler
    H->>H: 2. Parse UUID và bind DeleteConsumerRequest
    H->>S: DeleteConsumer(ctx, command)

    S->>Repo: 3. GetByID(ctx, query)
    Repo->>DB: Truy vấn consumer hiện tại
    DB-->>Repo: consumer hiện tại
    Repo-->>S: current

    S->>S: 4. Kiểm tra current.ConfigVersion == req.ExpectedConfigVersion
    alt Phiên bản cấu hình không khớp
        S-->>H: ErrVersionConflict (409)
        H-->>Envoy: 409 Conflict
    end

    Note over S: 5. Cấp phát Tombstone Fence & Chuẩn bị Outbox<br/>deleteFence = current.NextConfigVersion

    S->>Repo: Delete(ctx, command, outbox)
    Repo->>Prot: 6. Seal(ctx, metadata, outbox.Payload)
    Prot-->>Repo: Payload đã mã hóa X25519 & KeyID

    Note over Repo,DB: 7. Mở Transaction PostgreSQL (tx.BeginTx)
    Repo->>DB: Thực thi Master CTE Delete (Khóa 4 Chiều):
    Note over DB: CTE thẩm định đồng thời:<br/>- Khóa 4 Chiều (tenant_id, membership, workspace_id, zone_id, consumer_id)<br/>- Consumer Không có Live Operation Pending<br/>- INSERT INTO mail_outbox_records (mail.consumer.delete)
    DB-->>Repo: Kết quả CTE (authorized, currentVersion, outboxInserted, outboxID)

    alt Xung đột phiên bản hoặc có thao tác đang chạy
        Repo->>DB: Rollback Transaction
        Repo-->>S: ErrVersionConflict / ErrOperationInProgress (409)
        S-->>H: Error 409
        H-->>Envoy: 409 Conflict
    else Thành công trọn vẹn
        Repo->>DB: 8. COMMIT Transaction
        Repo-->>S: nil (Thành công)
        S-->>H: success
        H-->>Envoy: 200 OK ({"message": "consumer delete initiated"})
        Envoy-->>Client: 200 OK JSON
    end
```

---

## 🚀 Phase 3 — Job Orchestrator CDC Outbox Dispatch

Phase 3 chịu trách nhiệm tiếp nhận sự kiện Outbox từ cơ sở dữ liệu, định tuyến lệnh theo vùng hạ tầng và đảm bảo chuyển giao tin cậy theo thứ tự nghiêm ngặt (**At-least-once Strictly-Ordered Dispatch**) sang Zone Dataplane:

### 1. Bắt sự kiện CDC & Quản lý Hàng đợi (CDC Capture & Queue Processing)
- **CDC Event Capture**: Job Orchestrator CDC Listener theo dõi changefeed trên bảng `mail.mail_outbox_records`, phát hiện ngay lập tức các bản ghi Outbox mới được commit ở trạng thái `PENDING`.
- **Phân tải Worker & Claim Lease**:
  - Các Job Orchestrator Worker Pod tranh chấp và nhận batch bản ghi outbox an toàn qua cơ chế lock phân tán (`SKIP LOCKED`), ngăn ngừa hoàn toàn double-dispatch.
  - Cập nhật trạng thái bản ghi sang `PROCESSING`, gắn nhãn `claim_owner` và thời điểm `claimed_at`.
- **Xử lý sự cố & Tự động Claim lại (Crash Recovery & Auto-Claim)**:
  - Nếu worker pod gặp sự cố hoặc timeout trong lúc phát lệnh sang Kafka, sau khoảng thời gian thuê (`claimed_at > 60s`), các worker khác sẽ tự động claim lại bản ghi để tái gửi.
  - Giới hạn thử lại tối đa (`max_retries = 5`); vượt quá số lần retry sẽ chuyển sang trạng thái `DEAD_LETTER` và kích hoạt cảnh báo hệ thống.

### 2. Chiến lược Định tuyến & Phân vùng Kafka (Routing & Partitioning Strategy)
- **Định tuyến theo Zone vật lý**:
  - Router trích xuất `zone_id` từ metadata bản ghi và điều hướng lệnh chính xác vào Kafka Topic của Zone mục tiêu: `aurora.jobs.commands.zone.{zone_id}.v1`.
  - Tuyệt đối không gửi chéo vùng; mỗi Zone sở hữu topic và phân vùng cách ly độc lập.
- **Phân vùng nghiêm ngặt theo thực thể (Partitioning Strategy)**:
  - **Partition Key = `consumer_id`**: Đảm bảo tất cả các sự kiện thay đổi vòng đời (Create $\to$ Resume $\to$ Update $\to$ Pause $\to$ Delete) của **cùng một Consumer** luôn được phân bổ vào **cùng một Kafka Partition duy nhất**, loại bỏ hoàn toàn tình trạng Out-of-Order Execution tại Dataplane.
- **Tính toàn vẹn & Niêm phong Mật mã (Wire Security & Invariants)**:
  - Kafka Producer sử dụng cấu hình `acks = all`, `enable.idempotence = true` và `max.in.flight.requests.per.connection = 1`.
  - Payload được bảo vệ nguyên vẹn dưới dạng mã hóa X25519; JO đóng vai trò Zero-Knowledge Relay và không thể giải mã hay sửa đổi nội dung cấu hình nhạy cảm.

```mermaid
sequenceDiagram
    autonumber
    participant DB as PostgreSQL (mail_outbox_records)
    participant CDC as JO CDC Listener
    participant Router as JO Dispatch Router
    participant Kafka as Kafka Cluster (Zone Topic)
    participant DP as Dataplane Zone Ingress

    DB-->>CDC: 1. CDC Changefeed phát hiện Outbox Record mới (PENDING)
    CDC->>DB: 2. Claim Lease bản ghi (status = 'PROCESSING', claim_owner = pod_id)
    DB-->>CDC: Xác nhận claim thành công
    CDC->>Router: 3. Chuyển giao Event (zone_id, consumer_id, sealed_payload, traceparent)

    Note over Router: 4. Định tuyến theo Zone & Partition Key = consumer_id
    Router->>Kafka: 5. Produce vào aurora.jobs.commands.zone.{zone_id}.v1 (Key = consumer_id, acks=all)
    Kafka-->>Router: 6. Kafka ACK (Offset Metadata)

    Note over Kafka,DP: Kênh truyền độc lập của Zone
    Kafka-->>DP: 7. Dataplane Ingress tiêu thụ message theo đúng thứ tự Partition
```

---

## ⚙️ Phase 4 — Dataplane Stream Execution & Graceful Drain

Phase 4 chịu trách nhiệm xả cạn các message đang dở dang, đóng kết nối an toàn với Broker và giải phóng hoàn toàn bộ nhớ:

### 1. Tiếp nhận Message & Giải mã Phong ấn Mật mã (Ingress & X25519 Decryption)
- **Ingress Consumption**: Dataplane Zone Ingress Worker tiêu thụ message từ topic `aurora.jobs.commands.zone.{zone_id}.v1`.
- **Giải mã X25519**: `CryptoEngine` sử dụng Zone Private Key để giải mã phong ấn, trích xuất Protobuf `MailConsumerDeleteV1` (`ConfigVersion = deleteFence`, `DrainTimeoutSeconds`).
- **Tombstone Fence Enforcement**: So khớp `deleteFence >= current.ConfigVersion`. Đảm bảo Dataplane giải phóng đúng instance cũ và không bị xung đột nếu người dùng tạo lại cùng định danh.

### 2. Quản lý Vòng đời Adapter & Xả cạn Graceful Drain (Drain In-Flight & Resource Cleanup)
- **Dừng nhận message mới**:
  - `BrokerAdapter` ngắt nhận message mới từ Broker (Kafka/Redis/NATS/RabbitMQ).
- **Xả cạn In-Flight Message trong `DrainTimeoutSeconds`**:
  - Chờ toàn bộ các message đang trong pipeline template rendering & JMAP delivery hoàn tất trong thời gian tối đa `drain_timeout_seconds` (1–3600s).
  - Nếu hết thời gian mà message vẫn chưa xong $\to$ Kích hoạt hard-close để tránh treo worker.
- **Commit Final Offsets & Đóng TCP Socket**:
  - `BrokerAdapter` commit offset cuối cùng, đóng socket kết nối TCP và rời khỏi Consumer Group.
  - `ConsumerRegistry` xóa hoàn toàn thực thể Consumer khỏi bộ nhớ Dataplane.
- **Chuẩn bị kết quả thực thi**:
  - Đóng gói bản tin `MailJobResultV1` (`job_id = event_id`, `resource_id = consumer_id`, `status = SUCCESS`, `observed_config_version = deleteFence`).

```mermaid
sequenceDiagram
    autonumber
    participant Kafka as Kafka Cluster (Zone Command Topic)
    participant Ingress as Dataplane Zone Ingress
    participant Crypto as Zone CryptoEngine (X25519)
    participant Adapter as BrokerAdapter (Kafka/Redis/NATS)
    participant Broker as External Message Broker
    participant Reg as In-Memory Consumer Registry

    Kafka-->>Ingress: 1. Tiêu thụ Message MailConsumerDeleteV1 (Tombstone Fence)
    Ingress->>Crypto: 2. Yêu cầu giải mã sealed payload bằng Zone Private Key
    Crypto-->>Ingress: 3. Payload Protobuf đã giải mã (DrainTimeoutSeconds = 30s)

    Ingress->>Adapter: 4. Ngắt nhận message mới & Bắt đầu Graceful Drain
    Adapter->>Adapter: 5. Xử lý nốt các batch in-flight messages (Render & Send)
    Adapter->>Broker: 6. Commit final offsets & Đóng TCP connection
    Broker-->>Adapter: 7. Connection closed cleanly

    Ingress->>Reg: 8. Hủy thực thể Consumer khỏi Registry (Memory Cleanup)
    Reg-->>Ingress: OK

    Ingress->>Ingress: 9. Tạo MailJobResultV1 (Status = SUCCESS)
```

---

## 🏁 Phase 5 — Dataplane Result Reporting & Job Orchestrator Settlement

Phase 5 chịu trách nhiệm hoàn tất vòng đời của tác vụ phân tán: Dataplane báo cáo hoàn tất xả cạn và hủy tài nguyên thành công về kênh kết quả tập trung, và Job Orchestrator thực hiện tất toán nguyên tử trên Outbox table:

### 1. Đóng gói Bản tin Báo cáo Kết quả (Result Packaging & Wire Schema)
- **Dataplane Result Publisher**: Đóng gói Protobuf `MailJobResultV1` với các trường dữ liệu tiêu chuẩn:
  - `job_id`: Khóa ngoại liên kết chính xác với `event_id` ban đầu trong `mail.mail_outbox_records`.
  - `resource_id`: Định danh `consumer_id`.
  - `status`: `JOB_STATUS_SUCCESS` (hoặc `JOB_STATUS_FAILED` nếu xảy ra lỗi timeout xả cạn).
  - `observed_config_version`: Hàng rào tombstone version (`deleteFence`).
  - `settled_timestamp`: Thời điểm hoàn tất tại Zone (ISO 8601 UTC).
- **Phát tin lên Kafka Topic kết quả**:
  - Topic: `aurora.jobs.results.v1` (Topic chung toàn hệ thống).
  - Partition Key: `consumer_id`.

### 2. Quy trình Tất toán Outbox & Xử lý Phân nhánh Trạng thái (Idempotent Settlement & State Branching)
- **Job Orchestrator Result Listener** tiêu thụ message từ Kafka `aurora.jobs.results.v1`:
  - **Nhánh `status == SUCCESS`**:
    ```sql
    UPDATE mail.mail_outbox_records
    SET status = 'SETTLED',
        settled_at = NOW(),
        retry_count = retry_count
    WHERE event_id = $job_id
      AND status IN ('PENDING', 'PROCESSING');
    ```
    - Xác nhận thực thể đã hoàn tất giải phóng toàn bộ hạ tầng tại Zone.
    - **Gọi chuyển giao sang Phase 6**: Kích hoạt phát tán sự kiện hoàn tất `OperationSettledEvent` sang Event Bus nội bộ.
  - **Nhánh `status == FAILED`**:
    ```sql
    UPDATE mail.mail_outbox_records
    SET status = 'FAILED',
        settled_at = NOW(),
        error_details = $error_message
    WHERE event_id = $job_id
      AND status IN ('PENDING', 'PROCESSING');
    ```
    - Kích hoạt Reaper Scheduler để dọn dẹp thủ công.
- **Bảo đảm Idempotency**: Nếu kết quả gửi về nhiều lần (do Dataplane retry), câu lệnh SQL chỉ tác động khi bản ghi chưa ở trạng thái `SETTLED` (`RowsAffected == 0` $\to$ Bỏ qua an toàn).

```mermaid
sequenceDiagram
    autonumber
    participant DP as Dataplane Result Publisher
    participant ResKafka as Kafka Cluster (aurora.jobs.results.v1)
    participant JO as JO Result Listener
    participant DB as PostgreSQL (mail_outbox_records)
    participant Bus as Internal Event Bus (Phase 6 Trigger)

    DP->>ResKafka: 1. Produce MailJobResultV1 (status = SUCCESS, version = deleteFence)
    ResKafka-->>JO: 2. Tiêu thụ Message kết quả từ topic

    Note over JO,DB: Xử lý tất toán Idempotent
    JO->>DB: 3. UPDATE mail_outbox_records SET status = 'SETTLED', settled_at = NOW() WHERE event_id = $job_id
    DB-->>JO: 4. Rows Affected = 1 (Tất toán thành công)

    JO->>Bus: 5. Kích hoạt OperationSettledEvent (Chuyển giao Phase 6)
```

---

## 📡 Phase 6 — Timeline Projection, Audit Log & Real-time Notification Dispatch

Phase 6 chịu trách nhiệm phân tán các tác động phụ sau khi tất toán: cập nhật dòng thời gian Tenant Workspace, lưu vết kiểm toán và đẩy thông báo thời gian thực xuống trình duyệt của người dùng thuộc Tenant:

### 1. Lưu trữ Dòng thời gian & Kiểm toán An ninh (Timeline & Audit Persistence)
- **Timeline Worker**:
  - Tiêu thụ `OperationSettledEvent` và chèn bản ghi vào bảng `timeline.workspace_events`:
    ```sql
    INSERT INTO timeline.workspace_events (
        id, workspace_id, actor_id, event_type, entity_type, entity_id, payload, created_at
    ) VALUES (
        gen_random_uuid(), $workspace_id, $actor_user_id, 'MAIL_CONSUMER_DELETED', 'mail_consumer', $consumer_id,
        jsonb_build_object('status', 'DELETED', 'tombstone_version', $delete_fence, 'tenant_id', $tenant_id), NOW()
    );
    ```
- **Audit Logger**:
  - Ghi bản ghi bất biến vào `audit.system_audit_logs` phục vụ tra cứu tuân thủ doanh nghiệp:
    `INSERT INTO audit.system_audit_logs (actor_id, tenant_id, action, resource_type, resource_id, status, created_at) VALUES ($actor_user_id, $tenant_id, 'MAIL_CONSUMER_DELETE', 'mail_consumer', $consumer_id, 'SUCCESS', NOW());`

### 2. Kênh truyền Real-time & Định tuyến WebSocket (Notification Dispatch & Client Routing)
- **Phát Event sang Notification Bus**:
  - JO đẩy bản tin thông báo vào topic `aurora.notifications.events.v1`:
    ```json
    {
      "event_type": "MAIL_CONSUMER_DELETED",
      "tenant_id": "<tenant_uuid>",
      "workspace_id": "<workspace_uuid>",
      "actor_id": "<actor_user_uuid>",
      "consumer_id": "<consumer_uuid>",
      "status": "DELETED",
      "tombstone_version": 3,
      "settled_at": "2026-08-26T18:50:00Z"
    }
    ```
- **Notification Gateway & WebSocket Delivery**:
  - `Notification Gateway` tra cứu danh sách Active Session của các thành viên trong Tenant Workspace (`ws:sessions:tenant:{tenant_id}`).
  - Đẩy thông điệp realtime qua kết nối WebSocket/SSE đang mở tới Web Console của Tenant để gỡ bỏ hoàn toàn consumer khỏi bảng danh sách UI.

```mermaid
sequenceDiagram
    autonumber
    participant Bus as Internal Event Bus
    participant TimeWorker as Timeline Worker
    participant TimeDB as PostgreSQL (timeline.workspace_events)
    participant AuditLog as Audit Logger
    participant AuditDB as PostgreSQL (audit.system_audit_logs)
    participant NotiKafka as Notification Bus (aurora.notifications.events.v1)
    participant NotiGW as Notification Gateway (WebSocket Pod)
    participant Client as Tenant Web Console (Web UI)

    Bus-->>TimeWorker: 1. Tiêu thụ OperationSettledEvent

    par Ghi vết Timeline & Audit
        TimeWorker->>TimeDB: 2a. INSERT INTO timeline.workspace_events (MAIL_CONSUMER_DELETED, tenant_id)
        TimeDB-->>TimeWorker: Saved
    and
        Bus-->>AuditLog: 2b. Tiêu thụ Audit Event
        AuditLog->>AuditDB: 2c. INSERT INTO audit.system_audit_logs (MAIL_CONSUMER_DELETE, SUCCESS)
        AuditDB-->>AuditLog: Saved
    and
        Bus->>NotiKafka: 2d. Publish Event sang aurora.notifications.events.v1
        NotiKafka-->>NotiGW: 3. Notification Gateway nhận Event
        NotiGW->>Client: 4. WebSocket Push Event (Removed from UI, Toast Noti)
    end
```

---

## 🛡️ Exhaustive Failure and Security Rules Matrix

| Tình huống ngoại lệ (Failure Condition) | Hành vi thực tế của hệ thống (Actual System Behavior) | Cơ chế phục hồi (Recovery Mechanism) |
|---|---|---|
| **Người dùng tạo lại consumer trùng mã ngay sau khi xóa** | Cấp phát monotonic tombstone fence đảm bảo outbox delete mang version lớn hơn version cũ, và consumer mới tạo lại sẽ có UUID hoàn toàn độc lập $\to$ Tránh race condition với Dataplane. | Dataplane phân biệt chính xác instance cũ và instance mới qua UUID. |
| **Xung đột phiên bản khi nhiều người cùng xóa/sửa đồng thời** | `target` CTE kiểm tra `config_version != expected_config_version` $\to$ Trả về `409 Conflict` (`ErrVersionConflict`). | UI nhận mã lỗi 409, fetch lại trạng thái mới nhất từ hệ thống. |
| **Consumer đang có một Outbox Operation chưa hoàn tất** | `live_operation` CTE phát hiện bản ghi outbox của consumer ở trạng thái `PENDING`/`PROCESSING` $\to$ Trả về `409 Conflict` (`ErrOperationInProgress`). | Người dùng đợi vài giây cho outbox job trước đó settled rồi bấm Xóa lại. |
| **User bị gỡ khỏi Tenant trước khi request tới DB** | CTE kiểm tra `tenant_memberships` không tìm thấy bản ghi `active` $\to$ Trả về `404 Not Found` (`ErrConsumerNotFound`). | Ngăn chặn hành vi can thiệp trái phép sau khi thu hồi quyền thành viên. |
| **Dataplane xả cạn vượt quá thời gian `drain_timeout_seconds`** | Dataplane tự động kích hoạt hard-close: commit offset đã xong, hủy tiến trình các message còn lại và đóng kết nối broker $\to$ Báo cáo `SUCCESS` về JO. | Đảm bảo worker không bị treo vô hạn (Deadlock Prevention). |

---

## Code map

### Phase 1 — Client Entry, Central Envoy & ACR ExtAuthz
- **ACR ExtAuthz Filter & Route Rewriting**: `acr/src/gateway/ext_authz.rs`
- **ACR Session Proof & Tenant Membership Verifier**: `acr/src/auth/proof.rs`

### Phase 2 — Controlplane Processing & Atomic PostgreSQL CTE Deletion
- **HTTP Route Registration**: `controlplane/internal/mail/route.go` (`DELETE /api/v1/tenant/critical/mail/consumers/:id`)
- **HTTP Handler**: `controlplane/internal/mail/transport/http/handler/tenant_consumer_handler.go` (`Delete`)
- **Domain Service**: `controlplane/internal/mail/service/tenant_consumer_service_impl.go` (`DeleteConsumer`)
- **Taxonomy Errors**: `controlplane/internal/mail/taxonomy/errors.go` (`ErrVersionConflict`, `ErrOperationInProgress`, `ErrConsumerNotFound`)
- **SQL Repository & CTE Master**: `controlplane/internal/mail/repository/tenant_consumer_repo_impl.go` (`Delete`)
- **X25519 Payload Protector**: `controlplane/internal/security/job_payload.go` (`Seal`)
- **Protobuf Wire Schema**: `controlplane/internal/mail/transport/rpc/proto/` (`MailConsumerDeleteV1`)

### Phase 3 — Job Orchestrator CDC Outbox Dispatch
- **Job Orchestrator Changefeed Worker**: `job-orchestrator/src/workers/outbox_listener.rs`
- **Job Orchestrator Partition Dispatcher**: `job-orchestrator/src/dispatcher/kafka_producer.rs`

### Phase 4 — Dataplane Stream Execution & Graceful Drain
- **Dataplane Zone Ingress Consumer**: `dataplane/src/mail/ingress/command_consumer.rs`
- **Dataplane Zone Crypto Engine**: `dataplane/src/crypto/zone_payload.rs`
- **Dataplane In-Memory Consumer Registry**: `dataplane/src/mail/registry/consumer_state.rs`
- **Dataplane Stream Broker Adapter**: `dataplane/src/mail/broker/consumer.rs`

### Phase 5 — Dataplane Result Reporting & Job Orchestrator Settlement
- **Dataplane Result Publisher**: `dataplane/src/mail/broker/reporter.rs`
- **Job Orchestrator Result Settler**: `job-orchestrator/src/workers/result_listener.rs`

### Phase 6 — Timeline Projection, Audit Log & Real-time Notification Dispatch
- **Timeline Projection Worker**: `controlplane/internal/timeline/worker/event_listener.go`
- **Audit Logger**: `controlplane/internal/audit/service/audit_logger.go`
- **Notification Event Publisher**: `job-orchestrator/src/notifier/event_publisher.rs`
- **Realtime Notification Gateway**: `notification-hub/src/gateway/ws_server.rs`
