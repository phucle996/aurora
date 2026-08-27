# Personal Storage Access Session Status — God View

This workflow lets the authenticated personal client reconcile one access
session's durable readiness. It never reads Zone KV and a notification never
substitutes for this read.

## API-scope contract

The browser calls neutral
`GET /api/v1/storage/buckets/{bucket_id}/access-sessions/{access_session_id}`.
After Trinity verification ACR rewrites it to the internal `/personal` route
and overwrites user, workspace and Zone headers. Controlplane requires
`storage:bucket:read` at level `*`. The repository rechecks the personal owner,
workspace, Zone, bucket, command owner, actor and exact event id before
returning status.

## Input and output

The request has no body. It carries the normal Trinity/device/workspace cookies
and `Accept: application/json`; browser identity headers are removed by ACR.

| Result | Contract |
|---|---|
| `PENDING` | Outbox is `PENDING` or `PROCESSING`; the client must not use the handle. |
| `ACTIVE` | Outbox is `SUCCEEDED`; Dataplane reported the Zone KV CAS success. |
| `FAILED` | Outbox is `FAILED`; optional bounded `error_code` is returned. |
| `404` | Any owner/workspace/Zone/bucket/session correlation fails. |

`completed_at`, when present, is RFC3339 UTC. No protected command payload,
binding hash, action policy or error message is returned.

## Phase 1 — Client → Envoy → ACR

~~~mermaid
sequenceDiagram
    participant B as Browser
    participant E as Central Envoy
    participant A as ACR
    participant CP as Controlplane

    B->>E: GET neutral nested status route with Trinity cookies
    E->>A: CheckRequest method path headers empty body
    A->>A: Verify session platform Zone workspace CORS rate limits
    A->>A: Remove client identity headers and rewrite /personal
    A->>CP: GET internal route plus overwritten trusted context
    alt invalid session or context
        A-->>E: Local 401 or 403; no upstream forward
    end
~~~

## Phase 2 — Permission and durable outbox read

~~~mermaid
sequenceDiagram
    participant M as permission middleware
    participant H as Personal Storage handler
    participant S as Status service
    participant R as Status repository
    participant DB as PostgreSQL

    M->>M: Authorize storage:bucket:read level *
    H->>H: Parse bucket and access-session UUIDs
    H->>S: Flat status query with trusted actor workspace Zone
    S->>R: Read exact personal status
    R->>DB: CTE durable bucket owner workspace Zone target
    R->>DB: Join exact storage.access.prepare outbox actor owner resource event
    DB-->>R: One row or none
    S->>S: Map pending states to PENDING succeeded to ACTIVE failed to FAILED
    H-->>B: 200 safe status fields or 404
~~~

## Phase 3 — Client wake-up and retry behavior

Centrifugo `storage.access.prepare` SUCCESS/FAILED wakes the current waiter and
causes an immediate GET. Without the event, bounded exponential polling calls
the same GET. A reconnect is also only a reason to refetch. Zone Control is
called only after `ACTIVE`.

## Failure and recovery

| Failure | Behavior |
|---|---|
| Notification lost | Polling reads the durable row. |
| PostgreSQL unavailable | GET fails; client reports degraded and does not use the session. |
| Terminal result replay | Same terminal row remains; response is stable. |
| Row removed by 30-day cleanup | `404`; the short-lived session is already unusable. |
| Context changes while waiting | request aborts and the cached handle is cleared. |

## Code map

- `controlplane/internal/storage/route.go`
- `controlplane/internal/storage/transport/http/handler/personal_bucket_handler.go`
- `controlplane/internal/storage/service/personal_storage_access_session_service.go`
- `controlplane/internal/storage/repository/personal_storage_access_session_repo.go`
- `cloud-console/src/features/storage/api.ts`
- `cloud-console/src/features/storage/objects/access-session.ts`
