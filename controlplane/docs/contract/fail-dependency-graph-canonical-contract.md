# Controlplane — C4 Architectural Contract

Status: Draft v1  
Owner: Platform/Controlplane team  
Scope: Full C4 architecture contract — System Context through Code-level dependency graph and fail-fast lifecycle rules  

---

## C4 Architectural Diagrams

> Organized per the **C4 Model** (L1 → L2 → L3 → L4).  
> - **Static diagrams** (`C4Context`, `C4Container`, `C4Component`) — describe *what exists and who uses it*.  
> - **Dynamic diagrams** (`sequenceDiagram`) — describe *how components interact at runtime during initialization*.

---

### [L1] System Context — Controlplane in the World

Who uses the platform, what external systems Controlplane depends on, and how it fits into the broader cloud-native architecture. Controlplane is treated as a single black box.

```mermaid
C4Context
    title System Context — Cloud-Native Platform

    %% ── Human Actors ────────────────────────────────────────
    Person(admin,   "Admin",                  "Manages zones, secrets, IAM keys via Admin Portal")
    Person(tenant,  "Cloud Tenant",           "Manages cloud resources via Cloud Console UI")
    Person(sre,     "SRE / Platform Engineer","Monitors health, operates runbooks, manages deployment")

    %% ── Controlplane (Black Box) ─────────────────────────────
    System(cp, "Controlplane Cluster", "HA cluster (3 nodes) — manages resource lifecycle, identity, policy enforcement and job distribution for all Dataplane zones")

    %% ── Client UIs ──────────────────────────────────────────
    System_Ext(adminui,    "Admin UI",       "Admin portal (React/Vite) — zone & IAM management")
    System_Ext(cloudui,    "Cloud UI",       "Tenant console (Next.js) — cloud resource self-service")
    System_Ext(runbook,    "Runbook Web",    "SRE runbook console — incident response automation")

    %% ── Ingress ─────────────────────────────────────────────
    System_Ext(envoy,      "Envoy Edge Gateway", "TLS termination, HTTP/gRPC load balancing (Least Request) across 3 Controlplane nodes")

    %% ── Dataplane Zones ──────────────────────────────────────
    System_Ext(dpz1,       "Dataplane Zone 1",  "Single Rust node — Asia Southeast region")
    System_Ext(dpz2,       "Dataplane Zone 2",  "HA Active-Active Rust cluster (2 nodes) — US West region")

    %% ── Persistence & Messaging ──────────────────────────────
    System_Ext(psql,       "PostgreSQL 17",      "Central persistent store — tenants, zones, audit, IAM")
    System_Ext(pgbouncer,  "PgBouncer",          "Connection pooler (transaction mode) — proxies to PostgreSQL")
    System_Ext(redis,      "Redis Core",         "Identity sessions, rate-limit counters, pub/sub cache")
    System_Ext(redisjob,   "Redis Job",          "Job queue broker (Redis Streams) — distributed task dispatch")

    %% ── Observability Stack ───────────────────────────────────
    System_Ext(obs,        "Observability Stack","OpenTelemetry Collector, Prometheus, Grafana, Loki, Tempo")

    %% ── Relationships ────────────────────────────────────────
    Rel(admin,   adminui,   "Uses",               "HTTPS / Browser")
    Rel(tenant,  cloudui,   "Uses",               "HTTPS / Browser")
    Rel(sre,     runbook,   "Operates via",       "HTTPS / Browser")
    Rel(sre,     obs,       "Monitors via",       "Grafana Dashboard")

    Rel(adminui, envoy,     "API calls",          "REST / HTTPS :443")
    Rel(cloudui, envoy,     "API calls",          "REST / HTTPS :443")
    Rel(runbook, envoy,     "Routes via",         "HTTP :80")
    Rel(envoy,   cp,        "Load balances to",   "HTTP + gRPC / Least Request")

    Rel(dpz1,    cp,        "Registers & heartbeats", "gRPC / mTLS")
    Rel(dpz2,    cp,        "Registers & heartbeats", "gRPC / mTLS")
    Rel(dpz1,    redisjob,  "Consumes jobs",          "Redis Streams")
    Rel(dpz2,    redisjob,  "Consumes jobs",          "Redis Streams")

    Rel(cp,      pgbouncer, "Reads / Writes",     "pgx / TCP :6432")
    Rel(pgbouncer, psql,    "Proxies to",         "scram-sha-256 / TCP :5432")
    Rel(cp,      redis,     "Sessions / Cache",   "resp3 / TCP")
    Rel(cp,      redisjob,  "Dispatches jobs",    "Redis Streams")
    Rel(cp,      obs,       "Emits telemetry",    "OTLP gRPC :4317")
```

---

### [L2] Container — Application Topology

Static view of the Controlplane Go process and its external dependencies.

```mermaid
C4Container
    title Controlplane — Container Topology

    Person(sre, "SRE / Platform Engineer", "Monitors health, manages deployment lifecycle")

    System_Boundary(cp, "Controlplane") {
        Container(app,  "NewApplication",    "Go Process",        "Entry point — wires module graph, HTTP & gRPC servers")
        Container(http, "HTTP Engine",       "gin.Engine",        "Serves REST API via NewGlobalRoutes")
        Container(grpc, "gRPC Server",       "google.golang.org/grpc", "Dataplane registration & heartbeat")
        Container(mg,   "Module Graph",      "NewGlobalModules",  "Bootstraps Tier-0 and Tier-1 modules with tier policy")
    }

    System_Ext(db,     "PostgreSQL",     "Persistent store — all module data")
    System_Ext(redis,  "Redis",          "Cache, rate-limit counters, pub/sub fanout bus")
    System_Ext(pe,     "PolicyEngine",   "Injected as Tier-0 — RBAC, rate-limit, AdminCIDR policy")

    Rel(sre,  app,  "Observes via",    "k8s probes /health/*")
    Rel(app,  mg,   "Bootstraps")
    Rel(app,  http, "Binds")
    Rel(app,  grpc, "Binds")
    Rel(mg,   db,   "Connects to")
    Rel(mg,   redis,"Connects to")
    Rel(mg,   pe,   "Injects (Tier-0)")
    Rel(http, mg,   "Routes to handlers via")
```

---

### [L3] Component — Module Graph & Route Orchestrator

Static view of the internal components inside the Module Graph and the Route Orchestrator.

```mermaid
C4Component
    title Module Graph & Route Orchestrator — Component View

    Container_Boundary(mg, "Module Graph: NewGlobalModules") {
        Component(core,  "Core Module",        "Tier-0 — FAIL-CLOSE",  "Runtime secret provider, security keys")
        Component(iam,   "IAM Module",          "Tier-0 — FAIL-CLOSE",  "Auth, tokens, admin API-key lifecycle")
        Component(pe,    "PolicyEngine",        "Tier-0 — FAIL-CLOSE",  "RBAC, rate-limit, AdminCIDR enforcement")
        Component(hyp,   "Hypervisor Module",   "Tier-1 — FAIL-OPEN",   "VM orchestration — degrades to HTTP 503")
        Component(mail,  "Mail Module",         "Tier-1 — FAIL-OPEN",   "Email delivery — degrades to HTTP 503")
    }

    Container_Boundary(gr, "Route Orchestrator: NewGlobalRoutes") {
        Component(hroute, "Health Routes",      "Direct — router.GET",  "Liveness, Readiness, Startup probes")
        Component(t0r,    "Tier-0 Routes",      "module.RegisterRoutes","Core, IAM — guaranteed non-nil handlers")
        Component(t1r,    "Tier-1 Routes",      "RegisterRoutes / 503 Fallback", "Active routes or apires.503 fallback")
    }

    Rel(core,  t0r,    "Wires handler")
    Rel(iam,   t0r,    "Wires handler")
    Rel(hyp,   t1r,    "Wires handler (if IsEnabled)")
    Rel(mail,  t1r,    "Wires handler (if IsEnabled)")
    Rel(pe,    t0r,    "Enforces policy via middleware")
```

---

### [L3 Dynamic] Bootstrap Initialization Flow

How `NewApplication` calls `NewGlobalModules`, and how each `NewModule` returns `(module, err)` for the global graph to apply the tier policy.

```mermaid
sequenceDiagram
    autonumber
    participant App  as NewApplication
    participant GMG  as NewGlobalModules
    participant T0   as Tier-0 NewModule
    participant T1   as Tier-1 NewModule

    App  ->>  GMG : call NewGlobalModules(cfg, db, ...)

    %% ── Tier-0 ──────────────────────────────────────────────
    GMG  ->>  T0  : NewModule(cfg, db, ...)
    T0  -->>  GMG : (module, err)

    alt err != nil or module == nil
        GMG -->> App : return nil, err
        note over App : FAIL-CLOSE → app.Stop() → process crash
    end

    %% ── Tier-1 ──────────────────────────────────────────────
    GMG  ->>  T1  : NewModule(cfg, db, ...)
    T1  -->>  GMG : (module, err)

    alt err != nil
        note over GMG : FAIL-OPEN → Log Error & Suppress
        GMG  ->>  GMG : NewDegradedModule(err)  [Null Object Pattern]
    end

    GMG -->> App : return &Modules{...}, nil
    note over App : Module Graph Fully Wired ✓
```

---

### [L3 Dynamic] HTTP Engine & Route Orchestration Flow

How `NewApplication` initializes `gin.Engine`, wires middleware, and delegates to `NewGlobalRoutes`.

```mermaid
sequenceDiagram
    autonumber
    participant App  as NewApplication
    participant Gin  as gin.Engine
    participant MW   as initMiddlewares
    participant GR   as NewGlobalRoutes
    participant Mod  as module.RegisterRoutes

    App  ->>  Gin  : gin.New()
    Gin -->>  App  : engine (or nil)

    alt engine == nil
        note over App : FAIL-CLOSE → app.Stop() → crash
    end

    App  ->>  Gin  : SetTrustedProxies(proxies)

    alt proxies nil / empty
        note over Gin : WARN — Trust All Proxies (security risk)
    else IP/CIDR format invalid
        Gin -->>  App  : err (parse error)
        note over App : FAIL-CLOSE → app.Stop() → crash
    end

    App  ->>  MW   : initMiddlewares(cfg, db, coreModule, ...)
    MW  -->>  App  : err

    alt err != nil
        note over App : FAIL-CLOSE → app.Stop() → crash
    end

    App  ->>  Gin  : engine.Use(globalMiddlewares...)
    App  ->>  GR   : NewGlobalRoutes(engine, modules)

    %% ── Tier-0 routes ───────────────────────────────────────
    GR   ->>  Mod  : [Tier-0] module.RegisterRoutes(router, m)
    note right of Mod : Direct Route → module.handler.action

    %% ── Tier-1 routes ───────────────────────────────────────
    alt module.IsEnabled()
        GR   ->>  Mod  : [Tier-1] module.RegisterRoutes(router, m)
        note right of Mod : Direct Route → module.handler.action
    else degraded
        GR   ->>  GR   : router.Group("/api/v1/...").Any("/*any", 503)
        note right of GR : Fallback → apires.RespondServiceUnavailable
    end

    note over GR : ✓ Invariant — NO nil guards inside module/route.go
```

---

### [L4 Code] Constructor Dependency Chain

Code-level view of how each `NewModule` chains `(repo → cache → service → handler)` with fail-fast guards before returning to the Module Graph.

```mermaid
sequenceDiagram
    autonumber
    participant GMG  as NewGlobalModules
    participant NM   as NewModule
    participant Repo as NewRepository
    participant Cac  as NewCache / FanoutBus
    participant Svc  as NewService
    participant Hdl  as NewHandler

    GMG  ->>  NM   : NewModule(cfg, db, ...)

    NM   ->>  Repo : NewRepository(db)
    Repo -->> NM   : repo

    alt repo == nil
        NM  -->> GMG : return nil, err
    end

    NM   ->>  Cac  : NewCache(rds) / NewFanoutBus(...)
    Cac  -->> NM   : cache

    alt cache == nil
        NM  -->> GMG : return nil, err
    end

    NM   ->>  Svc  : NewService(repo, cache)
    note over Svc  : fail-fast guard — panic if repo or cache nil
    Svc  -->> NM   : service

    NM   ->>  Hdl  : NewHandler(service)
    note over Hdl  : fail-fast guard — panic if service nil
    Hdl  -->> NM   : handler

    NM  -->>  GMG  : return &Module{Handler: handler}, nil
    note over GMG  : Tier policy applied → Inject Active or Degrade
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
