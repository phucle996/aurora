# Tenant Bucket Quota Update — God View

> **Critical-route revision (2026-08-26):** ACR consumes the exact session proof for the public `/api/v1/critical/storage/...` mutation and rewrites only to the corresponding `/api/v1/tenant/critical/storage/...` target. Controlplane runs `RequireSessionProof` before `Authorize`; older non-critical route text below is superseded.

Tenant Bucket Quota update is asynchronous. Controlplane validates quota headroom,
keeps the last confirmed capacity unchanged, transitions `READY -> UPDATING`, and
writes the requested target only in a sealed Zone command. Dataplane applies MinIO;
JO promotes the typed actual result back to PostgreSQL.

---

## API-scope contract

Browser calls neutral `PATCH /api/v1/storage/buckets/{id}/quota`. ACR validates the Trinity
tenant membership, resolves workspace and zone context, rewrites the path to
`/api/v1/tenant/storage/buckets/{id}/quota`, and injects trusted identity headers (`x-user-id`,
`x-workspace-id`, `x-zone-id`, `x-tenant-id`). Controlplane requires `storage:bucket:write` permission.

---

## REST input and output

### Request headers used

| Header | Use |
|---|---|
| `Cookie` | ACR derives Trinity session, tenant membership, Zone, and workspace context. |
| `Origin` | CORS enforcement at Envoy / ACR. |
| `X-Requested-With` or `Sec-Fetch-Site` | Required by ACR CSRF check for `PATCH`. |
| `traceparent` | Injected into outbox record for distributed OpenTelemetry tracing. |

### JSON payload

```json
{
  "quota_bytes": 214748364800
}
```

| Field | Type | Contract |
|---|---|---|
| `quota_bytes` | `integer` | **Required**. New quota limit (`>= 1073741824` = 1 GiB). Must be >= current `used_bytes` + 1 GiB safety headroom. |

### Response payload

| Status | Payload | Reason |
|---|---|---|
| `200` | `{"status": "success", "data": null, "message": "bucket quota updated"}` | Transition and sealed command committed; physical quota is still asynchronous. |
| `400` | `{"status": "error", "code": "BAD_REQUEST", "message": "quota leaves less than one GiB free"}` | Quota below current used storage. |
| `401` | `{"status": "error", "code": "UNAUTHORIZED", "message": "unauthorized"}` | Missing or invalid Trinity session cookie. |
| `403` | `{"status": "error", "code": "FORBIDDEN", "message": "permission denied"}` | Missing `storage:bucket:write` permission grant or inactive tenant membership. |
| `404` | `{"status": "error", "code": "NOT_FOUND", "message": "bucket not found"}` | Bucket does not exist or is not owned by the tenant workspace. |
| `500` | `{"status": "error", "code": "INTERNAL_ERROR", "message": "internal_error"}` | Database error, payload sealing failure, or outbox insert error. |

---

## Key and transport contract

| Key / Transport | Store | Operation | Invariant |
|---|---|---|---|
| `storage.tenant_buckets.capacity_quota_bytes` | PostgreSQL | Read, then JO actual update | Latest Zone-confirmed hard quota. |
| `storage.storage_outbox_records` | PostgreSQL | Insert | Generic transport row; target quota is only in sealed `BucketResizeSync`. |
| `storage.bucket.resize` | Kafka (Zone Command) | At-least-once publish | Sealed binary payload dispatched to the workspace's target Zone. |
| `dataplane.job_result` | Kafka (Result) | Publish result | Dataplane returns terminal status (`SUCCEEDED` / `FAILED`). |

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

    Browser->>Envoy: PATCH /api/v1/storage/buckets/{id}/quota { quota_bytes }
    Envoy->>ACR: CheckRequest
    ACR->>Redis: Validate Trinity tenant session & resolve workspace/zone
    ACR->>ACR: Strip untrusted headers & inject x-user-id, x-tenant-id, x-workspace-id, x-zone-id
    ACR->>ACR: Rewrite path to /api/v1/tenant/storage/buckets/{id}/quota
    ACR-->>Envoy: Ok
    Envoy->>CP: Forward PATCH /api/v1/tenant/storage/buckets/{id}/quota
```

---

## Phase 2 — Controlplane Transition Transaction

```mermaid
sequenceDiagram
    autonumber
    participant Envoy as Central Envoy Gateway
    participant AuthMW as Authorize Middleware
    participant Handler as TenantBucketHandler
    participant Service as TenantBucketService
    participant Repo as TenantBucketRepository
    participant Protector as Payload Protector
    participant PG as PostgreSQL

    Envoy->>AuthMW: Request with trusted headers
    AuthMW->>AuthMW: Verify tenant:storage:bucket:write grant
    AuthMW->>Handler: Dispatch UpdateQuota(c)
    Handler->>Service: UpdateBucketQuota(param)
    Service->>Repo: GetByID(bucketID, workspaceID, tenantID, userID, zoneID)
    Repo->>PG: Query existing bucket metadata
    Service->>Service: Build BucketResizeSync protobuf
    Service->>Protector: Seal payload bytes
    Service->>Repo: UpdateQuota(param, outboxRecord)
    Repo->>PG: CTE status READY->UPDATING + INSERT sealed command
    PG-->>Repo: Commit successful
    Repo-->>Service: Success
    Service-->>Handler: Success
    Handler-->>Envoy: 200 OK JSON
    Envoy-->>Browser: 200 OK JSON
```

### Hop-by-Hop Contract — Phase 2

#### Hop 2.1: ContextInjector & Authorize Middleware
- **Input**: Headers `x-user-id`, `x-tenant-id`, `x-workspace-id`, `x-zone-id`.
- **Processing**: Evaluates L1 Permission Registry for key `{tenant_id}:{workspace_id}:storage:bucket:write`.
- **Output**: Validated request context.

#### Hop 2.2: TenantBucketHandler → TenantBucketService
- **Input**: `bucketID`, `quotaBytes`, `workspaceID`, `tenantID`, `userID`, `zoneID`.
- **Processing**:
  1. Validates headroom constraint (`quotaBytes > current_used_bytes + 1 GiB`).
  2. Builds Protobuf `BucketResizeSync`.
  3. Seals payload bytes via Protector with topic `storage.bucket.resize`.
- **Output**: Call to `repo.UpdateQuota(ctx, param, outboxRecord)`.

#### Hop 2.3: TenantBucketRepository → PostgreSQL Atomic CTE
- **Input SQL Query**:
  ```sql
  WITH authorized_target AS (
      SELECT b.id, b.name, b.zone_id, b.used_bytes, b.capacity_quota_bytes
      FROM storage.tenant_buckets b
      JOIN hierarchy.tenant_workspaces w ON b.workspace_id = w.id
      JOIN hierarchy.tenant_memberships m 
        ON m.tenant_id = w.tenant_id 
       AND m.user_id = $4 
       AND m.status = 'active'
      WHERE b.id = $1 
        AND b.workspace_id = $2 
        AND b.tenant_id = $3 
        AND w.zone_id = $5
      FOR UPDATE OF b
  ),
  updated_bucket AS (
      UPDATE storage.tenant_buckets
      SET status = 'UPDATING',
          updated_at = NOW()
      WHERE id IN (SELECT id FROM authorized_target WHERE $6 >= used_bytes + 1073741824)
      RETURNING id, name, zone_id, tenant_id
  ),
  inserted_outbox AS (
      INSERT INTO storage.storage_outbox_records (
          event_id, zone_id, job_topic, payload, owner_id, owner_type, status,
          job_version, resource_id, resource_name, payload_schema_version, trace_id, idle,
          actor_user_id, payload_key_id
      )
      SELECT $7, ub.zone_id, 'storage.bucket.resize', $8, ub.tenant_id, 'TENANT', 'PENDING',
             1, ub.id::text, ub.name, 1, $9, 30,
             $4, $10
      FROM updated_bucket ub
      JOIN authorized_target at ON ub.id = at.id
  )
  SELECT id FROM updated_bucket;
  ```
- **Output**: Atomic `READY -> UPDATING` plus pending outbox. `capacity_quota_bytes` remains the last confirmed value.

#### Hop 2.4: Controlplane → Browser
- **Output**: HTTP `200 OK` JSON.

---

## Phase 3 — Outbox CDC Dispatch & Dataplane Execution

```mermaid
sequenceDiagram
    autonumber
    participant PG as PostgreSQL (Outbox)
    participant JO as Job Orchestrator CDC
    participant KafkaCmd as Zone Command Kafka
    participant DP as Zone Dataplane (BucketResizeExecutor)
    participant MinIO as MinIO Admin API

    PG-->>JO: Read committed outbox record (topic: storage.bucket.resize)
    JO->>KafkaCmd: Publish JobCommandV1 (sealed BucketResizeSync, target zone)
    KafkaCmd-->>DP: Consume job command
    DP->>DP: Decrypt payload & decode BucketResizeSync
    DP->>MinIO: MinIO Admin SetBucketQuota (Hard Quota)
    MinIO-->>DP: Quota applied
```

---

## Phase 4 — Job Settlement, Timeline & Realtime Notification

```mermaid
sequenceDiagram
    autonumber
    participant DP as Zone Dataplane
    participant KafkaRes as Kafka Result Topic
    participant JO as Job Orchestrator (Result Worker)
    participant PG as PostgreSQL
    participant Timeline as Timeline / Notification Service
    participant Centrifugo as Centrifugo WebSocket
    actor Browser as Cloud Console (Tenant)

    DP->>KafkaRes: SUCCEEDED + BucketQuotaAppliedV1
    KafkaRes-->>JO: Consume JobResult
    JO->>PG: Write actual quota + READY, then settle outbox SUCCEEDED
    JO->>Timeline: Publish Event: TENANT_BUCKET_QUOTA_UPDATED { tenant_id, bucket_id, new_quota }
    Timeline->>Centrifugo: Publish to channel "tenant:storage:{tenant_id}:{workspace_id}"
    Centrifugo-->>Browser: WebSocket Push: { type: "BUCKET_QUOTA_UPDATED", id, capacity_quota_bytes, status: "READY" }
```

`FAILED` carries no success payload. JO leaves `capacity_quota_bytes` unchanged,
restores `UPDATING -> READY`, and only then settles the outbox as `FAILED`.

---

## Code map

- **God View SoT**: `god_view/storage/tenant_bucket_quota_update_god_view_workflow.md`
- **Controlplane Route**: `controlplane/internal/storage/route.go`
- **Controlplane Handler**: `controlplane/internal/storage/transport/http/handler/tenant_bucket_handler.go` (`UpdateQuota`)
- **Controlplane Service**: `controlplane/internal/storage/service/tenant_bucket_service.go` (`UpdateBucketQuota`)
- **Controlplane Repository**: `controlplane/internal/storage/repository/tenant_bucket_repo.go` (`UpdateQuota`)
- **Dataplane Executor**: `dataplane/src/executor/storage/resize.rs`
