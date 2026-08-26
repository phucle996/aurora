# Tenant Storage Credential Delete — God View

> **Critical-route revision (2026-08-26):** credential revocation uses the public `/api/v1/critical/storage/...` route. ACR consumes the exact session proof before the sole `/api/v1/tenant/critical/storage/...` rewrite, and Controlplane runs `RequireSessionProof` before `Authorize`. Older non-critical route text below is superseded.

Tenant Credential Deletion is an asynchronous credential revocation mutation. Controlplane
deletes the credential record in PostgreSQL and writes a sealed Zone outbox command in an atomic
CTE transaction. Physical service account revocation on MinIO occurs asynchronously via Dataplane
at the edge zone.

---

## API-scope contract

Browser calls neutral `DELETE /api/v1/storage/buckets/{id}/credentials/{credential_id}`.
ACR validates the Trinity tenant membership, resolves workspace and zone context, rewrites
the path to `/api/v1/tenant/storage/buckets/{id}/credentials/{credential_id}`, and injects
trusted identity headers (`x-user-id`, `x-workspace-id`, `x-zone-id`, `x-tenant-id`).
Controlplane requires `storage:credential:delete` permission.

---

## REST input and output

### Request headers used

| Header | Use |
|---|---|
| `Cookie` | ACR derives Trinity session, tenant membership, Zone, and workspace context. |
| `Origin` | CORS enforcement at Envoy / ACR. |
| `X-Requested-With` or `Sec-Fetch-Site` | Required by ACR CSRF check for `DELETE`. |
| `traceparent` | Injected into outbox record for distributed OpenTelemetry tracing. |

### Response payload

| Status | Payload | Reason |
|---|---|---|
| `200` | `{"status": "success", "data": null, "message": "tenant credential deletion initiated"}` | Credential deleted from PostgreSQL; outbox command queued. |
| `401` | `{"status": "error", "code": "UNAUTHORIZED", "message": "unauthorized"}` | Missing or invalid Trinity session cookie. |
| `403` | `{"status": "error", "code": "FORBIDDEN", "message": "permission denied"}` | Missing `storage:credential:delete` permission grant or inactive tenant membership. |
| `404` | `{"status": "error", "code": "NOT_FOUND", "message": "credential not found"}` | Credential does not exist or bucket is not owned by the tenant workspace. |
| `500` | `{"status": "error", "code": "INTERNAL_ERROR", "message": "internal_error"}` | Database error, payload sealing failure, or outbox insert error. |

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

    Browser->>Envoy: DELETE /api/v1/storage/buckets/{id}/credentials/{credential_id}
    Envoy->>ACR: CheckRequest
    ACR->>Redis: Validate Trinity tenant session & resolve workspace/zone
    ACR->>ACR: Strip untrusted headers & inject x-user-id, x-tenant-id, x-workspace-id, x-zone-id
    ACR->>ACR: Rewrite path to /api/v1/tenant/storage/buckets/{id}/credentials/{credential_id}
    ACR-->>Envoy: Ok
    Envoy->>CP: Forward DELETE /api/v1/tenant/storage/buckets/{id}/credentials/{credential_id}
```

---

## Phase 2 — Controlplane Desired-State Transaction

```mermaid
sequenceDiagram
    autonumber
    participant Envoy as Central Envoy Gateway
    participant AuthMW as Authorize Middleware
    participant Handler as TenantCredentialHandler
    participant Service as TenantBucketService
    participant Repo as TenantCredentialRepository
    participant Protector as Payload Protector
    participant PG as PostgreSQL

    Envoy->>AuthMW: Request with trusted headers
    AuthMW->>AuthMW: Verify tenant:storage:credential:delete grant
    AuthMW->>Handler: Dispatch Delete(c)
    Handler->>Service: DeleteCredentialForTenant(bucketID, credentialID, workspaceID, tenantID, userID, zoneID)
    Service->>Repo: GetByID(credentialID, bucketID, workspaceID, tenantID, userID, zoneID)
    Repo->>PG: Query credential access key
    Service->>Service: Build CredentialSync (delete action)
    Service->>Protector: Seal payload bytes
    Service->>Repo: Delete(credentialID, bucketID, workspaceID, tenantID, userID, zoneID, outboxRecord)
    Repo->>PG: Execute atomic CTE (DELETE tenant_credentials + INSERT storage_outbox_records)
    PG-->>Repo: Commit successful
    Repo-->>Service: Success
    Service-->>Handler: Success
    Handler-->>Envoy: 200 OK JSON
    Envoy-->>Browser: 200 OK JSON
```

### Hop-by-Hop Contract — Phase 2

#### Hop 2.1: ContextInjector & Authorize Middleware
- **Input**: Headers `x-user-id`, `x-tenant-id`, `x-workspace-id`, `x-zone-id`.
- **Processing**: Evaluates L1 Permission Registry for key `{tenant_id}:{workspace_id}:storage:credential:delete`.
- **Output**: Validated request context.

#### Hop 2.2: TenantCredentialHandler → TenantBucketService
- **Input**: `bucketID`, `credentialID`, `workspaceID`, `tenantID`, `userID`, `zoneID`.
- **Processing**:
  1. Queries credential to retrieve `access_key`.
  2. Builds Protobuf `CredentialSync` with `action: "delete"`.
  3. Seals payload bytes via Protector with topic `storage.credential.delete`.
- **Output**: Call to `repo.Delete(ctx, credentialID, bucketID, workspaceID, tenantID, userID, zoneID, outboxRecord)`.

#### Hop 2.3: TenantCredentialRepository → PostgreSQL Atomic CTE
- **Input SQL Query**:
  ```sql
  WITH authorized_credential AS (
      SELECT c.id, c.access_key, b.name AS bucket_name, b.zone_id, b.tenant_id
      FROM storage.tenant_credentials c
      JOIN storage.tenant_buckets b ON c.bucket_id = b.id
      JOIN hierarchy.tenant_workspaces w ON b.workspace_id = w.id
      JOIN hierarchy.tenant_memberships m 
        ON m.tenant_id = w.tenant_id 
       AND m.user_id = $5 
       AND m.status = 'active'
      WHERE c.id = $1 
        AND c.bucket_id = $2 
        AND b.workspace_id = $3 
        AND b.tenant_id = $4 
        AND w.zone_id = $6
      FOR UPDATE OF c
  ),
  deleted_credential AS (
      DELETE FROM storage.tenant_credentials
      WHERE id IN (SELECT id FROM authorized_credential)
      RETURNING id
  ),
  inserted_outbox AS (
      INSERT INTO storage.storage_outbox_records (
          event_id, zone_id, job_topic, payload, owner_id, owner_type, status,
          job_version, resource_id, resource_name, payload_schema_version, trace_id, idle,
          actor_user_id, payload_key_id
      )
      SELECT $7, ac.zone_id, 'storage.credential.delete', $8, ac.tenant_id, 'TENANT', 'PENDING',
             1, ac.id::text, ac.bucket_name, 1, $9, 30,
             $5, $10
      FROM authorized_credential ac
  )
  SELECT id FROM deleted_credential;
  ```
- **Output**: Atomic deletion of `tenant_credentials` row and pending outbox insert.

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
    participant DP as Zone Dataplane (CredentialDeleteExecutor)
    participant MinIO as MinIO Admin API

    PG-->>JO: Read committed outbox record (topic: storage.credential.delete)
    JO->>KafkaCmd: Publish JobCommandV1 (sealed CredentialSync, target zone)
    KafkaCmd-->>DP: Consume job command
    DP->>DP: Decrypt payload & decode CredentialSync
    DP->>MinIO: MinIO Admin DeleteServiceAccount (Access Key)
    MinIO-->>DP: Service Account deleted
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

    DP->>KafkaRes: Publish JobResult (job_id, status: SUCCEEDED)
    KafkaRes-->>JO: Consume JobResult
    JO->>PG: Settle storage_outbox_records (status = 'SUCCEEDED')
    JO->>Timeline: Publish Event: TENANT_CREDENTIAL_DELETED { tenant_id, bucket_id, credential_id }
    Timeline->>Centrifugo: Publish to channel "tenant:storage:{tenant_id}:{workspace_id}"
    Centrifugo-->>Browser: WebSocket Push: { type: "CREDENTIAL_DELETED", id }
```

---

## Code map

- **God View SoT**: `god_view/storage/tenant_storage_credential_delete_god_view_workflow.md`
- **Controlplane Route**: `controlplane/internal/storage/route.go`
- **Controlplane Handler**: `controlplane/internal/storage/transport/http/handler/tenant_credential_handler.go` (`Delete`)
- **Controlplane Service**: `controlplane/internal/storage/service/tenant_bucket_service.go` (`DeleteCredentialForTenant`)
- **Controlplane Repository**: `controlplane/internal/storage/repository/tenant_credential_repo.go` (`Delete`)
- **Dataplane Executor**: `dataplane/src/executor/storage/credential.rs`
