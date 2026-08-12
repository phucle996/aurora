# Personal Tenant Catalog — God View

This workflow lists only active tenant memberships of the verified Personal
principal so the Context Center can offer valid tenant transitions. It is a
Personal owner workflow; it cannot be called as a Tenant catalog.

## API-scope contract

| Item | Contract |
|---|---|
| Browser method/path | `GET /api/v1/tenants` |
| ACR rewrite | verified Personal → `/api/v1/personal/tenants`; concrete Tenant → local `403` |
| Trusted headers to upstream | ACR removes client authority headers and injects verified `x-user-id` |
| Payload | none |
| Response | `{ tenants: [{ id, code, name, primary_domain }] }` |
| Authority | Controlplane hierarchy PostgreSQL membership, tenant status and primary domain |

### Phase 1 headers

| Header | Use |
|---|---|
| `cookie: access_token/access_key/access_secret` | ACR verifies the Personal Trinity session |
| Envoy `:method`, `:path` | Exact neutral route and rewrite decision |
| Upstream `x-user-id` | ACR-overwritten verified principal only |

### Phase 1 payload

| Payload | Contract |
|---|---|
| Query/body | Empty; tenant, workspace, role and level selectors are rejected/not used |

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Envoy
    participant A as ACR
    participant R as Auth-State Redis

    B->>E: GET /api/v1/tenants with Trinity cookies
    E->>A: ext_authz CheckRequest
    A->>R: Verify Personal session
    alt concrete Tenant session
        A-->>E: Local 403; no Controlplane forward
    else verified Personal
        A->>A: Rewrite path to /api/v1/personal/tenants
        A->>E: Forward exact GET and inject x-user-id
    end
```

## Phase 2 — Controlplane hierarchy read

```mermaid
sequenceDiagram
    participant E as Envoy
    participant H as TenantHandler
    participant S as TenantService
    participant P as TenantRepoImpl
    participant DB as Hierarchy PostgreSQL

    E->>H: GET /api/v1/personal/tenants + verified x-user-id
    H->>H: Parse UUID; reject missing/invalid identity
    H->>S: ListTenantsForUser(user_id)
    S->>P: Query active memberships
    P->>DB: Join tenant_memberships, active tenants, primary tenant_domains
    DB-->>P: Sorted durable catalog rows
    P-->>S: TenantCatalogItem list
    S-->>H: Catalog
    H-->>E: 200 {tenants:[...]}
```

## Key contract

`tenant_memberships(user_id, tenant_id, status='active')`,
`tenants(status='active')`, and the primary `tenant_domains` row are the
durable checks. A client cannot supply `tenant_id`, workspace, role, level or
domain to widen this catalog.
