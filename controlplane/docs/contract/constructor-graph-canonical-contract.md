# Constructor Graph Canonical Contract

Status: Draft v1  
Owner: Platform/Controlplane team  
Scope: Internal Module Component instantiation, validation order, and Graceful Degradation logic  

---

## 1. Layered Component Constructor Flow (Sơ đồ khởi tạo phân lớp)

Việc khởi tạo các thành phần bên trong một Module nghiệp vụ được tổ chức tuần tự theo các lớp từ dưới lên trên. Bản thân constructor của các lớp con **không tự ý panic**, mà trả lỗi về cho hàm khởi tạo Module (`NewModule`) tổng hợp:

```mermaid
flowchart TD
    %% Styling
    classDef boundaryStyle fill:#1F1F35,stroke:#7C4DFF,stroke-width:2px,color:#FFFFFF,font-weight:bold;
    classDef stepStyle fill:#161625,stroke:#4CAF50,stroke-width:1px,color:#B2FF59;
    classDef errStyle fill:#8B0000,stroke:#FF3333,stroke-width:1px,color:#FFFFFF;

    Start["🚀 Call: NewModule()"]:::boundaryStyle --> Repo["1. Repository Layer <br/> (NewRepository)"]:::stepStyle
    
    %% Repo check
    Repo --> RepoCheck{"repo == nil?"}:::stepStyle
    RepoCheck -- "Yes" --> RetErr["Return error to Caller"]:::errStyle
    
    %% Cache check
    RepoCheck -- "No" --> Cache["2. Cache & Infra Layer <br/> (NewCache / Publisher)"]:::stepStyle
    Cache --> CacheCheck{"cache == nil?"}:::stepStyle
    CacheCheck -- "Yes" --> RetErr
    
    %% Service check
    CacheCheck -- "No" --> Service["3. Service Layer <br/> (NewService)"]:::stepStyle
    Service --> SvcCheck{"service == nil?"}:::stepStyle
    SvcCheck -- "Yes" --> RetErr
    
    %% Handler check
    SvcCheck -- "No" --> Handler["4. Handler Layer <br/> (NewHandler)"]:::stepStyle
    Handler --> HandlerCheck{"handler == nil?"}:::stepStyle
    HandlerCheck -- "Yes" --> RetErr
    
    %% Success
    HandlerCheck -- "No" --> RetSuccess["Return *Module, nil"]:::boundaryStyle
```

---

## 2. Caller-Side Decision Flow (Quy trình quyết định của Caller)

Hàm khởi tạo Module Graph tổng (`NewGlobalModules` tại `controlplane/internal/app/module.go`) tiếp nhận kết quả khởi tạo `(*Module, error)` từ mỗi module và đưa ra quyết định dựa trên cấp độ Module (Tier):

```mermaid
flowchart TD
    %% Styling
    classDef mainStyle fill:#1F1F35,stroke:#7C4DFF,stroke-width:2px,color:#FFFFFF,font-weight:bold;
    classDef decisionStyle fill:#2E1B4E,stroke:#BA68C8,stroke-width:2px,color:#E1BEE7;
    classDef closeStyle fill:#8B0000,stroke:#FF3333,stroke-width:2px,color:#FFFFFF,font-weight:bold;
    classDef openStyle fill:#161625,stroke:#4CAF50,stroke-width:1px,color:#B2FF59;

    Caller["App Module Graph: NewGlobalModules"]:::mainStyle --> CallMod["Call NewModule()"]:::mainStyle
    CallMod --> ErrCheck{"err != nil?"}:::decisionStyle

    %% Success path
    ErrCheck -- "No (nil)" --> Active["Register Active Module <br/> (enabled = true)"]:::openStyle

    %% Error path
    ErrCheck -- "Yes (error)" --> TierCheck{"Is Tier-0 Module?"}:::decisionStyle
    
    %% Tier-0 -> Fail Close
    TierCheck -- "Yes (Core, IAM, PolicyEngine)" --> FailClose["FAIL-CLOSE Policy: <br/> logger.SysFatal -> Stop Process"]:::closeStyle
    
    %% Tier-1 -> Fail Open / Degradation
    TierCheck -- "No (Hypervisor, Mail)" --> FailOpen["FAIL-OPEN Policy: <br/> Log warn/error & Suppress"]:::openStyle
    FailOpen --> Degrade["Instantiate Muted Module <br/> (NewDegradedModule)"]:::openStyle
    Degrade --> RegisterDegraded["Register Degraded Module <br/> (enabled = false, err = initError)"]:::openStyle
```

---

## 3. Constructor Dependency Invariants (Nguyên Tắc Khởi Tạo)

### 3.1 Trình tự và Trách nhiệm (Responsibility Delegation)

- **Constructors nghiệp vụ (Repo, Service, Handler):**
  - Phải kiểm tra đầu vào nghiêm ngặt. Nếu thiếu dependency bắt buộc, **trả về error có cấu trúc hoặc nil object** thay vì tự ý `panic` hoặc gọi `os.Exit`.
  - Mục đích: Giữ cho các module con độc lập về mặt logic xử lý lỗi và trao toàn quyền quyết định cho Caller.
- **Hàm NewModule:**
  - Tổng hợp lỗi từ các lớp con. Nếu bất kỳ lớp con nào lỗi, trả về lỗi rõ ràng lên Module Graph.

### 3.2 Cơ chế Degraded Module (Muted/Fallback Mode)

Khi một module Tier-1 bị lỗi khởi tạo, caller sẽ bọc nó bằng `NewDegradedModule(err)`:

- Trường `enabled` được gán thành `false`.
- Trường `err` được gán bằng lỗi khởi tạo gốc để phục vụ ghi log/audit.
- Các handler/service của module này sẽ từ chối nhận request hoặc trả về HTTP `503 Service Unavailable` tại tầng định tuyến, giữ cho toàn bộ phần còn lại của Controlplane không bị crash.
