# Fail Dependency Graph Canonical Contract

Status: Draft v1  
Owner: Platform/Controlplane team  
Scope: Dependency Graph and Lifecycle Fail-Fast Rules for Controlplane Services  

---

## Fail-Fast Architectural Diagrams

### 1. Global & Local Module Dependency Flow (Module Graph)

Describes how the global application bootstrap sequence loads Tier-0 (critical) and Tier-1 (non-critical) modules down to their internal structures.

```mermaid
flowchart TD
    Global["Global Module Graph: NewGlobalModules"] --> T0_Call["1. Call Tier-0 NewModule()"]
    
    %% Tier-0 Local Initialization and Return
    T0_Call --> T0_Exec["Local NewModule() executes <br/> (Constructor Pattern)"]
    T0_Exec -->|Returns: module, err| T0_Check{"Global Check: <br/> err != nil or module == nil?"}
    
    %% Tier-0 Decision
    T0_Check -- "Yes" --> T0_Fail["FAIL-CLOSE Policy: <br/> Propagate error -> app.Stop() -> Crash"]
    T0_Check -- "No" --> T1_Call["2. Call Tier-1 NewModule()"]
    
    %% Tier-1 Local Initialization and Return
    T1_Call --> T1_Exec["Local NewModule() executes <br/> (Constructor Pattern)"]
    T1_Exec -->|Returns: module, err| T1_Check{"Global Check: <br/> err != nil?"}
    
    %% Tier-1 Decision
    T1_Check -- "No" --> T1_Success["Inject Active Module to Graph"]
    T1_Check -- "Yes" --> T1_Degrade["FAIL-OPEN Policy: <br/> Log Error & Suppress (Do not return err)"]
    T1_Degrade --> T1_Dummy["Instantiate Degraded Dummy Module <br/> (Null Object Pattern)"]
    T1_Dummy --> T1_Inject["Inject Dummy Module to Graph"]
    
    T1_Success --> AppReady["Module Graph Fully Wired ✓"]
    T1_Inject --> AppReady
```

---

### 2. HTTP Engine & Route Registration Flow (Router Flow)

Illustrates the HTTP engine lifecycle from router initialization and proxy configuration to direct route handler registration.

```mermaid
flowchart TD
    R_Start["NewApplication app.go"] --> R_New["router := gin.New()"]
    R_New --> R_NilCheck{"router == nil?"}
    R_Crash["FAIL-CLOSE: app.Stop & Crash"]
    R_NilCheck -- "Yes" --> R_Crash
    
    R_NilCheck -- "No" --> R_Proxies{"Check Configured Proxies"}
    
    %% Configured Proxies branching
    R_Proxies -->|nil / empty slice| R_Warn["Warn: Trust All Proxies <br/> (Potential Security Issue)"]
    R_Proxies -->|Non-empty slice| R_Config["SetTrustedProxies(IPs)"]
    
    R_Config --> R_ParseCheck{"Are IP/CIDR formats valid?"}
    R_ParseCheck -- "No (Parsing Error)" --> R_Crash
    R_ParseCheck -- "Yes" --> R_InitMiddleware
    
    R_Warn --> R_InitMiddleware["Initialize Middlewares & Cross-Module Wiring <br/> (initMiddlewares)"]
    
    R_InitMiddleware --> R_MidCheck{"err != nil?"}
    R_MidCheck -- "Yes (FAIL-CLOSE)" --> R_Crash
    
    R_MidCheck -- "No" --> R_UseMiddleware["Apply Global Middlewares <br/> (engine.Use)"]
    R_UseMiddleware --> R_Register["Global Route Orchestrator <br/> NewGlobalRoutes(engine, modules)"]
    
    %% Global Router Delegation
    R_Register --> R_Health["Direct Health Route Map"]
    R_Register --> R_Tier0["Critical Modules (Tier-0)"]
    R_Register --> R_Tier1["Non-Critical Modules (Tier-1)"]
    
    R_Health -->|Direct Route| R_HealthAct["health.handler.action"]
    
    R_Tier0 -->|Delegates to| R_T0Router["Module Router: <br/> module.RegisterRoutes(router, m)"]
    R_T0Router -->|Direct Route| R_T0Act["module.handler.action"]
    
    R_Tier1 -->|Check IsEnabled| R_T1Check{"IsEnabled?"}
    R_T1Check -- "Yes" --> R_T1Router["Module Router: <br/> module.RegisterRoutes(router, m)"]
    R_T1Router -->|Direct Route| R_T1Act["module.handler.action"]
    
    R_T1Check -- "No" --> R_T1Fallback["Fallback Route Group"]
    R_T1Fallback -->|Direct Route| R_T1Degraded["apires.RespondServiceUnavailable (HTTP 503)"]
    
    %% Invariant
    R_T0Act --> R_NoGuards["✓ Invariant: NO nil guards allowed in module/route.go"]
    R_T1Act --> R_NoGuards
```

---

### 3. Component Constructor Flow (Constructor Pattern)

Details the generic internal module dependency tree construction, showing where dependencies (Repository, Cache, Service, Handler) are validated.

```mermaid
flowchart TD
    C_Start["NewModule"] --> C_Repo["NewRepository"]
    C_Repo --> C_RepoCheck{"repo == nil?"}
    C_RepoCheck -- "Yes" --> C_Err["Return Initialization Error"]
    
    C_RepoCheck -- "No" --> C_Cache["NewCache / FanoutBus / Infrastructure Client"]
    C_Cache --> C_CacheCheck{"cache == nil?"}
    C_CacheCheck -- "Yes" --> C_Err
    
    C_CacheCheck -- "No" --> C_Svc["NewService repo, cache"]
    C_Svc --> C_SvcCheck{"Is repo or cache nil?"}
    C_SvcCheck -- "Yes" --> C_Panic["PANIC: Constructor fail-fast check"]
    
    C_SvcCheck -- "No" --> C_Handler["NewHandler service"]
    C_Handler --> C_HandlerCheck{"Is service nil?"}
    C_HandlerCheck -- "Yes" --> C_Panic
    
    C_HandlerCheck -- "No" --> C_Ok["Module Ready: return module, nil ✓"]
    C_Err --> C_Propagate["Propagate error up to Module Graph"]
```

---

## 0) Contract Governance

### Contract Item

- `DEPGRAPH-GOV-001` Canonical ownership and validation policy.

### Owner

- Platform/Controlplane team, Module owners.

### Rules

- The Global Application Graph (`controlplane/internal/app/module.go`) is the single source-of-truth for cross-module dependency configuration.
- Any critical component failure during startup MUST result in an immediate fail-close behavior (blocking deployment/startup).
- No silent recovery or hidden default states are allowed for Tier-0 services.

### Invariants

- Fail-fast decisions MUST happen at the call-site / initialization phase, not at the execution/routing phase.
- Route handlers (`NewGlobalRoutes` / `module.RegisterRoutes`) are guaranteed by contract to receive valid, fully-initialized, non-nil modules and handler instances.

### Failure Semantics

- Mismatched failure strategy (e.g., Tier-0 failure treated as degraded/fail-open) will block continuous delivery pipeline.

### Verification Evidence

- Bootstrapping tests, compiler checks, and startup probe integrity tests.

---

## 1) Tier Classification Contract

### Contract Item

- `DEPGRAPH-TIER-001` Tier-0 vs Tier-1 Service Segmentation.

### Owner

- SRE Platform Engineering / Architecture.

### Rules

- **Tier-0 (Critical Services)**:
  - Modules: `Core`, `IAM`, `PolicyEngine`, and HTTP Router (`gin.Engine`).
  - Strategy: **Fail-Close**. Any initialization failure (e.g., database pool unreachable, Redis client nil, required cryptographic keys missing) must panic or return a terminal error, causing the container to crash immediately. This allows Kubernetes `StartupProbes` or orchestrators to restart/halt the pod and notify SRE.
- **Tier-1 (Non-Critical Services)**:
  - Modules: `Hypervisor`, `Mail`.
  - Strategy: **Fail-Open (Graceful Degradation)**. Network connection failures or infrastructure configuration mismatches must be caught at the initialization boundary. The system must log a system-level error and inject a degraded dummy instance (Null Object Pattern) so that the control plane continues to boot and operate with limited functionality.

### Invariants

- Standard health endpoints (`/api/v1/health/readiness`, `/api/v1/health/liveness`) must report partial degradation for Tier-1 but remain unready/unhealthy for Tier-0 issues.

---

## 2) Call-Site Fail-Fast Initialization

### Contract Item

- `DEPGRAPH-INIT-001` Fail-Fast at Construction and Bootstrap.

### Owner

- Component and Module Developers.

### Rules

- **HTTP Router**: Checked for nil directly at creation inside `NewApplication` (`app.go`).
- **Core Module & IAM Module**: Must fail the global bootstrap function `NewGlobalModules` (`app/module.go`) if initialization fails or returns nil.
- **Sub-dependencies & Handlers**: `ZoneHandler` and `ZoneService` must be checked for nil during `NewModule` creation in the core module (`core/module.go`) and return an error if nil.

### Invariants

- No nil-check or silent returns inside route registration definitions (`core/route.go`). Late-stage silent checks mask misconfigurations and violate observability standards.

---

## 3) Change Log Policy

- Any transition of a service from Tier-1 (degraded) to Tier-0 (critical) requires updating this contract.
- Removing a constructor fail-fast check without moving it upstream is a contract violation.
