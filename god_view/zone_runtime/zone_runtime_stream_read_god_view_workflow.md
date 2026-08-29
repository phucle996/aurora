# Zone Runtime Stream Read — God View

This document owns the shared Zone read-plane chassis used by module-specific
runtime workflows. It does not grant business authority by itself. Every
enabled resource type has a separate Personal or Tenant God View that pins its
permission, registration producer and fixed Victoria query.

## Shared contract

| Boundary | Contract |
|---|---|
| Public issuer | ACR-local `POST /api/v1/runtime/assertions`; no Controlplane HTTP forward |
| Central authorization | IAM exact permission decision in the verified Personal or Tenant workspace |
| Ticket | Ed25519 schema v1, TTL 10 seconds, exact method plus full path hash, Zone audience and random `jti` |
| Zone admission | Signature, expiry, route binding, resource head and distributed replay CAS all fail closed |
| Public stream | `GET /zone-public/v1/runtime/{module}/{type}/{uuid}/{panel}[/{component}]?from_seconds=1..300` |
| Internal stream | Envoy rewrites an admitted request to `GET /runtime/stream?from_seconds=...` |
| Data sources | Zone VictoriaMetrics and VictoriaLogs only; the stream service has no MinIO, Kafka, Redis or PostgreSQL credential |
| Browser authority | Assertion headers only. Cookie, raw query, owner/workspace/Zone and every `x-aurora-*` scope header are removed |

## Phase 1 — Client → Envoy → ACR

Browser calls same-origin `POST /api/v1/runtime/assertions` with JSON
`resource_type`, UUID `resource_id`, `panel`, optional `component_id` and
`from_seconds`. Envoy includes method, exact path, Origin, Cookie, CSRF signal
and bounded body in `CheckRequest`. ACR performs CORS, rate limits, Trinity
session verification, CSRF, selected workspace and verified Zone resolution.
It maps the public resource type to one internal module/type/permission tuple
and sends a flat authorization request to IAM over Shared Redis.

On deny, timeout or malformed reply ACR returns a local error. On allow, ACR
signs an assertion and returns local JSON containing `assertion`, `signature`,
`key_id`, verified `zone_id`, verified `zone_code`, exact `method`, exact `path`
and `expires_at`. Envoy does not forward this route to Controlplane.

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Central Envoy
    participant A as ACR
    participant I as IAM authorization consumer
    B->>E: POST /api/v1/runtime/assertions cookie origin CSRF plus JSON
    E->>A: ExtAuthz CheckRequest with exact method path headers body
    A->>A: Verify session workspace Zone body and resource registry
    A->>I: Exact owner branch workspace and permission decision
    alt deny timeout or invalid context
        A-->>E: Local error; no signature and no upstream
    else allowed
        A->>A: Sign TTL-10s exact-path assertion
        A-->>E: Local 200 JSON; no upstream forward
        E-->>B: Runtime read ticket
    end
```

## Phase 2 — Browser → Zone Public Edge → Authorizer

Browser derives the Zone origin from verified `zone_code` and configured base
domain. It sends `GET` with `Accept: text/event-stream` and the three assertion
headers using `credentials=omit`. Zone Public Edge CORS allows those headers,
then Lua removes Cookie, Authorization, CSRF, device ID and every caller
`x-aurora-*` header except the three opaque assertion headers.

The authorizer verifies Ed25519 signature, issuer/audience, clock bounds,
method, full path hash, route segments, bounded query and local Zone. It reads
`{module}.{resource_type}.head.{resource_id}` from `AURORA_ZONE_CONFIG`; schema,
enabled flag, version, tombstone, owner type/id, workspace, Zone and resource
identity must equal the assertion. It then creates `jti` in
`AURORA_ZONE_RUNTIME_REPLAY`; `AlreadyExists`, timeout and KV failure deny.

Only after both checks does it overwrite trusted scope headers. Adapter-owned
fields such as Storage physical bucket name come only from the verified head.
Assertion headers are removed before upstream. Envoy rewrites the path to
`/runtime/stream`, preserves only bounded `from_seconds`, disables retries and
uses a 330-second idle limit.

## Phase 3 — Fixed Victoria query and bounded SSE

The Rust handler parses trusted UUID/token headers, rejects client `panel_id`
and `component_id` query fields, validates the module adapter and acquires
bounded connection/fan-out permits. A subscription key contains the complete
trusted scope and snapshot window.

Each module adapter owns a fixed PromQL or LogsQL template. Browser text never
enters a raw Victoria query. The first read emits `runtime.snapshot`; live
polls emit `runtime.metric`, `runtime.log` or `runtime.event`. Response bodies,
log line counts, stream lifetime, heartbeat and snapshot window are bounded.
Victoria failures become sanitized `stream.error`. Metric lag emits
`runtime.gap` and keeps the newest frame; log lag closes the stream because
silently dropping ordered logs would imply a false complete tail.

## Failure and recovery

| Failure | Result |
|---|---|
| ACR session/workspace/permission invalid | No ticket |
| Signature, route, expiry or replay invalid | Zone deny before registry/source read |
| Head missing, disabled, tombstoned or scope mismatch | Zone deny before Victoria |
| Registry/replay KV unavailable or corrupt | `503`, fail closed |
| Victoria unavailable or response too large | Sanitized SSE error; business state is unchanged |
| Stream expires/disconnects | Client mints a fresh assertion and reconnects; no cursor is durable |

## Code map

- `acr/src/runtime_read.rs`
- `controlplane/internal/iam/transport/pubsub/handler/runtime_read_authorization_redis.go`
- `zone-public-edge-gateway/authorizer/src/runtime_read.rs`
- `zone-public-edge-gateway/envoy.yaml`
- `zone-runtime-stream/src/{http,contract,source,stream}.rs`
- `k8s/zone-public-edge-gateway.yaml`
- `k8s/zone-runtime-stream.yaml`
