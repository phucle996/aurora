# Tenant Bucket List — God View

Tenant Bucket List is a synchronous read workflow for querying all buckets within an
enterprise workspace. Controlplane executes 3-table join verification in PostgreSQL
(`tenant_workspaces`, `tenant_memberships`, `tenant_buckets`) to return a flat projection
of buckets strictly scoped to the authorized tenant workspace and zone.

---

## API-scope contract

Browser calls neutral `GET /api/v1/storage/buckets`. ACR validates the Trinity tenant membership,
resolves workspace and zone context, rewrites the path to `/api/v1/tenant/storage/buckets`,
and injects trusted identity headers (`x-user-id`, `x-workspace-id`, `x-zone-id`, `x-tenant-id`).
Controlplane requires `storage:bucket:read` permission.

---

## REST input and output

### Request headers used

| Header | Use |
|---|---|
| `Cookie` | ACR derives Trinity session, tenant membership, Zone, and workspace context. |
| `Origin` | CORS enforcement at Envoy / ACR. |

### Response payload

| Status | Payload | Reason |
|---|---|---|
| `200` | `{"status": "success", "data": [ { "id": "...", "name": "tn-...", "workspace_id": "...", "zone_id": "...", "tenant_id": "...", "status": "READY", "capacity_quota_bytes": 107374182400, "used_bytes": 0, "versioning_enabled": false, "lifecycle_rules": [], "created_at": "...", "updated_at": "..." } ], "message": "tenant buckets retrieved successfully"}` | List of tenant buckets for the workspace. |
| `401` | `{"status": "error", "code": "UNAUTHORIZED", "message": "unauthorized"}` | Missing or invalid Trinity session cookie. |
| `403` | `{"status": "error", "code": "FORBIDDEN", "message": "permission denied"}` | Missing `storage:bucket:read` permission grant or inactive tenant membership. |
| `500` | `{"status": "error", "code": "INTERNAL_ERROR", "message": "internal_error"}` | Database query failure. |

---

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    autonumber
    actor Browser as Cloud Console (Tenant)
    participant Envoy as Central Envoy Gateway
    participant ACR as ACR (ExtAuthz)
    participant Redis as Auth-State Redis
    participant CP as Controlplane

    Browser->>Envoy: GET /api/v1/storage/buckets
    Envoy->>ACR: CheckRequest
    ACR->>Redis: Validate Trinity tenant session & resolve workspace/zone
    ACR->>ACR: Strip untrusted headers & inject x-user-id, x-tenant-id, x-workspace-id, x-zone-id
    ACR->>ACR: Rewrite path to /api/v1/tenant/storage/buckets
    ACR-->>Envoy: Ok
    Envoy->>CP: Forward GET /api/v1/tenant/storage/buckets
```

---

## Phase 2 — Controlplane Read Query

```mermaid
sequenceDiagram
    autonumber
    participant Envoy as Central Envoy Gateway
    participant AuthMW as Authorize Middleware
    participant Handler as TenantBucketHandler
    participant Service as TenantBucketService
    participant Repo as TenantBucketRepository
    participant PG as PostgreSQL

    Envoy->>AuthMW: Request with trusted headers
    AuthMW->>AuthMW: Verify tenant:storage:bucket:read grant
    AuthMW->>Handler: Dispatch List(c)
    Handler->>Service: ListBucketsForTenant(workspaceID, tenantID, userID, zoneID)
    Service->>Repo: List(workspaceID, tenantID, userID, zoneID)
    Repo->>PG: SELECT JOIN tenant_workspaces AND tenant_memberships
    PG-->>Repo: Return bucket rows
    Repo-->>Service: Return []TenantBucket
    Service-->>Handler: Return []TenantBucket
    Handler-->>Envoy: 200 OK JSON
    Envoy-->>Browser: 200 OK JSON
```

### Hop-by-Hop Contract — Phase 2

#### Hop 2.1: ContextInjector & Authorize Middleware
- **Input**: Headers `x-user-id`, `x-tenant-id`, `x-workspace-id`, `x-zone-id`.
- **Processing**: Evaluates L1 Permission Registry for key `{tenant_id}:{workspace_id}:storage:bucket:read`.
- **Output**: Validated request context.

#### Hop 2.2: TenantBucketHandler → TenantBucketService
- **Input**: `workspaceID`, `tenantID`, `userID`, `zoneID` extracted from context.
- **Output**: Call to `repo.List(ctx, workspaceID, tenantID, userID, zoneID)`.

#### Hop 2.3: TenantBucketRepository → PostgreSQL Query
- **Input SQL Query**:
  ```sql
  SELECT b.id, b.name, b.workspace_id, b.zone_id, b.tenant_id, b.status,
         b.capacity_quota_bytes, b.used_bytes, b.used_bytes_observed_at,
         b.encrypt_enabled, b.versioning_enabled, b.object_locking_enabled,
         b.replication_enabled, b.retention_days, b.legal_hold_enabled,
         b.tags, b.lifecycle_rules, b.created_at, b.updated_at
  FROM storage.tenant_buckets b
  JOIN hierarchy.tenant_workspaces w ON b.workspace_id = w.id
  JOIN hierarchy.tenant_memberships m 
    ON m.tenant_id = w.tenant_id 
   AND m.user_id = $3 
   AND m.status = 'active'
  WHERE b.workspace_id = $1 
    AND b.tenant_id = $2 
    AND w.zone_id = $4
  ORDER BY b.created_at DESC;
  ```
- **Output**: Array of `TenantBucket` entities.

#### Hop 2.4: Controlplane → Browser
- **Output**: HTTP `200 OK` JSON array of bucket records.

---

## Failure and security rules

| Condition | Failure Semantics | Recovery / Settlement |
|---|---|---|
| Inactive tenant membership | HTTP `403 Forbidden` / Empty list | User membership status must be active. |
| Context zone mismatch | Empty list `[]` | User only sees buckets in the active workspace's zone. |

---

## Code map

- **God View SoT**: `god_view/storage/tenant_bucket_list_god_view_workflow.md`
- **Controlplane Route**: `controlplane/internal/storage/route.go`
- **Controlplane Handler**: `controlplane/internal/storage/transport/http/handler/tenant_bucket_handler.go` (`List`)
- **Controlplane Service**: `controlplane/internal/storage/service/tenant_bucket_service.go` (`ListBucketsForTenant`)
- **Controlplane Repository**: `controlplane/internal/storage/repository/tenant_bucket_repo.go` (`List`)
