# Storage Object Management (STS & Client-Direct S3) — God View (Master SoT)

> [!IMPORTANT]
> Tài liệu này là **Source of Truth (SoT) duy nhất** cho toàn bộ vòng đời và luồng xử lý cấp quyền truy cập đối tượng tạm thời (STS - Security Token Service) và các thao tác (List/Head Metadata/Get Tags/Upload/Download/Delete) trực tiếp dưới client trong phân hệ Storage.
> **Mọi thay đổi** liên quan đến: API Gateway route, cấu trúc Outbox table, cơ chế CDC của Job Orchestrator, thuật toán tạo Service Account/AssumeRole của Dataplane local zone, và cấu hình Client-side SDK **đều phải tham chiếu và cập nhật** tệp này trước.

---

## Mục Lục

1. [Tổng Quan Kiến Trúc & Chuỗi Nhân Quả](#1-tổng-quan-kiến-trúc--chuỗi-nhân-quả)
2. [Cơ Chế Chống Lỗi IDOR & Bảo Mật Khoá Ký](#2-cơ-chế-chống-lỗi-idor--bảo-mật-khoá-ký)
3. [Database Query Contract & Outbox Schema](#3-database-query-contract--outbox-schema)
4. [Luồng Kỹ Thuật Chi Tiết — End-to-End](#4-luồng-kỹ-thuật-chi-tiết--end-to-end)
   - [Phase 1: Yêu cầu STS Token tại Control Plane (Hồ Chí Minh)](#phase-1-yêu-cầu-sts-token-tại-control-plane-hồ-chí-minh)
   - [Phase 2: CDC & Dispatcher tại Job Orchestrator (Central)](#phase-2-cdc--dispatcher-tại-job-orchestrator-central)
   - [Phase 3: Tạo STS Credentials tại Dataplane (Hà Nội Local)](#phase-3-tạo-sts-credentials-tại-dataplane-hà-nội-local)
   - [Phase 4: Nhận Token và Client-Direct S3 Query](#phase-4-nhận-token-và-client-direct-s3-query)
5. [Bảo Vệ Tải & HA Guards](#5-bảo-vệ-tải--ha-guards)
6. [Observability (Giám Sát Vận Hành)](#6-observability-giám-sát-vận-hành)
7. [Danh Sách Keys (Redis, NATS & Centrifugo)](#7-danh-sách-keys-redis-nats--centrifugo)
8. [Tham Chiếu Code Toàn Hệ Thống](#8-tham-chiếu-code-toàn-hệ-thống)

---

## 1. Tổng Quan Kiến Trúc & Chuỗi Nhân Quả

Hệ thống quản lý đối tượng (Object Management) vận hành theo nguyên lý **phân tách địa lý**, **least privilege**, và **Client-Direct S3**:
* **Control Plane (HCM)**: Không được mở kết nối trực tiếp đến MinIO Cluster (Hà Nội). CP chỉ đóng vai trò xác thực quyền (IDOR) và điều phối việc cấp quyền truy cập tạm thời thông qua Outbox.
* **Dataplane (Hà Nội)**: Giữ thông tin Admin Credentials nội bộ, kết nối trực tiếp với MinIO Cluster để sinh bộ khoá tạm thời (STS Credentials) thông qua API `AssumeRole` hoặc MinIO Service Account.
* **Mô hình Client-Direct S3 qua STS (Security Token Service)**:
  1. **Ủy quyền một lần (Single Bootstrap)**: Khi người dùng mở giao diện quản lý tệp tin của Bucket, Control Plane yêu cầu Dataplane khởi tạo một bộ tài khoản tạm thời **STS Credentials** (AccessKey, SecretKey, SessionToken, Expiry - hiệu lực 30 phút) thông qua Outbox Job.
  2. **Thao tác dữ liệu trực tiếp (Client-Direct)**: Trình duyệt React sử dụng S3 Client nội bộ, nạp bộ tài khoản tạm thời này để tự ký Signature V4 và gửi request trực tiếp lên **Envoy Storage local (Hà Nội)**. Mọi thao tác List Objects, Head Metadata, Get Object Tagging, Upload, Download, và Delete đều được xử lý tức thời (dưới 50ms), hoàn toàn bypass qua Control Plane (HCM) và Job Orchestrator.
  3. **Ràng buộc Least Privilege**: Bộ tài khoản tạm thời được giới hạn chỉ được thao tác trong duy nhất bucket đích qua Inline Policy.

```mermaid
flowchart TD
    classDef ui fill:#1e3a8a,stroke:#3b82f6,color:#ffffff,stroke-width:2px;
    classDef cp fill:#1f2937,stroke:#9ca3af,color:#ffffff,stroke-width:2px;
    classDef db fill:#5b21b6,stroke:#8b5cf6,color:#ffffff,stroke-width:2px;
    classDef queue fill:#7c2d12,stroke:#ea580c,color:#ffffff,stroke-width:2px;
    classDef dp fill:#064e3b,stroke:#10b981,color:#ffffff,stroke-width:2px;

    %% Client / UI
    UI["💻 Console UI (Client Browser)"]:::ui
    Envoy_S3["🛡️ Envoy Storage Gateway (Hà Nội)"]:::ui
    MinIO["🗄️ MinIO S3 Cluster (Hà Nội)"]:::dp

    %% Control Plane (HCM)
    CP["🚀 Control Plane (HCM)"]:::cp
    PG_DB["💾 PostgreSQL Storage DB (HCM)"]:::db

    %% Job Orchestrator (Central)
    JO["⚙️ Job Orchestrator (Central)"]:::cp
    NATS["🧲 NATS Core (Event Bus)"]:::queue
    Centri["📡 Centrifugo WS Gateway"]:::ui

    %% Dataplane (Hà Nội)
    DP["⚙️ Dataplane (Hà Nội Local)"]:::dp
    Redis_DP["⚡ Redis Local (Zone Stream)"]:::queue

    %% 1. Đăng ký yêu cầu STS
    UI -->|1. POST /buckets/:id/sts-token| CP
    CP -->|2. Check Session & IDOR| CP
    CP -->|3. INSERT outbox record| PG_DB
    CP -->|"4. HTTP 202 Accepted (transaction_id)"| UI

    %% 2. Truyền Job đi
    PG_DB -.->|"5. CDC (Logical Replication)"| JO
    JO -->|"6. Dispatch Job (XADD)"| Redis_DP
    Redis_DP -.->|7. Read Stream| DP

    %% 3. Thực thi xin cấp STS
    DP -->|8. Request STS:AssumeRole| MinIO
    MinIO -->|9. Return STS Credentials| DP
    DP -->|"10. Push Job Result (XADD)"| Redis_DP
    Redis_DP -.->|11. Read Result Stream| JO

    %% 4. Trả kết quả về UI
    JO -->|12. DELETE outbox record| PG_DB
    JO -->|13. Publish complete event| NATS
    NATS -->|14. Push credentials payload| Centri
    Centri -.->|"15. WebSocket Push: STS Credentials"| UI
    
    %% 5. Thao tác trực tiếp
    UI ====>|16. List / Head / Tags / Upload / Download / Delete (Signed with STS)| Envoy_S3
    Envoy_S3 ====>|17. Forward request| MinIO
```

---

## 2. Cơ Chế Chống Lỗi IDOR & Bảo Mật Khoá Ký

Để bảo vệ tuyệt đối an toàn thông tin và chống lại các cuộc tấn công đánh cắp credentials tạm thời (XSS) ở phía Client Browser:

### Lớp 1: Chống lỗi IDOR tại Control Plane (HCM)
* CP **không bao giờ** tin tưởng các tham số tên bucket vật lý hay folder path do client truyền lên.
* Client chỉ truyền lên `bucket_id` (UUID).
* CP thực hiện câu lệnh SQL JOIN bắt buộc với `hierarchy.personal_workspaces` để xác nhận User thực thi thực sự là Owner của workspace chứa bucket đó:
  ```sql
  SELECT b.name, b.zone_id 
  FROM storage.personal_buckets b
  JOIN hierarchy.personal_workspaces w ON b.workspace_id = w.id
  WHERE b.id = $1 AND w.owner_id = $2;
  ```
  Nếu không khớp, CP trả về `403 Forbidden` lập tức, không ghi Outbox Job.

### Lớp 2: Bảo mật Khoá Ký và Giới Hạn Phạm Vi (Least Privilege STS)
* Thay vì trả về khoá Master Admin, Dataplane tạo một bộ khoá tạm thời (STS) với Inline Policy cực kỳ chặt chẽ chỉ cho phép làm việc trên đúng một bucket và các keys con thuộc bucket đó: `"Resource": ["arn:aws:s3:::<bucket_name>", "arn:aws:s3:::<bucket_name>/*"]`.
* Bộ khoá tạm thời (STS) này chỉ tồn tại trong vòng tối đa **30 phút**. Sau 30 phút, bộ khoá sẽ tự động bị vô hiệu hóa ở tầng MinIO, hạn chế tối đa rủi ro bị khai thác nếu client bị tấn công XSS.

---

## 3. Database Query Contract & Outbox Schema

* **Bảng Outbox**: `storage_outbox_records` trong schema `storage`.
* **Topic Job mới**:
  1. `storage.object.sts`: Job yêu cầu Dataplane tạo STS credentials ngắn hạn cho bucket chỉ định.

### Contract Insert Outbox:
```sql
INSERT INTO storage.storage_outbox_records (
    event_id, routing_scope, job_topic, payload, user_id, status
) VALUES ($1, $2, $3, $4, $5, 'PENDING');
```
* **`$1`**: UUID sinh cho `transaction_id`.
* **`$2`**: `zone:<zone_id>` trích xuất từ cấu hình bucket (để định tuyến tới Dataplane local của zone đó).
* **`$3`**: `"storage.object.sts"`.
* **`$4`**: Payload Protobuf `ObjectStsRequest` chứa metadata yêu cầu (`bucket_name`, `duration_seconds`).

---

## 4. Luồng Kỹ Thuật Chi Tiết — End-to-End

### Phase 1: Yêu cầu STS Token tại Control Plane (Hồ Chí Minh)

#### A. Phân tích chi tiết trao đổi thông tin (Protocol Specification)

```
[Client Browser] 
    |
    | (1) POST /api/v1/storage/buckets/:id/sts-token
    | Cookies: access_token, workspace_id, etc.
    |
[Envoy Ingress Gateway]
    |
    | (2) gRPC CheckRequest (mang theo Cookies)
    |
[acr (Edge Authz)]
    |
    |-- a. Giải mã JWT 'access_token' -> lấy user_id
    |-- b. Đọc Redis -> lấy zone_id của workspace
    |-- c. Rewrite URI -> /api/v1/personal/storage/buckets/:id/sts-token
    |
    | (3) gRPC CheckResponse OK + Inject Headers (X-User-Id, X-Workspace-Id, X-Zone-Id, etc.)
    |
[Envoy Ingress Gateway]
    |
    | (4) Forward request kèm các Injected Headers
    |
[Control Plane (HCM) - Go]
    |
    |-- a. Auth Middleware: So khớp Permission Key
    |-- b. IDOR Check: SELECT b.name FROM storage.personal_buckets JOIN personal_workspaces ... WHERE owner_id = X-User-Id
    |-- c. Sinh transaction_id (UUID v7)
    |-- d. INSERT PENDING record vào bảng storage_outbox_records (job_topic: "storage.object.sts")
    |
    | (5) HTTP 202 Accepted (Body: { "transaction_id": "<uuid>" })
    v
[Client Browser]
```

#### B. Payload chi tiết của request:

1. **Yêu cầu cấp khoá tạm thời (Object STS Request)**:
   * **Client gửi**:
     * Method / URL: `POST /api/v1/storage/buckets/:id/sts-token`
     * Body (JSON):
       ```json
       {
         "bucket_name": "ws-a1b2c3d4-mybucket",
         "duration_seconds": 1800
       }
       ```
   * **Control Plane nhận**:
     * Headers: `X-User-Id`, `X-Workspace-Id`, `X-Zone-Id`, v.v.
     * Body: JSON chứa `bucket_name`, `duration_seconds`.
     * **Control Plane ghi Outbox Record**:
       * `job_topic`: `"storage.object.sts"`
       * `payload`: `ObjectStsRequest` Protobuf chứa đầy đủ thông tin yêu cầu.
   * **Control Plane trả về**:
     * Status code: `202 Accepted`
     * Body (JSON): `{ "transaction_id": "6a2f3e8b-11c9-4a4b-9eef-1234567890ab" }`

---

### Phase 2: CDC & Dispatcher tại Job Orchestrator (Central)

1. **Job Orchestrator (Rust)** bắt được log replication (CDC Stream) của dòng mới chèn vào `storage.storage_outbox_records`.
2. **JO phân tích**:
   * Kiểm tra cột `status` == `'PENDING'`.
   * Đọc `routing_scope` để biết zone đích (ví dụ: `'zone:vn-han-1'`).
3. **JO chuyển tiếp (Dispatch)**:
   * JO đẩy Job thô vào Redis Stream local của zone tương ứng trên Redis Job Broker:
     `XADD storage:jobs:vn-han-1 * id <job_id> topic <job_topic> payload <binary_payload> trace_id <trace_id>`
   * Cập nhật trạng thái outbox record trong DB sang `'PROCESSING'`.

---

### Phase 3: Tạo STS Credentials tại Dataplane (Hà Nội Local)

1. **Dataplane (Rust)** tại Hà Nội block read từ Redis Stream local:
   `XREADGROUP GROUP dp_group local_worker STREAMS storage:jobs:vn-han-1 >`
2. **DP phân giải Job**:
   * Giải mã binary payload của Job.
   * Lấy Admin credentials truy cập MinIO được quản lý local.
3. **DP gọi API STS hoặc Admin API của MinIO**:
   * Thực hiện gọi API `AssumeRole` lên MinIO, truyền vào thời gian hết hạn (ví dụ: 1800 giây) và **Inline Policy** giới hạn chặt chẽ chỉ cho phép thao tác trên đúng `bucket_name` này.
   * Bộ khoá trả về chứa: `access_key`, `secret_key`, `session_token`, và `expiration`.
4. **DP trả kết quả**:
   * DP gửi kết quả đã tạo ngược lại cho `job-orchestrator` bằng cách đẩy vào Redis Stream kết quả chung:
     `XADD storage:results * job_id <job_id> status SUCCEEDED payload <result_protobuf>`

---

### Phase 4: Nhận Token và Client-Direct S3 Query

1. **Job Orchestrator (Central)** đọc Redis Stream `storage:results`.
2. **JO xử lý**:
   * Cập nhật Postgres outbox record tương ứng: Đổi `status` thành `'SUCCEEDED'`, cập nhật `completed_at = now()`.
   * **JO phát tin nhắn (NATS Core)**:
     * Topic: `storage.job.completed.<transaction_id>`
     * Payload (JSON): Chứa dữ liệu trả về từ Dataplane:
       ```json
       {
         "status": "COMPLETED",
         "credentials": {
           "access_key": "sts-access-key-id",
           "secret_key": "sts-secret-access-key",
           "session_token": "sts-long-session-token-string...",
           "expiration": "2026-07-15T09:43:27Z"
         }
       }
       ```
3. **Notification Service & Centrifugo**:
   * Dịch vụ Notification lắng nghe topic NATS `storage.job.completed.*`.
   * Nó thực hiện POST API gửi payload này sang **Centrifugo WS Gateway**:
     `POST /api/publish` -> Kênh WebSocket: `storage:result:{transaction_id}`.
4. **Console UI (React)**:
   * Client nhận được push event WebSocket từ Centrifugo:
     * Event name: `"job_completed"`
     * Payload: Chứa `credentials`.
   * **Lưu Cache**: UI lưu Credentials này vào bộ nhớ (RAM state hoặc SessionStorage) với thời gian sống trùng với thời hạn `expiration` (sau đó tự động xóa và xin cấp token mới).
   * **Khởi tạo Client S3 động**: UI nạp SDK `@aws-sdk/client-s3` trực tiếp ở Client, cấu hình với `endpoint` là Envoy Gateway local và truyền bộ khoá STS nhận được.
   * **Thao tác trực tiếp (Bypass hoàn toàn Control Plane & Dataplane)**:
     * **Với List**: UI thực hiện lệnh `ListObjectsV2Command` để duyệt danh sách đối tượng trực tiếp.
     * **Với Metadata & Tags**: Khi người dùng click chọn tệp tin, UI thực hiện đồng thời `HeadObjectCommand` và `GetObjectTaggingCommand` để lấy thông tin mô tả chi tiết và nhãn (tags) của tệp tin. Độ trễ hiển thị chỉ còn **< 50ms**.
     * **Với Upload**: UI thực hiện `PutObjectCommand` để tải tệp lên trực tiếp.
     * **Với Download**: UI gọi `GetObjectCommand` trực tiếp hoặc tạo đường dẫn ngắn hạn bằng Client-side SDK để tải tệp xuống.
     * **Với Delete**: UI thực hiện `DeleteObjectCommand` trực tiếp để xóa tệp.

---

## 5. Bảo Vệ Tải & HA Guards

1. **Bảo vệ tải WAN Network**: Toàn bộ luồng thao tác dữ liệu (List, Get, Put, Delete, Head Metadata) đều đi trực tiếp từ Client tới Envoy Gateway local tại Hà Nội, không tốn băng thông đường truyền WAN liên vùng (HCM - HN) của Control Plane.
2. **Giảm tải outbox**: Không còn các outbox record phát sinh cho từng cú click chuột của người dùng, giúp cơ sở dữ liệu PostgreSQL ở Control Plane luôn hoạt động mượt mà, tránh nghẽn luồng CDC.
3. **Quản lý Token Hết Hạn (Token Expiry Guard)**: Frontend React tự động theo dõi thời hạn hết hạn của Token. Khi token còn 2 phút hiệu lực, nếu người dùng vẫn tương tác tích cực, frontend sẽ thực hiện yêu cầu ngầm (silent bootstrap) để lấy STS token mới mà không gây gián đoạn trải nghiệm.

---

## 6. Observability (Giám Sát Vận Hành)

### 6.1 Logs

| Mã Operation (`op`) | Thành phần | Vị trí | Ý nghĩa / Mục tiêu |
|:---|:---|:---|:---|
| **`storage.object.sts_request`** | Controlplane | `personal_bucket_handler.go` | Tiếp nhận đăng ký job xin cấp STS token |
| **`storage.object.sts_executor`** | Dataplane | `object_signer.rs` | Ghi nhận Dataplane gọi STS tạo credentials thành công |
| **`storage.job.completed`**       | Job Orchestrator | `object_resolve.rs` | Ghi nhận hoàn thành vòng đời job, gửi push notification chứa token |

---

## 7. Danh Sách Keys (Redis, NATS & Centrifugo)

### 7.1 Redis Keys
* **`sizes:<zone_id>`** (Redis Job): Stream chứa dữ liệu quét dung lượng bucket.

### 7.2 NATS & Centrifugo Subjects
* NATS Topic: **`storage.job.completed.<transaction_id>`** (Job Orchestrator -> Notification Service).
* Centrifugo Kênh: **`storage:result:{transaction_id}`** (Centrifugo WS -> Client Browser).

---

## 8. Tham Chiếu Code Toàn Hệ Thống

| Tệp tin | Vị trí định nghĩa | Vai trò trong luồng |
|:---|:---|:---|
| **Go Route** | [`route.go`](../../controlplane/internal/storage/route.go) | Khai báo route đăng ký API requests |
| **Go Handler** | [`personal_bucket_handler.go`](../../controlplane/internal/storage/transport/http/handler/personal_bucket_handler.go) | Handler nhận request và ghi outbox |
| **Rust L2 Dispatcher** | [`l2_dispatcher.rs`](../../job-orchestrator/src/reverse_provider/storage/l2_dispatcher.rs) | Định tuyến kết quả job về từ Dataplane |
| **Rust L2 Resolver** | [`object_resolve.rs`](../../job-orchestrator/src/reverse_provider/storage/db/object_resolve.rs) | Cập nhật DB Outbox và bắn NATS |
| **Rust DP Signer** | [`object_signer.rs`](../../dataplane/src/executor/storage/object_signer.rs) | Thực thi gọi API STS tạo credentials |
| **UI Component** | [`ObjectsTab.tsx`](../../cloud-console/src/app/\(console\)/storage/\[id\]/components/ObjectsTab.tsx) | UI gửi request lấy STS, khởi tạo S3 client trực tiếp và gọi Envoy |

