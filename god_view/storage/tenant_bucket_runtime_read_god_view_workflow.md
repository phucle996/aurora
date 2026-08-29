# Tenant Storage Bucket Runtime Read — God View

This workflow reads soft MinIO usage for one bucket in the currently selected
Tenant workspace. It has no Personal fallback and does not replace the durable
PostgreSQL usage projection.

## API-scope contract

Browser sends same-origin `POST /api/v1/runtime/assertions` with flat
`resource_type=storage_bucket`, bucket UUID, `panel=metrics` and a bounded
window. ACR requires a verified Tenant session/membership, selected workspace
and Zone, then asks IAM for exact `storage:bucket:read` in that Tenant range.
The route is ACR-local and is never forwarded to Controlplane.

On allow, browser calls the verified Zone directly at
`GET /zone-public/v1/runtime/storage/bucket/{bucket_id}/metrics?from_seconds=N`
with the three assertion headers and no credentials. Browser cannot choose
Tenant, owner, workspace, Zone, physical bucket name or Victoria query.

## Phase 1 — Client → Envoy → ACR

Envoy supplies ACR an ExtAuthz `CheckRequest` containing exact POST path,
Origin, Cookie, CSRF signal and bounded JSON body. ACR performs CORS, pre/post
auth rate limits, Trinity session verification, Tenant membership binding,
workspace/Zone resolution and request validation. `storage_bucket` maps only
to internal `storage/bucket`, metrics panel and `storage:bucket:read`.

IAM evaluates the Tenant role projection for exact Tenant/workspace permission;
Personal grants are not consulted. Deny, timeout or malformed reply produces a
local error. Allow produces an Ed25519 TTL-10s ticket bound to
`owner_type=TENANT`, `owner_id=tenant_id`, actor, workspace, Zone, resource and
full GET path. Envoy returns the no-store JSON and does not forward upstream.

## Phase 2 — Zone registration and admission

Successful Tenant bucket create carries bucket, Tenant owner, workspace and
Zone UUIDs inside the protected Storage command. After every MinIO create side
effect succeeds, Dataplane CAS-creates schema-v1
`storage.bucket.head.{bucket_id}` with trusted `tn-` physical name. Successful
delete disables and tombstones the same head before emitting a successful job
result. Registration failure keeps the lifecycle command retryable.

At this write/read boundary,
`AURORA_ZONE_CONFIG/storage.bucket.head.{bucket_id}` is JSON containing
`schema_version`, `runtime_read_enabled`, `module`, `resource_type`,
`resource_id`, `resource_name`, positive `version`, `event_id`, `tombstoned`,
`owner_id`, `owner_type`, `workspace_id` and `zone_id`. Admission uses every
field except `event_id`; Tenant requires `owner_type=TENANT`, the asserted
Tenant owner and a bounded `tn-` name.

Zone Public Edge strips caller scope and its authorizer verifies signature,
expiry, exact route/query, local Zone, resource head equality and distributed
`jti` replay CAS. The head must match Tenant owner, selected workspace, Zone
and resource exactly. Only then is its bounded `tn-` name injected as
`x-aurora-resource-name` and the request rewritten to `/runtime/stream`.

`AURORA_ZONE_RUNTIME_REPLAY/{assertion_jti}` is a separate CAS-created
single-byte `1` with 30-second KV retention. It carries no Tenant or bucket
authority.

## Phase 3 — telemetry and fixed read

Zone OTel Collector privately scrapes MinIO v2 bucket metrics every 15 seconds,
keeps reviewed usage/object gauges for Aurora bucket prefixes and remote-writes
them to Zone VictoriaMetrics. The Storage runtime adapter accepts only the
metrics panel with no component and generates the fixed selector
`minio_bucket_usage_total_bytes{bucket="trusted-name"}`.

SSE emits bounded snapshot and metric frames. Console opens this workflow only
when durable bucket status is `READY`, then converts the newest byte sample to
binary MB for the bucket detail cache. Stream loss never mutates Tenant state;
the normal Tenant bucket detail/list continues to return durable PostgreSQL
usage as the neutral `used_mb` field (and raw bytes for backend consumers).
Reconnect mints a new assertion and uses jittered exponential
delay from 1 second through a 30-second ceiling; a valid sample resets retry.

## Failure and isolation rules

| Condition | Result |
|---|---|
| Inactive/mismatched Tenant membership or IAM deny | ACR does not sign; no Personal downgrade |
| Bucket belongs to another Tenant/workspace/Zone | Head comparison denies before Victoria |
| Signature expired/replayed or route changed | Zone deny |
| Head missing, conflicting, disabled or tombstoned | Zone deny |
| Bucket detail is not `READY` | Console does not mint a ticket or open SSE |
| Pre-schema bucket | Fail closed until trusted lifecycle re-projection; physical-name inference is forbidden |
| MinIO metrics/OTel/Victoria unavailable | Runtime detail degrades; durable projection and billing remain independent |
| Tenant directory has many buckets | No per-row SSE; it uses Controlplane projection/refetch |

## Rollout order

Deploy Zone KV support, schema-2 Dataplane create, Zone authorizer/runtime
stream and OTel scrape before the Controlplane producer; enable Console last.
The schema-2 command requires both runtime head and initial commercial
admission before lifecycle success. Existing pre-registration buckets remain
fail-closed and require a separate trusted lifecycle re-projection.

## Code map

- `cloud-console/src/features/storage/{api,realtime}.ts`
- `acr/src/runtime_read.rs`
- `controlplane/internal/iam/service/tenant_runtime_read_authorization_service.go`
- `proto/storage/bucket_lifecycle/v1/bucket_lifecycle.proto`
- `dataplane/src/executor/storage/{bucket,delete,runtime_registration}.rs`
- `zone-public-edge-gateway/authorizer/src/runtime_read.rs`
- `zone-runtime-stream/src/storage.rs`
- `dev/zone/otel/otel-collector.yml`
