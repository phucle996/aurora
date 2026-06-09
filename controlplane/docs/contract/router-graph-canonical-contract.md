# Router Graph Canonical Contract

Status: Draft v1  
Owner: Platform/Controlplane team  
Scope: HTTP Engine setup, Middleware pipeline, and Route Registration delegation  

---

## 1. HTTP Engine & Route Registration Flow (Router Flow)

Sơ đồ dưới đây đặc tả luồng khởi tạo HTTP Engine (Gin), cấu hình bảo mật, thiết lập middlewares và bàn giao đăng ký route cho các Module:

```mermaid
flowchart TD
    %% Styling
    classDef initStyle fill:#1F1F35,stroke:#7C4DFF,stroke-width:2px,color:#FFFFFF,font-weight:bold;
    classDef stepStyle fill:#161625,stroke:#4CAF50,stroke-width:1px,color:#B2FF59;
    classDef failStyle fill:#8B0000,stroke:#FF3333,stroke-width:2px,color:#FFFFFF,font-weight:bold;

    R_Start["NewApplication app.go"]:::initStyle --> R_New["router := gin.New()"]:::stepStyle
    R_New --> R_NilCheck{"router == nil?"}:::stepStyle
    R_Crash["FAIL-CLOSE: app.Stop & Crash"]:::failStyle
    R_NilCheck -- "Yes" --> R_Crash
    
    R_NilCheck -- "No" --> R_Proxies{"Check Configured Proxies"}:::stepStyle
    
    %% Configured Proxies branching
    R_Proxies -->|nil / empty slice| R_Warn["Warn: Trust All Proxies <br/> (Potential Security Issue)"]:::stepStyle
    R_Proxies -->|Non-empty slice| R_Config["SetTrustedProxies(IPs)"]:::stepStyle
    
    R_Config --> R_ParseCheck{"Are IP/CIDR formats valid?"}:::stepStyle
    R_ParseCheck -- "No (Parsing Error)" --> R_Crash
    R_ParseCheck -- "Yes" --> R_InitMiddleware:::stepStyle
    
    R_Warn --> R_InitMiddleware["Initialize Middlewares & Cross-Module Wiring <br/> (initMiddlewares)"]
    
    R_InitMiddleware --> R_MidCheck{"err != nil?"}:::stepStyle
    R_MidCheck -- "Yes (FAIL-CLOSE)" --> R_Crash
    
    R_MidCheck -- "No" --> R_UseMiddleware["Apply Global Middlewares <br/> (engine.Use)"]:::stepStyle
    R_UseMiddleware --> R_Register["Global Route Orchestrator <br/> NewGlobalRoutes(engine, modules)"]:::stepStyle
    
    %% Global Router Delegation
    R_Register --> R_Health["Direct Health Route Map"]:::stepStyle
    R_Register --> R_Tier0["Critical Modules (Tier-0)"]:::stepStyle
    R_Register --> R_Tier1["Non-Critical Modules (Tier-1)"]:::stepStyle
    
    R_Health -->|Direct Route| R_HealthAct["health.handler.action"]:::stepStyle
    
    R_Tier0 -->|Delegates to| R_T0Router["Module Router: <br/> module.RegisterRoutes(router, m)"]:::stepStyle
    R_T0Router -->|Direct Route| R_T0Act["module.handler.action"]:::stepStyle
    
    R_Tier1 -->|Check IsEnabled| R_T1Check{"IsEnabled?"}:::stepStyle
    R_T1Check -- "Yes" --> R_T1Router["Module Router: <br/> module.RegisterRoutes(router, m)"]:::stepStyle
    R_T1Router -->|Direct Route| R_T1Act["module.handler.action"]:::stepStyle
    
    R_T1Check -- "No" --> R_T1Fallback["Fallback Route Group"]:::stepStyle
    R_T1Fallback -->|Direct Route| R_T1Degraded["apires.RespondServiceUnavailable (HTTP 503)"]:::stepStyle
    
    %% Invariant
    R_T0Act --> R_NoGuards["✓ Invariant: NO nil guards allowed in module/route.go"]:::initStyle
    R_T1Act --> R_NoGuards
```

---

## 2. HTTP Engine Bootstrap & Security Invariants

Hệ thống HTTP Router bắt buộc phải bảo đảm các nguyên tắc cấu hình an toàn sau:

### 2.1 Trusted Proxies Security

- **Không tin tưởng mặc định:** Nếu danh sách proxy trống, Gin Router sẽ phát log cảnh báo nghiêm trọng.
- **Fail-Close trên CIDR/IP sai định dạng:** Bất kỳ lỗi parse cấu hình proxy IP/CIDR nào đều kích hoạt crash tiến trình khởi chạy ngay lập tức (`FAIL-CLOSE`) để bảo đảm lớp CIDR guard không bị bypass.

### 2.2 Route Registration Contract

- **No Nil Guards inside Route files:** Các file đăng ký route của các module nghiệp vụ (e.g. `internal/iam/route.go`, `internal/core/route.go`) **không được chứa code kiểm tra nil** đối với Handler hay Service.
- **Đảm bảo của App:** Lớp `App` cam kết bằng hợp đồng thiết kế rằng khi hàm đăng ký route được gọi, mọi dependency truyền vào (Module, Handler, Service) đều đã được khởi tạo hoàn hảo và non-nil.

---

## 3. Tier-1 Route Fallback (Tuyến fallback khi dịch vụ tắt)

Đối với các module Tier-1 (Non-critical) có thể bị tắt (disabled) hoặc bị hạ cấp (degraded) do lỗi khởi tạo:

- Lớp định tuyến không được ẩn hay xóa route của client.
- Thay vào đó, nếu module bị tắt (`IsEnabled == false`), route của module đó sẽ được định tuyến sang nhóm Fallback Route Group.
- Nhóm fallback này tự động phản hồi mã trạng thái HTTP `503 Service Unavailable` thông qua `apires.RespondServiceUnavailable` để thông báo cho Client biết tính năng tạm thời bị gián đoạn nhưng không phá vỡ cấu trúc routing chung của API Gateway.
