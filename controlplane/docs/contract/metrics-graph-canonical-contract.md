# Metrics Graph Canonical Contract

Status: Draft v2  
Owner: Platform/Controlplane team  
Scope: Subsystem Metrics Topology, Naming Patterns, and OTel Push Boundaries  

---

## Overview

Tài liệu này đặc tả **Mẫu Tổ Chức Cây Đo Lường** (Metrics Graph Pattern) áp dụng thống nhất cho toàn bộ hệ thống Controlplane. Đây là hợp đồng thiết kế để định hướng cách đặt tên, tổ chức phân cấp, đăng ký, và xuất bản chỉ số (metrics) từ mã nguồn thông qua cơ chế Push-based sử dụng OpenTelemetry Metrics SDK gửi về OTel Collector và lưu trữ tập trung tại VictoriaMetrics.

---

## 1. Naming Pattern & Metrics Graph (Đặt Tên & Sơ Đồ Thu Thập)

### 1.1 Metrics Naming Pattern (Quy tắc đặt tên)

Tất cả các metric được đăng ký trong hệ thống Controlplane và các microservice vệ tinh phải tuân thủ nghiêm ngặt định dạng chuẩn sau:

$$\text{format: } \langle\text{namespace}\rangle\_\langle\text{subsystem}\rangle\_\langle\text{metric\_name}\rangle\_[\text{unit}]$$

Trong đó:

- **`namespace`**: Định danh không gian tên toàn cầu của sản phẩm (e.g., `aurora_controlplane` hoặc `aurora_dataplane`).
- **`subsystem`**: Tên phân hệ/module cụ thể sinh ra metrics (e.g., `http`, `iam`, `core`, `mail`).
- **`metric_name`**: Mô tả nghiệp vụ cốt lõi của metric dưới dạng `snake_case` (e.g., `service_calls`, `downstream_duration`).
- **`unit` (Đơn vị tính):** Bắt buộc phải là:
  - `_total`: Cho Counters (ví dụ: `http_requests_total`, `iam_service_calls_total`).
  - `_seconds`: Cho Histograms hoặc Gauges đo lường thời gian (ví dụ: `http_request_duration_seconds`, `iam_downstream_duration_seconds`).

---

### 1.2 Metrics Collection & OTel Push Flow (Sơ đồ luồng dữ liệu)

Sơ đồ dưới đây minh họa toàn bộ vòng đời của metrics: từ thời điểm được đo lường tại các tầng nghiệp vụ, ghi nhận qua OTel SDK Instruments trong bộ nhớ, đến khi được đẩy tự động dưới dạng OTLP/gRPC payload (Push Model) tới OTel Collector, và lưu trữ tại VictoriaMetrics:

```mermaid
flowchart TD
    %% Colors & Styles Definition
    classDef nsStyle fill:#1F1F35,stroke:#7C4DFF,stroke-width:2px,color:#FFFFFF,font-weight:bold;
    classDef metricStyle fill:#161625,stroke:#4CAF50,stroke-width:1px,color:#B2FF59;
    classDef globalStyle fill:#161625,stroke:#FF9100,stroke-width:1px,color:#FFD180;
    classDef engineStyle fill:#2E1B4E,stroke:#BA68C8,stroke-width:2px,color:#E1BEE7,font-weight:bold;
    classDef extStyle fill:#003366,stroke:#33b5e5,stroke-width:2px,color:#E0F7FA,font-style:italic;

    subgraph Src ["🛠️ Controlplane Process (In-Memory)"]
        
        %% Instrumentations
        subgraph HttpLayer ["🔌 HTTP Layer (Subsystem: http)"]
            H1["http_requests_total"]:::metricStyle
            H2["http_request_duration_seconds"]:::metricStyle
        end

        subgraph ServiceLayer ["💼 Business Service Layer (Subsystem: [module_name])"]
            S1["[module_name]_service_calls_total"]:::metricStyle
            S2["[module_name]_downstream_duration_seconds"]:::metricStyle
        end

        subgraph InfraLayer ["🗄️ Infrastructure Interceptors (Subsystem: dependency)"]
            D1["dependency_duration_seconds"]:::globalStyle
        end

        %% OTel Meter Provider
        MeterProvider["📦 OTel MeterProvider <br/> (Active Push Registry)"]:::nsStyle

        %% In-Memory flow
        H1 & H2 -->|"Record"| MeterProvider
        S1 & S2 -->|"Record"| MeterProvider
        D1 -->|"Record"| MeterProvider
    end

    %% Collector & VictoriaMetrics
    OTelCollector["⚙️ OTel Collector <br/> (OTLP Receiver 4317)"]:::engineStyle
    VictoriaMetrics["🔥 VictoriaMetrics <br/> (Storage Engine)"]:::extStyle

    %% OTLP Push Stream
    MeterProvider ==>|"Push OTLP/gRPC <br/> (Periodic Interval)"| OTelCollector
    OTelCollector ==>|"Write Prometheus Remote Write"| VictoriaMetrics
```

---

### 1.3 Nguyên tắc ranh giới & Bảo mật (Zero-Inbound Observability)

1. **Không mở cổng Scraper (Zero-Inbound Boundary):** Loại bỏ hoàn toàn endpoint `/metrics` HTTP GET. Không chạy bất kỳ HTTP metric listener nào ở các microservices (như port 2112 trước đây). Tất cả chỉ số đều được đẩy (Push-based) một chiều ra ngoài.
2. **Lớp Giao Tiếp (HTTP Layer):** Do HTTP Middleware tự động đo lường (HTTP status, route, latency). Module nghiệp vụ **tuyệt đối không** đo lường các chỉ số HTTP tại Service layer.
3. **Lớp Nghiệp Vụ (Service Layer):** Mỗi module tự định nghĩa và quản lý chỉ số nghiệp vụ của mình thông qua tối đa 2 metrics generic: 1 Counter và 1 Histogram.
4. **Lớp Hạ Tầng (Infrastructure Layer):** Các driver (DB pgx tracer, Redis hook) tự động ghi nhận latency thô qua metric dependency toàn cục (`dependency_duration_seconds`).

---

## 2. Module Metrics Design Pattern (Mẫu Thiết Kế Module)

Mỗi module nghiệp vụ khi triển khai observability cần tuân thủ cấu trúc tối giản gồm đúng 2 metrics vector dùng chung:

### 2.1 Service Calls Counter (`[module]_service_calls_total`)

- **Mục tiêu:** Đo đếm tần suất và phân loại kết quả đầu ra của tất cả nghiệp vụ trong module.
- **Attributes bắt buộc (OTel Labels):** `[]string{"flow", "result", "cache_path"}`
  - `flow`: Tên chức năng/luồng nghiệp vụ cụ thể (e.g. `"register"`, `"login"`, `"create_zone"`).
  - `result`: Outcome code nghiệp vụ (e.g. `"success"`, `"already_exists"`, `"invalid_credentials"`).
  - `cache_path`: Hướng đi của luồng dữ liệu (e.g. `"cache_miss"`, `"cache_hit"`, `"n/a"` nếu không sử dụng).

### 2.2 Downstream Latency Histogram (`[module]_downstream_duration_seconds`)

- **Mục tiêu:** Đo lường latency có chọn lọc của các tác vụ gọi hệ thống ngoài (downstream) để phục vụ cô lập sự cố (isolation).
- **Attributes bắt buộc (OTel Labels):** `[]string{"kind", "operation", "status"}`
  - `kind`: Loại downstream (e.g. `"db"`, `"redis"`, `"crypto"`).
  - `operation`: Tên thao tác nghiệp vụ cụ thể (e.g. `"presence_check"`, `"hash_password"`, `"insert_user"`).
  - `status`: Kết quả thao tác (`"ok"` hoặc `"error"`).

---

## 3. Namespace Registration & Policies Engine (Đăng Ký & Xử Lý Lỗi)

Để tránh việc các module nghiệp vụ tự ý khởi tạo namespace và meter provider riêng lẻ dẫn đến phân mảnh, hệ thống áp dụng cơ chế tự đăng ký ngược (Self-Registration) về trung tâm. Toàn bộ lỗi phát sinh trong quá trình này được định tuyến qua **Observability Policies Engine** để đưa ra quyết định hành vi.

```mermaid
sequenceDiagram
    participant App as App main()
    participant CoreObs as internal/observability (Central)
    participant Policy as Policies Engine (Decider)
    participant ModMetrics as internal/[module]/metrics (Module)

    ModMetrics->>CoreObs: RegisterModuleMetrics(metrics.Register)
    Note over ModMetrics, CoreObs: Đăng ký callback qua init() của module nghiệp vụ

    App->>CoreObs: InitOTel(rawNamespace)
    CoreObs->>CoreObs: Chuẩn hóa Namespace (NormalizeNamespace)
    
    rect rgb(240, 248, 255)
        Note over CoreObs, Policy: Kiểm tra Namespace hợp lệ & Đăng ký Metrics
        CoreObs->>Policy: EvaluateNamespace(namespace)
        alt Namespace Rỗng/Không Hợp Lệ
            Policy-->>CoreObs: Action: FAIL-CLOSE (Abort startup)
        else Namespace Hợp Lệ
            CoreObs->>ModMetrics: Thực thi callback Register(meterProvider, namespace)
            alt Lỗi Đăng Ký Metrics (Ví dụ: Duplicate Metric)
                ModMetrics-->>CoreObs: Trả về error
                CoreObs->>Policy: EvaluateRegistrationError(module, error)
                alt Module Trọng Yếu (Tier-0 e.g., HTTP, Core)
                    Policy-->>CoreObs: Action: FAIL-CLOSE (Crash process)
                else Module Phụ Trợ (Tier-1)
                    Policy-->>CoreObs: Action: FAIL-OPEN (Degrade/Log & Suppress)
                end
            end
        end
    end

    CoreObs-->>App: Trả về MeterProvider / Error
```

### Chính sách xử lý lỗi (Observability Policies Engine Decision Matrix)

| Scenario | Severity | Policy Action | Mô tả chi tiết |
| --- | --- | --- | --- |
| Central Namespace Rỗng hoặc Bị Lỗi Định Dạng Nặng | High | **FAIL-CLOSE** | Gây crash/stop tiến trình bootstrap ứng dụng ngay lập tức. Không cho phép cluster HA chạy ở trạng thái thiếu namespace hoặc sai namespace. |
| Trùng lặp / Lỗi khởi tạo metrics lớp giao tiếp (HTTP/Core) | High | **FAIL-CLOSE** | Lớp HTTP và Core DB/Redis metrics là Tier-0. Lỗi đăng ký metrics này sẽ block deployment để nhà vận hành xử lý cấu hình sai. |
| Lỗi đăng ký metrics ở các module phụ trợ hoặc non-critical (Tier-1) | Low | **FAIL-OPEN** | Ghi log cảnh báo mức độ WARN/ERROR, chặn lỗi không cho lan rộng (suppress), khởi tạo thực thể mock/dummy no-op để luồng chính tiếp tục hoạt động, duy trì tính HA của dịch vụ. |

### Chính sách quản lý Namespace (Namespace Delegation Policy)

- **Chuẩn hóa duy nhất:** Namespace chỉ được chuẩn hóa một lần duy nhất tại hàm `NormalizeNamespace` của tầng observability trung tâm.
- **Quyền quyết định:** Nếu namespace lỗi hoặc trống, chính sách xử lý sẽ được quyết định bởi policy engine của tầng trung tâm. Các module con nhận namespace đã chuẩn hóa và **không tự động thay đổi hay bổ sung giá trị mặc định**.

---

## 4. Governance & Integration Rules

1. **Rule of Two (Quy tắc hai metric):** Một module nghiệp vụ chỉ được phép đăng ký tối đa 1 Counter nghiệp vụ và 1 Histogram đo latency. Không tạo các metric đơn lẻ cho từng flow.
2. **Generic API calls:** Callsite tại Service Layer phải gọi qua các hàm Record generic, tự truyền giá trị label (flow, result, operation) thay vì gọi các hàm hard-code nghiệp vụ.
3. **No-op Staging:** Khi thực hiện refactor metrics, các hàm đo lường chưa chuyển đổi phải được dọn dẹp sạch sẽ (bỏ stub rỗng) hoặc chuyển đổi đồng loạt để tránh để lại dead code.
4. **Panic-safe Invariant:** Tất cả các collector/metric pointer tại module nghiệp vụ phải được kiểm tra `nil` trước khi gọi `.Add()` hoặc `.Record()`. Nếu OTel không được khởi động (ví dụ: trong unit test), luồng đo lường sẽ tự động bỏ qua (fail-open) thay vì gây panic ứng dụng.
