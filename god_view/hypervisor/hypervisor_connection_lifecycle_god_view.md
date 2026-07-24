# Proxmox Hypervisor Connection Lifecycle - Workflow God View

> [!NOTE]
> Tài liệu này đóng vai trò là **Source of Truth (SoT) / God View** cho luồng Kết Nối, Giám Sát Trạng Thái (Healthcheck) và Vòng Đời Kết Nối (Connection Lifecycle) của các máy chủ ảo hóa Proxmox Hypervisor Node.
> Mọi thay đổi liên quan đến schema `hypervisor`, controlplane Go module, job-orchestrator, dataplane agent, và Admin UI bắt buộc phải tuân thủ nghiêm ngặt đặc tả này.

---

## 🗺️ 1. Giới Thiệu & Kiến Trúc Tổng Quan

Hệ thống quản lý Hypervisor triển khai theo mô hình **Decoupled Architecture**:
1. **Dataplane Agent** đóng vai trò là bên duy nhất lưu trữ cấu hình kết nối vật lý và trực tiếp giữ kết nối tới Proxmox Cluster thông qua biến môi trường.
2. **Controlplane (Platform Level)** hoàn toàn không lưu thông tin nhạy cảm (như API endpoint, API tokens của Proxmox) nhằm giảm thiểu tối đa rủi ro bảo mật (Blast Radius). Controlplane chỉ đóng vai trò tracking trạng thái logic của hạ tầng được report từ Dataplane.
3. Luồng đồng bộ trạng thái tái sử dụng **Zone Status Gateway** và Kafka topic **`aurora.zone.reports.v1`**.

### 🌐 Sơ đồ Điều Phối Request & Trạng Thái (System Dataflow)

```mermaid
graph TD
    classDef client fill:#332244,stroke:#8844ff,stroke-width:2px;
    classDef gateway fill:#112233,stroke:#3388ff,stroke-width:2px;
    classDef storage fill:#333311,stroke:#cccc33,stroke-width:2px;
    classDef backend fill:#221133,stroke:#aa44ff,stroke-width:2px;
    classDef dataplane fill:#113322,stroke:#33cc88,stroke-width:2px;

    SRE["💻 Admin UI (SRE Admin)"]:::client
    Envoy["🛡️ Envoy Gateway"]:::gateway
    acr["🛡️ acr Service (Rust)"]:::gateway
    CP["🚀 Controlplane Go (hypervisor module)"]:::backend
    DB["💾 PostgreSQL SoT (hypervisor schema)"]:::storage
    ZoneKV["🗄️ NATS Zone KV (Health + Coordination)"]:::storage
    RedisL1["⚡ Central Kafka"]:::storage
    JO["🚀 job-orchestrator (Rust Listener)"]:::backend
    DP["💻 Dataplane Agent (Rust)"]:::dataplane
    PVE["🖥️ Proxmox Cluster"]:::dataplane

    %% Luồng đọc của SRE
    SRE -- "1. GET /admin/hypervisor/nodes" --> Envoy
    Envoy -- "2. Check session" --> acr
    acr -- "3. Session OK" --> Envoy
    Envoy -- "4. Forward to" --> CP
    CP -- "5. SELECT from hypervisor.nodes" --> DB
    CP -- "6. HTTP Response JSON" --> SRE

    %% Luồng Auto-discovery & Heartbeat
    DP -- "a. Stable leader polls Nodes & Metrics" --> PVE
    DP -- "b. Leader-fenced PUT zone.service.hypervisor" --> ZoneKV
    
    %% Gateway Gom & Sync L1 (ZoneStatusGateway)
    ZoneKV -- "c. Read zone.service.* snapshots" --> DP
    DP -- "d. PRODUCE aurora.zone.reports.v1" --> RedisL1
    
    %% Platform listener consume & write DB
    RedisL1 -- "e. Manual Kafka poll" --> JO
    JO -- "f. UPSERT Dynamic Nodes" --> DB
```

---

## 🗃️ 2. Thiết Kế Cơ Sở Dữ Liệu & Zone KV

### 1. Current snapshot trong NATS Zone KV
* **Health key**: `AURORA_ZONE_HEALTH/zone.service.hypervisor`
* **Coordination key**: `AURORA_ZONE_COORDINATION/lease.zone.leader`
* **Value**: Một JSON snapshot nguyên khối chứa toàn bộ node của chu kỳ, probe owner và fencing token:
  ```json
  {
    "status": "healthy",
    "nodes": {
      "pve-node-01": {
        "status": "connected",
      "cpu_cores_total": 64,
      "cpu_cores_used": 16,
      "ram_mb_total": 262144,
      "ram_mb_used": 65536,
      "storage_gb_total": 2048,
        "storage_gb_used": 512,
        "updated_at": 1719517200
      }
    },
    "updated_at": 1719517200,
    "probe_node_id": "dataplane-vn-n2",
    "fencing_token": 42
  }
  ```

### 2. Cấu Trúc Bảng Database PostgreSQL (hypervisor schema)
Database không lưu trữ `endpoint` kết nối hay credentials của Proxmox, mà chỉ lưu thông tin phục vụ tracking và xếp lịch VM.

```sql
CREATE SCHEMA IF NOT EXISTS hypervisor;

-- Bảng quản lý trạng thái logic của các Proxmox Nodes (Tự động cập nhật qua Heartbeat)
CREATE TABLE IF NOT EXISTS hypervisor.nodes (
    id UUID PRIMARY KEY, -- Sinh động UUIDv7 khi phát hiện node mới
    zone_id UUID NOT NULL, -- Ánh xạ logic tới hierarchy.zones(id)
    node_code VARCHAR(100) NOT NULL, -- Định danh vật lý nhận từ Proxmox (ví dụ: pve-node-01)
    name VARCHAR(255) NOT NULL,      -- Tên hiển thị thân thiện
    status VARCHAR(32) NOT NULL DEFAULT 'disconnected', -- disconnected, connecting, connected, degraded, maintenance
    
    -- Dung lượng tài nguyên (Capacity metrics)
    cpu_cores_total INT NOT NULL DEFAULT 0,
    cpu_cores_used INT NOT NULL DEFAULT 0,
    ram_mb_total BIGINT NOT NULL DEFAULT 0,
    ram_mb_used BIGINT NOT NULL DEFAULT 0,
    storage_gb_total BIGINT NOT NULL DEFAULT 0,
    storage_gb_used BIGINT NOT NULL DEFAULT 0,
    
    -- Metadata vận hành
    last_active_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    
    CONSTRAINT ux_hypervisor_nodes_zone_code UNIQUE (zone_id, node_code)
);

CREATE INDEX IF NOT EXISTS idx_hypervisor_nodes_zone ON hypervisor.nodes(zone_id);
CREATE INDEX IF NOT EXISTS idx_hypervisor_nodes_status ON hypervisor.nodes(status);
```

---

## 🔄 3. Vòng Đời Kết Nối & Cơ Chế Khám Phá (Auto-Discovery)

### 1. Cơ chế Tự Động Phát Hiện Node (Auto-Discovery)
- SRE **không cần** đăng ký thủ công từng node vật lý của Proxmox Cluster trên Admin UI.
- Dataplane Agent khi khởi động sẽ kết nối tới Proxmox Cluster qua API endpoint (định cấu hình trong biến môi trường của Dataplane), tự động thực hiện truy vấn API danh sách node vật lý của Cluster đó.
- Danh sách node được ghi thành current snapshot trong Zone Health KV rồi Zone Gateway đẩy lên Platform.
- Phía Platform (`job-orchestrator`), khi nhận báo cáo từ stream, nếu phát hiện `node_code` chưa tồn tại trong bảng `hypervisor.nodes` thuộc `zone_id` tương ứng, hệ thống sẽ tự động thực hiện **INSERT** bản ghi node mới với UUIDv7 được sinh tự động.

### 2. State Machine của Hypervisor Node

```mermaid
stateDiagram-v2
    [*] --> CONNECTING : Dataplane khởi động, tự động quét Proxmox Node & thiết lập handshake
    CONNECTING --> CONNECTED : Handshake Proxmox thành công & Đẩy heartbeat đầu tiên lên Platform
    CONNECTING --> DISCONNECTED : Handshake thất bại / Timeout kết nối
    
    CONNECTED --> DEGRADED : Proxmox Node quá tải (CPU/RAM > 90%)
    DEGRADED --> CONNECTED : Tài nguyên phục hồi bình thường (< 90%)
    
    CONNECTED --> DISCONNECTED : Mất heartbeat từ Dataplane quá 45s (3 chu kỳ)
    DEGRADED --> DISCONNECTED : Mất heartbeat từ Dataplane quá 45s
    
    CONNECTED --> MAINTENANCE : SRE chủ động đặt từ Admin UI
    MAINTENANCE --> CONNECTED : SRE tắt chế độ bảo trì
    MAINTENANCE --> DISCONNECTED : Mất kết nối vật lý trong khi bảo trì
```

---

## 🏛️ 4. Mô Tả Chi Tiết Luồng Xử Lý (Sequence Diagrams)

### Luồng A: SRE Đọc Trạng Thái Giám Sát Hypervisor (Read Flow)
*(Xác thực session qua acr gRPC Gatekeeper, bypass Ed25519 & OTP Step-Up vì đây là luồng Read-only. Không truyền User ID hay User Roles, chỉ yêu cầu zone_id cụ thể)*

```mermaid
sequenceDiagram
    autonumber
    participant UI as 💻 Admin UI (SRE Client)
    participant Envoy as 🛡️ Envoy Gateway (Edge Proxy)
    participant acr as 🛡️ acr Service (Rust ExtAuthz)
    participant Redis as ⚡ Redis L1 (Sessions)
    participant CP as 🚀 Controlplane (Go Backend)
    participant DB as 💾 PostgreSQL (hypervisor schema)

    UI->>Envoy: GET /admin/hypervisor/nodes?zone_id=<uuid><br/>Cookie: access_token, access_key, access_secret
    
    Note over Envoy,acr: Envoy chuyển gRPC check sang acr service
    Envoy->>acr: CheckRequest (Headers & Cookies)
    
    rect rgb(20, 30, 40)
        Note over acr,Redis: Xác thực Session tại Platform Redis L1
        acr->>Redis: GET Session iam:admin_access_session:<access_key>
        Redis-->>acr: Trả về Session Data (Chứa device_public_key, ash)
        Note over acr: Đối chiếu hash(access_secret) với ash
    end

    alt Session Hợp Lệ
        acr-->>Envoy: CheckResponse OK (status 0)
        
        Envoy->>CP: Forward GET /admin/hypervisor/nodes?zone_id=<uuid>
        
        Note over CP: Hypervisor Handler kiểm tra tính hợp lệ của tham số:
        alt zone_id bị thiếu HOẶC có giá trị 'global' / '*'
            CP-->>Envoy: HTTP 400 Bad Request (Error: zone_id is required and cannot be global)
            Envoy-->>UI: HTTP 400 Bad Request
        else zone_id hợp lệ (UUID cụ thể)
            CP->>DB: SELECT id, zone_id, node_code, name, status, cpu_cores_total, cpu_cores_used, ram_mb_total, ram_mb_used, storage_gb_total, storage_gb_used, last_active_at<br/>FROM hypervisor.nodes WHERE zone_id = <uuid> ORDER BY node_code ASC
            DB-->>CP: Trả về danh sách records của zone tương ứng
            
            CP-->>Envoy: HTTP 200 OK (JSON Payload)
            Envoy-->>UI: HTTP 200 OK (JSON Response)
            Note over UI: Render danh sách Hypervisor Node vật lý của Zone,<br/>kèm thanh trạng thái tải (Capacity) và connection status.
        end
    else Session Không Hợp Lệ / Hết Hạn
        acr-->>Envoy: CheckResponse Denied (status 16 Unauthenticated)
        Envoy-->>UI: HTTP 401 Unauthorized (Redirect to Login)
    end
```

### Luồng B: Auto-Discovery, Healthcheck & Đẩy Tải (Heartbeat Flow)

```mermaid
sequenceDiagram
    autonumber
    participant PVE as 🖥️ Proxmox Cluster
    participant DP as 💻 Dataplane Agent (Rust)
    participant KV as 🗄️ NATS Zone KV
    participant L1 as ⚡ Central Kafka
    participant JO as 🚀 job-orchestrator (Rust)
    participant DB as 💾 PostgreSQL (hypervisor schema)

    Note over DP: [Mỗi 15s] Current Zone leader check loop
    DP->>KV: Verify lease.zone.leader owner + fencing
    DP->>KV: GET zone.metadata, require hypervisor enabled
    DP->>PVE: GET /api2/json/nodes (Query danh sách node vật lý & metrics)
    alt Kết nối Proxmox Thành Công
        PVE-->>DP: Trả về danh sách host & thông số tài nguyên sử dụng
        DP->>DP: Build full nodes snapshot
        DP->>KV: PUT zone.service.hypervisor
    else Kết nối Proxmox Thất Bại
        Note over DP: Đánh dấu tất cả các node thuộc cluster là "disconnected"
        DP->>KV: PUT zone.service.hypervisor (previous nodes disconnected)
    end

    Note over DP: [Mỗi 5s] Leader Zone reporter chạy vòng lặp đồng bộ
    DP->>KV: GET zone.service.mail + zone.service.hypervisor
    KV-->>DP: Trả về current workload snapshots
    DP->>DP: Gom dữ liệu workloads
    DP->>L1: PRODUCE ZoneReport Protobuf, key=zone_id, acks=all
    
    Note over JO: job-orchestrator manual-consume Kafka report topic
    L1-->>JO: Nhận ZoneReport record
    Note over JO: Trích xuất workloads.hypervisors từ payload
    alt Node chưa tồn tại trong DB
        JO->>DB: INSERT INTO hypervisor.nodes (New UUIDv7, zone_id, node_code, metrics, status)
    else Node đã tồn tại
        JO->>DB: UPDATE hypervisor.nodes SET metrics, status, last_active_at<br/>WHERE zone_id, node_code AND last_active_at < sent_at
    end
    JO->>L1: COMMIT offset sau DB side effects
```

---

## 🔒 5. Ranh Giới Bảo Mật & Rủi Ro HA (Security & Reliability Guardrails)

### 1. Đối soát Trạng Thái Máy Ảo (Phòng Ngừa Split-Brain / Out-of-sync)
* **Thách thức**: Người dùng thay đổi trạng thái VM trực tiếp trên Proxmox UI, gây lệch cấu hình với Controlplane.
* **Giải pháp**: 
  - Dataplane Agent chạy một background worker định kỳ 30 giây thực hiện truy vấn trạng thái VM (`GET /api2/json/cluster/resources?type=vm`).
  - Gửi bản đối chiếu VM ID + Trạng thái (running, stopped) về Platform để cập nhật lại DB logic.

### 2. Nguyên Tắc Quyền Tối Thiểu (Least Privilege) cho API Proxmox
* **Thách thức**: Hacker chiếm quyền Dataplane Agent ở biên và phá hủy toàn bộ hạ tầng Proxmox vật lý.
* **Giải pháp**:
  - API Token/Credentials cấu hình cho Dataplane ở biên bắt buộc không sử dụng tài nguyên root (`root@pam`).
  - Phân quyền (PVE Role) chỉ giới hạn trong một Resource Pool cụ thể phục vụ tạo/xóa VM, loại bỏ các quyền quản lý Cluster, Storage cấu hình, và Network vật lý.

### 3. Tối ưu hóa API Query (Tránh Rate Limiting Proxmox)
* **Thách thức**: Việc query liên tục danh sách Node và VM lên API Proxmox làm nghẽn tiến trình `pvedaemon` / `pveproxy`.
* **Giải pháp**:
  - Dataplane Agent lưu cache kết quả query API trong bộ nhớ RAM tạm thời.
  - Sử dụng cơ chế gộp request (Request Batching) thay vì gửi nhiều query đơn lẻ.

### 4. Tự Phục Hồi & Phát Hiện Node Chết (Dead Man's Switch)
* **Giải pháp**:
  - `job-orchestrator` duy trì Dead Man's Switch: Nếu cả zone quá 30 giây không gửi report lên stream, toàn bộ hypervisors của zone đó tự động chuyển sang `disconnected`.
  - Nếu zone vẫn gửi report nhưng một node cụ thể trong report đó không được cập nhật trạng thái mới hoặc bị biến mất khỏi report quá 45 giây, hệ thống tự động đánh dấu node đó là `disconnected`.
* **Phòng ngừa Race Condition (Out-of-order Heartbeats)**: Sử dụng cơ chế kiểm tra `last_active_at` khi cập nhật DB. 
  ```sql
  UPDATE hypervisor.nodes 
  SET status = $1, cpu_cores_used = $2, ..., last_active_at = $3
  WHERE zone_id = $4 AND node_code = $5 AND last_active_at < $3;

### 3. Tự Phục Hồi & Phát Hiện Node Chết (Dead Man's Switch)
* **Giải pháp**:
  - `job-orchestrator` kế thừa **Dead Man's Switch** của Zone report listener. Nếu cả Zone quá 30 giây không có Kafka report mới, current health bị hạ theo observation fence; desired/lifecycle state vẫn thuộc SRE.
  - Nếu zone vẫn gửi report nhưng một node cụ thể biến mất hoặc không được cập nhật trong `zone.service.hypervisor` quá 45 giây, `job-orchestrator` đánh dấu node đó là `disconnected`.
