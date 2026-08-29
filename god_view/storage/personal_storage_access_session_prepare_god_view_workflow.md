# Personal Storage Access Session Prepare — God View

This workflow creates one short-lived Storage capability in the selected Zone.
It does not create a Central authorization projection or reusable credential.
The durable Central state is the existing Storage job outbox; the capability
authority is the Zone KV record written by Dataplane.

## API-scope contract

This is a platform-owned `/personal` workflow. The browser calls the neutral
public route `POST /api/v1/storage/buckets/{bucket_id}/access-sessions`. ACR may
rewrite it to internal
`POST /api/v1/personal/storage/buckets/{bucket_id}/access-sessions` only after a
Trinity session, platform context, Zone and workspace selection have been
verified. Controlplane runs permission `storage:bucket:read` at level `*`; the
Storage repository then rechecks the durable personal owner, workspace, Zone
and bucket facts in the same statement that inserts the command.

The `202` response means only that the command is durable. It never means the
Zone capability is ready. A client must use the separate authenticated status
workflow and wait for `ACTIVE` before calling Zone Control. Centrifugo is a
wake-up hint, not readiness authority.

## Input and output

| Part | Contract |
|---|---|
| Cookies | Trinity session, access key/secret, device, workspace selection. |
| Method/path | `POST /api/v1/storage/buckets/{bucket_id}/access-sessions`. |
| `Content-Type` | `application/json`. |
| `X-Aurora-Requested-With` | CSRF marker set by the Console HTTP client. |
| Body | `duration_seconds` in 60..3600, reviewed action list and optional `key_prefix` up to 256 bytes. |

ACR removes client identity/workspace headers and overwrites trusted
`x-user-id`, `x-user-name`, `x-user-level`, `x-zone-id`, `x-workspace-id` and
the internal `/personal` path. It does not inspect or authorize Storage
capability actions on this Controlplane route.

| Status | Meaning |
|---|---|
| `202` | Returns `access_session_id`, `bucket_id`, `zone_id`, UTC `expires_at` and `gateway_path`; status is still pending. |
| `400` | UUID, duration, action or prefix is invalid. |
| `404` | Durable personal bucket/workspace/Zone ownership facts do not match. |
| `503 STORAGE_COMMERCIAL_ADMISSION_UNAVAILABLE` | Central owner admission is absent, stale or suspended. |
| `500` | Payload protection or PostgreSQL command insertion failed. |

## Key and state contract

| State | Owner | Rule |
|---|---|---|
| `storage.storage_outbox_records/{access_session_id}` | Controlplane Storage | One encrypted `storage.access.prepare` command and the only Central readiness row. |
| Kafka Storage command | JO | At-least-once delivery, fenced by event id, job version, resource and Zone. |
| `AURORA_ZONE_ACCESS/{access_session_id}` | Dataplane / Zone | JSON capability record, File KV, history 1, CAS create; exact replay succeeds and conflicting content fails closed. |
| `storage.storage_outbox_records.status` | JO | `PENDING/PROCESSING/SUCCEEDED/FAILED`; terminal rows retain `completed_at` and follow the normal 30-day cleanup. |
| `stream:{job_notifications}` | JO → Notification Service | Realtime hint after durable settlement; no payload or capability policy is exposed. |

There is deliberately no `access_session_auth_projection` table and no
`storage_access:{id}` Auth-State Redis key. ACR's normal Trinity session lookup
is authentication state, not Storage capability state.

## Phase 1 — Client → Envoy → ACR

~~~mermaid
sequenceDiagram
    participant B as Browser
    participant E as Central Envoy
    participant A as ACR
    participant AS as Auth-State session
    participant CP as Controlplane

    B->>E: POST neutral access-sessions with cookies CSRF JSON
    E->>A: CheckRequest method path headers buffered JSON body
    A->>AS: Verify Trinity user/device session and Zone
    A->>A: Verify platform branch workspace cookie CORS rate limits CSRF
    A->>A: Remove client identity headers and rewrite to /personal
    A->>CP: Exact POST internal path JSON plus overwritten trusted headers
    alt authentication or context invalid
        A-->>E: Local 401 or 403; no upstream request
        E-->>B: Denial
    end
~~~

No Storage capability lookup is performed by ACR in this phase.

## Phase 2 — Controlplane authorization and durable command

~~~mermaid
sequenceDiagram
    participant H as Personal Storage handler
    participant M as permission middleware
    participant S as Access-session service
    participant AD as Commercial admission repository
    participant R as Access-session repository
    participant DB as PostgreSQL

    M->>M: Authorize storage:bucket:read level *
    H->>H: Parse bucket UUID and canonicalize actions prefix duration
    H->>S: Flat StorageAccessSession command with trusted actor workspace Zone
    S->>AD: Require current PERSONAL owner admission
    S->>R: Resolve physical bucket through durable owner workspace Zone facts
    S->>S: Build random binding fence and protobuf prepare payload
    R->>R: Seal payload with Zone topic resource job metadata
    R->>DB: CTE recheck target then INSERT one storage outbox row
    DB-->>R: One row or no durable target
    S-->>H: Command committed
    H-->>B: 202 pending handle and UTC expiry
~~~

The service returns after PostgreSQL commit. Redis availability cannot change
the HTTP outcome.

## Phase 3 — Outbox → Kafka → Dataplane → Zone KV

~~~mermaid
sequenceDiagram
    participant JO as Job Orchestrator
    participant K as Kafka command topic
    participant DP as Dataplane Storage executor
    participant KV as AURORA_ZONE_ACCESS
    participant KR as Kafka result topic

    JO->>K: Publish fenced storage.access.prepare job
    K->>DP: At-least-once delivery
    DP->>DP: Validate schema UUIDs Zone/resource fences actions prefix expiry policy
    DP->>KV: CAS create access_session_id with JSON capability
    alt exact replay
        KV-->>DP: Existing bytes match; idempotent success
    else conflicting same id
        KV-->>DP: Conflict; fail closed
    end
    DP->>KR: SUCCEEDED ACCESS_READY or bounded FAILED result
~~~

Dataplane is the only writer of the Zone capability record. The record binds
session, actor, resource, bucket, workspace, Zone, actions, prefix, expiry and
policy revision.

The Phase 3 write schema is exactly one JSON value at
`AURORA_ZONE_ACCESS/{access_session_id}`:

| Field | Meaning in this phase |
|---|---|
| `access_session_id` | UUID and KV key equality fence |
| `binding_hash` | 64-character SHA-256 binding fence |
| `actor_id`, `workspace_id`, `zone_id` | Verified Personal scope |
| `resource_id`, `bucket_name` | Immutable bucket UUID and durable physical name |
| `actions` | Non-empty subset of the six reviewed Storage actions |
| `key_prefix` | Object-key scope, at most 256 bytes |
| `expires_at_unix_seconds` | Future expiry, no more than 3660 seconds from Dataplane validation time |
| `policy_revision` | Positive capability revision |

This record contains no wallet decision, transfer-ticket secret or runtime
metric selector; those belong to different KV workflows.

Before emitting the terminal Kafka result, Dataplane CAS-creates
`AURORA_ZONE_JOB_COMPLETION/job.completion.{job_id}.{delivery_epoch}` as protobuf
`JobCompletionReceiptV1`. The only fields are `schema_version=2`,
`command_sha256`, `attempt`, `message`, `result_payload`,
`result_payload_schema_version`, `result_status` and optional `error_code`.
It proves execution replay only and contains no access capability authority;
same-command replay reuses it, while conflict or corruption fails closed.

## Phase 4 — Result settlement and realtime hint

~~~mermaid
sequenceDiagram
    participant KR as Kafka result topic
    participant JO as Job Orchestrator
    participant DB as PostgreSQL outbox
    participant NS as Notification Service
    participant C as Centrifugo
    participant B as Browser

    KR->>JO: Validated terminal result
    JO->>DB: Idempotent status update plus completed_at
    JO->>NS: Enqueue storage.access.prepare job notification
    NS->>NS: Persist activity and inbox projection
    NS->>C: Publish authenticated user notification
    C-->>B: SUCCESS or FAILED wake-up hint
    B->>B: Run separate durable status refetch
~~~

Notification failure never rolls back the terminal outbox row. Reconnect and
poll timeout both recover through the status API.

## Failure and recovery

| Failure | Durable result | Recovery |
|---|---|---|
| Crash before outbox commit | Nothing created | Client may retry POST. |
| Crash after commit | Pending command remains | JO retries claim/publish. |
| Duplicate Kafka command | Same event/version | Dataplane exact replay succeeds. |
| Conflicting Zone KV value | No overwrite | Dataplane returns failure; outbox becomes `FAILED`. |
| Result replay | Terminal row already matches | JO treats it as already applied and reconstructs the same notification intent. |
| Notification/Centrifugo outage | Terminal row remains authoritative | Client polling or reconnect refetches status. |
| Zone KV missing after `202` | Status remains non-active until result | Client does not call Zone Control. |

## Code map

- `controlplane/internal/storage/transport/http/handler/personal_bucket_handler.go`
- `controlplane/internal/storage/service/personal_storage_access_session_service.go`
- `controlplane/internal/storage/repository/personal_storage_access_session_repo.go`
- `controlplane/internal/storage/migrations/000002_storage_job_outbox.up.sql`
- `job-orchestrator/src/results/storage/access.rs`
- `job-orchestrator/src/results/apply.rs`
- `dataplane/src/executor/storage/access.rs`
- `dataplane/src/infra/zone_kv.rs`
- `cloud-console/src/features/storage/objects/access-session.ts`
