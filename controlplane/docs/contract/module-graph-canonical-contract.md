# Module Graph Canonical Contract

Status: Draft v1  
Owner: Platform/Controlplane team  
Scope: Global Module Graph loading, Tier-0 vs Tier-1 dependency segmentation  

---

## 1. Global & Local Module Dependency Flow (Module Graph)

Sơ đồ dưới đây đặc tả luồng tải và thiết lập dependencies của các Module nghiệp vụ trong chuỗi bootstrap toàn cục (`NewGlobalModules` tại `controlplane/internal/app/module.go`):

```mermaid
flowchart TD
    %% Styling
    classDef globalStyle fill:#1F1F35,stroke:#7C4DFF,stroke-width:2px,color:#FFFFFF,font-weight:bold;
    classDef stepStyle fill:#161625,stroke:#4CAF50,stroke-width:1px,color:#B2FF59;
    classDef failStyle fill:#8B0000,stroke:#FF3333,stroke-width:2px,color:#FFFFFF,font-weight:bold;

    Global["Global Module Graph: NewGlobalModules"]:::globalStyle --> T0_Call["1. Call Tier-0 NewModule()"]:::stepStyle
    
    %% Tier-0 Local Initialization and Return
    T0_Call --> T0_Exec["Local NewModule() executes <br/> (Constructor Pattern)"]:::stepStyle
    T0_Exec -->|Returns: module, err| T0_Check{"Global Check: <br/> err != nil or module == nil?"}:::stepStyle
    
    %% Tier-0 Decision
    T0_Check -- "Yes" --> T0_Fail["FAIL-CLOSE Policy: <br/> Propagate error -> app.Stop() -> Crash"]:::failStyle
    T0_Check -- "No" --> T1_Call["2. Call Tier-1 NewModule()"]:::stepStyle
    
    %% Tier-1 Local Initialization and Return
    T1_Call --> T1_Exec["Local NewModule() executes <br/> (Constructor Pattern)"]:::stepStyle
    T1_Exec -->|Returns: module, err| T1_Check{"Global Check: <br/> err != nil?"}:::stepStyle
    
    %% Tier-1 Decision
    T1_Check -- "No" --> T1_Success["Inject Active Module to Graph"]:::stepStyle
    T1_Check -- "Yes" --> T1_Degrade["FAIL-OPEN Policy: <br/> Log Error & Suppress (Do not return err)"]:::stepStyle
    T1_Degrade --> T1_Dummy["Instantiate Degraded Dummy Module <br/> (Null Object Pattern)"]:::stepStyle
    T1_Dummy --> T1_Inject["Inject Dummy Module to Graph"]:::stepStyle
    
    T1_Success --> AppReady["Module Graph Fully Wired ✓"]:::globalStyle
    T1_Inject --> AppReady
```

---

## 2. Tier Classification & Policies (Phân Nhóm Module)

Hệ thống Controlplane phân loại các Module nghiệp vụ thành 2 phân hạng để tối ưu hóa tính HA và độ tin cậy:

### 2.1 Tier-0 (Critical Services - Dịch Vụ Sống Còn)

- **Danh sách Modules:** `Core`, `IAM`, `PolicyEngine`.
- **Chiến lược xử lý lỗi (Fail Strategy):** **Fail-Close**. Bất kỳ lỗi khởi tạo nào (như connection pool bị lỗi, thiếu runtime master key, lỗi Redis client) bắt buộc phải ném lỗi hoặc panic để chấm dứt (crash) tiến trình khởi động ngay lập tức. Điều này giúp bộ điều phối (Kubernetes) chặn đứng việc deploy cấu hình sai và kích hoạt cảnh báo hạ tầng cho SRE.

### 2.2 Tier-1 (Non-Critical Services - Dịch Vụ Phụ Trợ)

- **Danh sách Modules:** `Hypervisor`, `Mail`.
- **Chiến lược xử lý lỗi (Fail Strategy):** **Fail-Open (Graceful Degradation)**. Nếu xảy ra lỗi khởi tạo (như config sai, network timeout tới server mail), hệ thống bắt buộc phải bắt lỗi, ghi log cảnh báo mức độ WARN/ERROR, khử lỗi (suppress error) và khởi tạo một đối tượng thay thế không hoạt động (Null Object Pattern / Dummy Module). Việc này cho phép Controlplane tiếp tục vận hành các chức năng cốt lõi khác mà không bị tê liệt toàn bộ.

---

## 3. Governance Invariants

- **Single Source of Truth:** File `controlplane/internal/app/module.go` là nguồn tin cậy duy nhất định nghĩa cấu trúc Module Graph và liên kết chéo giữa các Module.
- **Health Reporting:** Endpoint kiểm tra sức khỏe `/api/v1/health/readiness` phải báo `unhealthy` nếu bất kỳ Tier-0 Module nào lỗi, nhưng báo `partially degraded` (giảm hiệu năng) nếu Tier-1 Module lỗi.
- **Fail-Fast Enforcement:** Mọi quyết định fail-fast phải xảy ra ngay tại biên khởi chạy (Initialization Boundary), tuyệt đối không để lọt lỗi cấu hình hoặc dependency `nil` vào runtime request.
