# Personal Storage Bucket Runtime Read — God View

This workflow reads soft MinIO bucket usage for one Personal bucket detail
screen. It never replaces PostgreSQL `used_bytes` as the durable list/detail
projection and never changes quota, billing or lifecycle state.

## API-scope contract

Browser calls same-origin `POST /api/v1/runtime/assertions` with
`resource_type=storage_bucket`, a bucket UUID, `panel=metrics` and
`from_seconds=1..300`. ACR accepts this registry entry only in a verified
platform session, resolves the selected Personal workspace and Zone, and asks
IAM for exact `storage:bucket:read`. ACR owns the endpoint and does not forward
it to Controlplane.

After admission the browser calls the verified Zone directly at
`GET /zone-public/v1/runtime/storage/bucket/{bucket_id}/metrics?from_seconds=N`
with the assertion headers and `credentials=omit`. The browser never sends
physical bucket name, owner, workspace, Zone or PromQL.

## Boundary matrix

| Boundary | Authority / state |
|---|---|
| ACR | Trinity Personal actor, selected workspace, Zone and IAM permission decision |
| Zone resource head | `storage.bucket.head.{bucket_id}` written after successful MinIO create and tombstoned after successful delete |
| Zone authorizer | Signed exact request, head equality and distributed replay fence |
| Zone runtime stream | Fixed `minio_bucket_usage_total_bytes{bucket="trusted-name"}` query |
| Zone telemetry | MinIO `/minio/v2/metrics/bucket` scraped by Zone OTel Collector into VictoriaMetrics |
| Durable UI fallback | Controlplane PostgreSQL `personal_buckets.used_bytes` returned by normal bucket detail/list APIs |

## Phase 1 — Client → Envoy → ACR

Browser sends `POST /api/v1/runtime/assertions`, `Content-Type: application/json`,
Origin, Trinity Cookie and same-origin CSRF signal with body:

```json
{"resource_type":"storage_bucket","resource_id":"<uuid>","panel":"metrics","from_seconds":60}
```

Envoy sends the exact method/path/headers/body in ExtAuthz `CheckRequest`. ACR
checks CORS, rate limits, session, CSRF, platform tenant sentinel, selected
workspace and verified Zone. It rejects unknown fields, non-UUID resource,
non-metrics panel, component IDs and out-of-range windows. It maps the request
to internal `storage/bucket` plus `storage:bucket:read` and asks IAM using the
Personal actor/workspace authority. Deny/timeout returns locally without a
signature. Allow returns a local no-store ticket JSON; no upstream request is
made.

## Phase 2 — Zone admission and trusted name mapping

Zone Public Edge strips all caller scope headers and passes only the opaque
assertion headers to its authorizer. The authorizer verifies signature, TTL,
audience, exact method/path/query and local Zone, then reads
`storage.bucket.head.{bucket_id}`. The head must be schema 1, enabled,
non-tombstoned, `owner_type=PERSONAL`, `owner_id=actor_id`, and match assertion
workspace/Zone/resource. Its `resource_name` must be a bounded `ws-` name.

At this read boundary, `AURORA_ZONE_CONFIG/storage.bucket.head.{bucket_id}` is
JSON with exactly the authority fields the authorizer consumes:
`schema_version`, `runtime_read_enabled`, `module`, `resource_type`,
`resource_id`, `resource_name`, positive `version`, `tombstoned`, `owner_id`,
`owner_type`, `workspace_id` and `zone_id`. `event_id` remains writer/recovery
metadata and is not authorization input.

After a successful replay CAS the authorizer overwrites scope headers and adds
`x-aurora-resource-name` from the head. Envoy rewrites only to
`/runtime/stream`; the physical name is never returned to the browser as
authority.

The replay write is a separate ephemeral schema:
`AURORA_ZONE_RUNTIME_REPLAY/{assertion_jti}` stores the single byte `1`, uses
CAS create and expires with the KV bucket's 30-second retention. It contains no
bucket, owner or workspace fields.

## Phase 3 — MinIO telemetry ingestion

Zone OTel Collector scrapes MinIO
`/minio/v2/metrics/bucket` every 15 seconds on the private `zone-infra`
network. Relabel rules keep only reviewed bucket usage/object gauges and
`ws-`/`tn-` bucket labels. The collector remote-writes to Zone
VictoriaMetrics. It does not ingest object paths, credentials or audit logs.

This telemetry is soft observation. The hourly Zone Control scan, Kafka
snapshot, JO projection and PostgreSQL `used_bytes` remain the durable usage
and billing-adjacent workflow.

## Phase 4 — fixed query and Console projection

The Storage adapter accepts only `storage/bucket`, metrics panel, no component
and a trusted physical name. It generates exactly
`minio_bucket_usage_total_bytes{bucket="..."}` and wraps the bounded Victoria
response in runtime snapshot/metric SSE frames. Console starts this workflow
only when the durable bucket detail reports `status=READY`, converts the newest
byte sample to a six-decimal binary-MB string and updates only that bucket's
query cache. On any assertion, stream or Victoria failure, the last durable
Controlplane value remains visible. Reconnect always mints a new ticket and
uses jittered exponential delay from 1 second up to 30 seconds; a valid fresh
sample resets the retry attempt.

The bucket directory does not open one SSE per row. It reads the durable
PostgreSQL projection and refreshes explicitly; a future workspace aggregate
stream is a separate workflow.

## Registration lifecycle and failure rules

| Condition | Result |
|---|---|
| Bucket is not `READY` | Console does not mint assertions or open an SSE; durable detail remains visible |
| MinIO create succeeds but required Zone KV CAS fails | Dataplane returns retryable; idempotent create replays until runtime head and admission indexes are durable |
| Existing conflicting/tombstoned head on create | Terminal conflict; never overwrite another scope |
| Delete succeeds | Head becomes disabled tombstone before successful job result |
| Delete head CAS fails | Delete job retries; MinIO `NoSuchBucket` is idempotent |
| Bucket predates registration schema | Runtime admission fails closed until an explicit trusted lifecycle re-projection; prefix inference is forbidden |
| OTel/Victoria unavailable | Runtime SSE degrades only; PostgreSQL and billing workflow continue |

## Rollout order

Deploy Zone KV support, schema-2 Dataplane create, Zone authorizer/runtime
stream and OTel scrape before the Controlplane producer; enable Console last.
The schema-2 create command makes runtime head and initial commercial admission
required before lifecycle success. Existing pre-registration buckets remain
fail-closed and require a separate trusted lifecycle re-projection.

## Code map

- `cloud-console/src/features/storage/{api,realtime}.ts`
- `cloud-console/src/app/(console)/storage/[id]/page.tsx`
- `acr/src/runtime_read.rs`
- `proto/storage/bucket_lifecycle/v1/bucket_lifecycle.proto`
- `dataplane/src/executor/storage/{bucket,delete,runtime_registration}.rs`
- `zone-public-edge-gateway/authorizer/src/runtime_read.rs`
- `zone-runtime-stream/src/storage.rs`
- `dev/zone/otel/otel-collector.yml`
