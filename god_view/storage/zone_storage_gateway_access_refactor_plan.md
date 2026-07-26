# [DRAFT PLAN] Zone Storage Gateway Access Refactor

> Temporary implementation-tracking artifact. This is not the final God View contract.  
> Visual/UI rules remain in `cloud-console/DESIGN.md`. Workflow/topology becomes authoritative only after the corresponding God View and deployed contracts are updated.  
> Created: 2026-07-26

## Implementation status (2026-07-26)

- **Shipped:** Central access-session endpoint, protobuf-encoded Auth-State Redis projection,
  `storage.access.prepare`/`StorageAccessRecord` protobuf contracts and JO
  route allow-list,
  Dataplane `AURORA_ZONE_ACCESS` KV/CAS projection, and the Rust Zone
  ExtAuthz verifier skeleton with mTLS, public-key verification and replay
  fencing. The legacy Controlplane `RequestSts`, JO `storage.object.sts`
  lifecycle, Dataplane STS executor, STS protobufs and public-MinIO endpoint
  configuration have been removed.
- **Staged but not enabled:** Zone Envoy deployment/config and Central
  assertion signing key distribution. The existing Docker storage listener is
  not an authorization fallback now that the secret-bearing STS producer has
  been removed; production traffic must remain disabled until public keys,
  mTLS identities and the S3 signing adapter are provisioned and tested.
- **Still required:** Zone route activation/transfer-ticket endpoints, usage-
  event/byte evidence wiring, revoke/revision command, multi-Zone route discovery and the full
  performance/failover gates below.
- **Console migration status:** Cloud Console no longer calls `/sts-token`,
  decodes credential-bearing notifications or persists presigned URLs. It
  creates an opaque access session, routes list/head/tag/bulk calls through the
  Gateway contract and presents an explicit disabled state for upload/download
  until transfer-ticket routes are deployed. Object browsing is still not
  release-ready until the Gateway activation gates are complete.

## 1. Objective

Replace the current browser-to-S3 STS/notification path with a Zone-local Storage Gateway path that:

- keeps Trinity authentication and ownership authorization in Central;
- does not expose S3 access key, secret key or session token to Cloud Console for list/metadata/tag/bulk operations;
- does not place storage credentials in `job.notification`, Centrifugo, notification history, logs or Redis;
- lets the Zone Gateway call the S3/MinIO endpoint in its own Zone;
- verifies a Central authorization assertion and a Zone-local access record before executing a request;
- meters resource/actor/byte usage without calling Controlplane on the storage hot path;
- remains safe under duplicate jobs, out-of-order delivery, replay, Zone failover and rolling deployment;
- keeps upload/download on short-lived presigned URLs where that is the correct data-plane optimization.

## 2. Non-goals

- Do not make Zone Storage Gateway a second ownership Source of Truth.
- Do not forward Trinity cookies or Central Redis credentials into a Zone.
- Do not use NATS KV as a per-request business database or as a replacement for Controlplane ownership.
- Do not use a bearer assertion delivered through notification as a disguised STS credential.
- Do not put S3 admin credentials in Envoy configuration, access logs or ExtAuthz responses.
- Do not make Cost Manager or Controlplane synchronous dependencies of every S3 list/head/tag request.
- Do not reintroduce STS credentials or notification-secret delivery as a
  rollback mechanism; rollback occurs at the deployment/route level.

## 3. Verified AS-IS

### 3.1 Current storage ingress

`controlplane/dev/envoy/envoy-storage.yaml` currently defines a standalone `storage-envoy` that:

- proxies directly to the local `minio` service in the Central Docker Compose environment;
- expects client SigV4/STS credentials;
- uses Lua to extract an access-key identifier for metering;
- writes `envoy-storage-access` JSON access logs containing the extracted access key;
- does not perform Aurora resource authorization itself.

This is a development topology and must not be copied as a Zone production topology without an explicit Zone route, mTLS boundary and private S3 endpoint.

### 3.2 Removed STS flow and current UI gap

The backend no longer exposes `RequestSts`, publishes or accepts
`storage.object.sts`, invokes MinIO STS, or serializes credentials through job
results. `ObjectStsRequest`/`ObjectStsResponse` are removed from all storage
protobuf copies. Cloud Console now requests an opaque access-session handle
and routes list/head/tag/bulk through the Zone Gateway. Upload/download remain
explicitly disabled until the short-lived transfer-ticket routes are deployed;
there is no compatibility STS path.

### 3.3 Current Zone KV contract

The Dataplane currently provisions four Zone JetStream KV buckets:

- `AURORA_ZONE_CONFIG` — desired/projected Zone configuration;
- `AURORA_ZONE_HEALTH` — rebuildable current health;
- `AURORA_ZONE_COORDINATION` — leases/fencing and coordination state.
- `AURORA_ZONE_ACCESS` — short-lived access readiness/revocation records with
  CAS, bounded retention and no credentials.

The access bucket is intentionally separate from config, health and
coordination. It remains a Zone-local execution projection and must not become
an ownership database.

### 3.4 Current metering contract

The existing storage billing God View resolves usage through access-key identity and a Central ownership projection. The new path must preserve the immutable `resource_id`/owner resolution boundary while replacing client access-key evidence with trusted gateway metadata and provider-side byte evidence.

## 4. Target topology

```text
Browser
  | Trinity cookie + opaque access_session_id
  v
Central Envoy
  | ext_authz
  v
ACR / Central Storage Access Authorizer
  | signed internal assertion, mTLS-only
  v
Zone Storage Envoy
  | local ExtAuthz gRPC Check
  v
zone-storage-authz (Rust)
  | read-only/restricted access to AURORA_ZONE_ACCESS
  v
Zone Storage Envoy
  | upstream S3 signing or trusted Rust storage adapter
  v
S3/MinIO in the same Zone

Zone Storage Gateway -------------------------------> Kafka storage usage topic
                                                       |
                                                       v
                                                Cost inbox / Cost Engine
```

The browser never connects to Zone NATS, S3 admin endpoints, Zone Storage Authz or the Dataplane job result channel.

## 5. Trust and ownership model

### 5.1 Controlplane is the ownership authority

At access-session creation, Controlplane derives the actor from the verified request context and checks:

```text
actor/session
bucket_id
workspace_id or tenant_id
zone_id
requested actions
resource/policy revision
```

The client must not be able to supply an alternate `owner_id`, `actor_id`, `workspace_id` or `zone_id` as an authorization input.

### 5.2 Central Access Record

Controlplane creates a short-lived Central Access Record in the Auth-State Redis authz-projection namespace. This is not a business ownership table.

```text
key: storage_access:{access_session_id}
ttl: expires_at_unix_seconds - now
value: protobuf storage.StorageAccessRecord (schema_version = 1)

StorageAccessRecord {
  access_session_id: uuid
  binding_hash: sha256
  actor_id: uuid
  resource_id: bucket-uuid
  bucket_name: physical-bucket-name
  workspace_id: uuid
  zone_id: uuid
  actions: [list, head, tag_read, tag_write, bulk_delete]
  key_prefix: optional/object/prefix
  expires_at_unix_seconds: unix timestamp
  policy_revision: 42
}
```

The Redis value is binary protobuf rather than JSON. Controlplane marshals the
record with the shared storage contract and ACR decodes it with `prost`; the
domain entity remains free of serialization tags. ACR rejects an unknown
`schema_version` and fails closed on corrupt or missing bytes.

The record is bound to the existing Trinity session. A stolen `access_session_id` without the matching session is not usable.

The CP database transaction/outbox remains the durable source for the access intent. The Redis record is ephemeral authz state and must be reconstructible/reissued; a missing Redis record fails closed.

### 5.3 Zone Access Record

Dataplane creates a separate Zone record after consuming the Central command:

```text
AURORA_ZONE_ACCESS/access.{access_session_id}

{
  "access_session_id": "uuid",
  "resource_id": "bucket-uuid",
  "zone_id": "uuid",
  "allowed_actions": [...],
  "policy_revision": 42,
  "binding_hash": "sha256",
  "expires_at": "timestamp",
  "status": "ACTIVE"
}
```

The Zone record is an execution/readiness registry, not an ownership decision. It contains no S3 secret.

### 5.4 Per-request Central assertion

For each list/head/tag/bulk request, Central ACR/authorizer creates an internal assertion. It is never returned to browser JavaScript and never published as a notification.

```text
{
  "jti": "uuid",
  "access_session_id": "uuid",
  "actor_id": "uuid",
  "resource_id": "bucket-uuid",
  "zone_id": "uuid",
  "action": "list|head|tag_read|tag_write|bulk_delete",
  "method": "GET|HEAD|PUT|POST|DELETE",
  "path_hash": "sha256",
  "body_hash": "sha256|null",
  "policy_revision": 42,
  "issued_at": "timestamp",
  "expires_at": "timestamp + a few seconds",
  "audience": "zone-storage-gateway",
  "key_id": "central-signing-key-id",
  "signature": "..."
}
```

The Zone Gateway authorizes only if the Central assertion and Zone Access Record have matching session/resource/Zone/action/revision/binding values.

## 6. Request flow

### 6.1 Prepare access session

1. Console sends `POST /api/v1/storage/buckets/{id}/access-sessions` through Central Envoy.
2. ACR verifies Trinity and injects trusted identity/context.
3. Controlplane verifies bucket access using the authoritative repository.
4. Controlplane creates `access_session_id`, `binding_hash`, scope, expiry and policy revision.
5. Controlplane commits the storage access outbox intent and materializes the ephemeral Central Access Record.
6. JO publishes `storage.access.prepare` through the durable Central-to-Zone transport.
7. Dataplane validates the Zone binding and creates `AURORA_ZONE_ACCESS/access.{id}` idempotently by CAS.
8. Dataplane emits an internal `ACCESS_READY` result. It does not publish credential material to Notification Service.
9. Console receives only status/operation metadata if a realtime update is needed.

### 6.2 List, metadata, tag and bulk request

1. Browser sends the existing Trinity cookie and `access_session_id` to Central Envoy.
2. Central ExtAuthz checks the Central Access Record and the requested path/action.
3. ACR signs the per-request internal assertion.
4. Central Envoy strips client-provided internal headers and forwards the assertion over mTLS to the Zone Storage Envoy.
5. Zone Envoy calls `zone-storage-authz` using Envoy ExtAuthz gRPC.
6. Rust verifier validates signature, canonical method/path/body, expiry, audience and Zone identity.
7. Rust verifier checks the Zone Access Record (local watch cache first, KV read on miss) and fails closed on mismatch/stale/missing state.
8. Zone Envoy forwards the allowed request to S3/MinIO through a trusted upstream signing/adapter path.
9. Storage ingress emits trusted usage metadata after the response; only successful/chargeable operations enter the usage aggregate.

### 6.3 Upload/download

1. Browser asks the Zone Storage Gateway for a scoped upload/download ticket using the same Central assertion path.
2. Gateway verifies the assertion and returns a short-lived presigned URL.
3. Browser transfers bytes directly to the private Zone S3 endpoint through the approved public route.
4. Provider-side access logs/usage collector provide actual byte evidence. A browser callback is never billing evidence.

### 6.4 Revocation and expiry

- Central session logout stops new assertions immediately.
- Access records expire by TTL; no manual delete is required for normal expiry.
- Ownership/policy/Zone changes increment `policy_revision` and invalidate the Central/Zone binding.
- Explicit revoke marks Central state revoked and publishes a Zone revoke command where required.
- Gateway fails closed for missing, expired, revoked or revision-mismatched records.

## 7. Rust Zone ExtAuthz service

### 7.1 Service name and boundary

Create a new Rust service:

```text
zone-storage-authz/
├── Cargo.toml
└── src/
    ├── main.rs
    ├── app.rs
    ├── config.rs
    ├── check.rs
    ├── assertion.rs
    ├── canonical.rs
    ├── access_store.rs
    ├── keys.rs
    ├── metrics.rs
    └── error.rs
```

It implements `envoy.service.auth.v3.Authorization/Check` over mTLS gRPC. It is a verifier, not an ownership database, S3 admin client or credential issuer.

### 7.2 Rust service tasks

- [x] **ZSG-3001 — Bootstrap the Rust ExtAuthz service** *(mTLS gRPC service shipped; readiness wiring remains deployment-specific)*
  - Create the standalone Cargo package with pinned dependency versions and reproducible lockfile.
  - Implement graceful shutdown, readiness and liveness endpoints.
  - Fail fast when Zone ID, NATS Zone URL, key set or TLS identity is missing.

- [x] **ZSG-3002 — Envoy Check contract**
  - Decode the Envoy v3 `CheckRequest`.
  - Extract method, normalized path, authority, selected headers, request body metadata and dynamic metadata.
  - Reject unsupported routes/actions by default.
  - Return explicit denied status details without leaking assertion contents.

- [x] **ZSG-3003 — Assertion verification**
  - Verify Ed25519/signing key ID, audience, issuer, expiry, issued-at skew and signature.
  - Verify `zone_id`, `resource_id`, action and path/body hashes.
  - Reject duplicate/conflicting assertion headers and all client-supplied `x-aurora-*` headers that bypass the central Envoy.
  - Support overlapping public keys during rotation and reject unknown key IDs.

- [x] **ZSG-3004 — Canonical request hashing**
  - Define one byte-compatible canonical method/path/query/header/body representation.
  - Normalize URL encoding once; reject ambiguous double encoding and path traversal.
  - Cap ExtAuthz body buffering for bulk requests.
  - Route large uploads/downloads to presigned flow instead of buffering them in ExtAuthz.

- [x] **ZSG-3005 — Zone access registry** *(watch/cache/read path shipped; read-only runtime ACL still to be applied)*
  - Add a dedicated `AURORA_ZONE_ACCESS` KV bucket; never reuse `AURORA_ZONE_COORDINATION`.
  - Implement bounded local watch cache with generation/expiry checks.
  - Read the KV record on cache miss and fail closed if NATS is unavailable.
  - Give the service read-only access by default; add narrowly scoped consume/revoke writes only if a destructive operation requires atomic one-time use.

- [x] **ZSG-3006 — Access matching**
  - Compare Central assertion and Zone record over `access_session_id`, resource, Zone, action, policy revision, expiry and `binding_hash`.
  - Enforce prefix restrictions and bucket/object path binding.
  - Do not resolve owner from the request or from a Zone database.

- [x] **ZSG-3007 — ExtAuthz failure semantics**
  - Invalid/malformed assertion: deny as unauthorized/forbidden.
  - Scope mismatch or policy revision mismatch: deny as forbidden.
  - Record not active yet: return a bounded not-ready response; never fall through to S3.
  - NATS/key-store unavailable: return 503 and fail closed.
  - Authz service overload: bounded queue, 429/503, no unbounded request buffering.

- [x] **ZSG-3008 — Observability** *(bounded counters/log redaction; exporter dashboards remain)*
  - Emit counters for allow/deny reasons, signature failures, record misses, KV latency, cache hit ratio and stale state.
  - Hash or redact actor/session identifiers in logs.
  - Never log assertion payload, cookies, S3 headers, presigned URLs or object content.
  - Propagate trace context without trusting client-supplied identity fields.

- [ ] **ZSG-3009 — Rust tests**
  - Valid assertion and valid Zone record.
  - Wrong user/session, bucket, Zone, action, prefix, revision, body hash and audience.
  - Expiry, clock skew, key rotation and unknown key ID.
  - Duplicate/replay behavior and CAS consume behavior where enabled.
  - KV outage, cache stale, malformed path and oversized body.

## 8. Phase and task plan

### Phase 0 — Contract, threat model and sizing

Goal: freeze the cross-boundary contract before implementation.

- [ ] **ZSG-0001 — Source-of-truth map**
  - Trace Controlplane ownership check, storage outbox, JO result path, DP executor, current storage Envoy and billing usage pipeline.
  - Mark the current STS notification payload as a security violation to remove.

- [ ] **ZSG-0002 — IDOR/replay threat model**
  - Enumerate forged bucket path, forged workspace/Zone, stolen access session ID, replayed internal assertion, stale policy revision and direct MinIO bypass.
  - Define deny behavior for every threat.

- [ ] **ZSG-0003 — Contract freeze**
  - Add byte-compatible protobuf definitions for `StorageAccessPrepare`, `StorageAccessReady`, `StorageAccessRevoke` and `StorageUsageEvent`.
  - Include stable event/operation/session IDs, schema version, resource ID, Zone, policy revision and trace context.
  - Keep secret/credential fields absent from all notification and access contracts.

- [ ] **ZSG-0004 — Assertion key strategy**
  - Decide Central signing issuer, key ID format, public-key distribution to Zones and rotation overlap.
  - Public keys may be distributed through versioned Zone configuration; private keys remain Central/Vault-owned.
  - The Zone verifier must never receive Central signing private material.

- [ ] **ZSG-0005 — NATS KV capacity benchmark**
  - Benchmark 10k/50k/100k active records with small values, expiry churn and replica factors 1/3/5.
  - Measure PUT/CAS, watch recovery, random GET, disk growth, compaction, failover and gateway p99 latency.
  - Set `history=1`, file storage, max value size, max age and max bytes from measurements rather than assumption.
  - Establish an active-record ceiling and admission/backpressure behavior.

- [ ] **ZSG-0006 — Upstream S3 authorization decision**
  - Validate whether Envoy AWS Request Signing works with the exact Envoy image and MinIO version.
  - Test payload hashing, unsigned payload mode, large-body behavior, retries and header canonicalization.
  - Envoy documents the AWS signing extension as requiring trusted downstream/upstream and notes buffering/unsigned-payload trade-offs; use it only behind the planned mTLS and ExtAuthz boundary. See the official [AWS Request Signing filter documentation](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/aws_request_signing_filter.html) and [v3 API reference](https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/filters/http/aws_request_signing/v3/aws_request_signing.proto).
  - If the spike fails, create a bounded Rust S3 adapter for non-presigned operations; do not weaken authz to keep direct proxying.

- [ ] **ZSG-0007 — God View draft**
  - Write the final workflow diagram and update `god_view/billing/storage_usage_billing_god_view.md` design notes before code contract merge.
  - Record that this plan is temporary until the God View is updated and reviewed.

**Phase gate:** contracts, threat model, upstream signing choice, NATS sizing and key distribution are approved.

### Phase 1 — Central access preparation

Goal: turn the existing STS initiation into a non-secret access-session intent.

- [x] **ZSG-1001 — Controlplane access-session endpoint**
  - Add `POST /api/v1/storage/buckets/{id}/access-sessions`.
  - Derive actor/workspace/tenant/Zone from trusted context.
  - Reuse the existing repository ownership check; never accept owner fields from body.
  - Bound requested actions and duration; default deny unsupported actions.
  - Return only `access_session_id`, expiry and readiness metadata.

- [x] **ZSG-1002 — Central Access Record** *(TTL projection shipped; idempotent prepare/revoke follow-up remains)*
  - Materialize the short-lived authz record in Auth-State Redis with session binding and TTL.
  - Use a dedicated namespace/ACL; do not use Shared L2 as an ownership SoT.
  - Make repeated prepare requests idempotent by `(session, resource, scope, policy_revision)`.
  - Revoke/expire records on logout, context change and policy revision change.

- [x] **ZSG-1003 — Storage access outbox**
  - Use `storage.access.prepare` as the sole storage access command; the
    secret-bearing `storage.object.sts` producer is removed.
  - Keep business transaction/outbox atomic in Controlplane PostgreSQL.
  - Route only to the exact Zone and resource binding.
  - Ensure WAL/LSN and Kafka ACK semantics remain after durable transport publish.

- [x] **ZSG-1004 — Central ACR ExtAuthz branch** *(signing/config branch shipped; Envoy Zone route is staged)*
  - Recognize the storage gateway route and access-session handle.
  - Verify Trinity and Central Access Record.
  - Sign the per-request internal assertion.
  - Set response headers only through Envoy’s trusted `OkHttpHeaders`/dynamic metadata path; strip client spoofed copies.
  - Never return the assertion to the browser body or notification stream.

- [ ] **ZSG-1005 — Central tests**
  - Owner vs non-owner, tenant membership, wrong workspace/Zone, revoked session and expired record.
  - Access-session ID from another Trinity session.
  - Replay with changed path/action/body.
  - Concurrent prepare requests and stale policy revision.

**Phase gate:** Central can issue an internal assertion only for an authenticated, authorized access session; no S3 secret exists in the new flow.

### Phase 2 — Dataplane Zone access record

Goal: make the Zone execution side idempotent and HA-safe.

- [x] **ZSG-2001 — Zone KV contract** *(replica/history/retention validation shipped)*
  - Add `AURORA_ZONE_ACCESS` to the Zone KV bootstrap with file storage, replica 3/5, history 1, bounded value size and measured max age/bytes.
  - Keep it separate from config, health and coordination buckets.
  - Add startup validation and fail-fast on wrong storage/replica/history/retention.

- [x] **ZSG-2002 — Shared protobuf copies** *(ready/revoke schema and CI breaking-change gate remain)*
  - Add the access prepare/ready/revoke schema to Controlplane, JO and Dataplane using byte-compatible generated output.
  - Add a breaking-change check in CI.

- [x] **ZSG-2003 — DP access prepare executor**
  - Add a clearly named executor for `storage.access.prepare`.
  - Validate resource ID, Zone binding, action scope, policy revision and expiry before KV write.
  - Do not call MinIO STS and do not create browser credentials.

- [x] **ZSG-2004 — Idempotent CAS record write** *(conflict reject shipped; revoke/newer revision path remains)*
  - Use `access_session_id` as idempotency key.
  - CAS create/update only when binding and revision match.
  - Reject attempts to resurrect a revoked/newer revision with an older job.
  - Bound duplicate job retries and record churn.

- [ ] **ZSG-2005 — Ready/revoke result path**
  - Emit an internal durable `ACCESS_READY`/`ACCESS_REVOKED` result with metadata only.
  - Do not reuse the generic notification message field for access state.
  - Define how Central Auth-State Redis is refreshed or reissued after a DP result.

- [ ] **ZSG-2006 — Zone failover tests**
  - DP worker crash before/after KV CAS.
  - Duplicate delivery, old revision after new revision and NATS leader failover.
  - Restart/rebuild of the access KV bucket and bounded watch recovery.

**Phase gate:** a valid Central assertion cannot authorize until the matching Zone record is active; duplicates are harmless and old revisions cannot resurrect access.

### Phase 3 — Rust Zone ExtAuthz service

Goal: add the required Rust verifier as an independent, least-privileged Zone service.

- [ ] **ZSG-3001 through ZSG-3009**
  - Complete the Rust service tasks in Section 7.
  - Run it as a separate Zone deployment with its own mTLS identity and NATS ACL.
  - It may read only `AURORA_ZONE_ACCESS` and the public assertion-key set; it may not read Zone config/health/coordination or Central Redis.

- [ ] **ZSG-3010 — ExtAuthz performance budget**
  - Establish p50/p95/p99 latency for cache hit, cache miss and KV failure.
  - Size bounded cache, watch buffer, concurrent checks and request body limits.
  - Set Envoy ext_authz timeout below the S3 upstream timeout and fail closed.

- [ ] **ZSG-3011 — Security review**
  - Verify mTLS peer identity, header stripping, assertion rotation, replay window, clock skew, path canonicalization and log redaction.
  - Prove that Zone Gateway cannot be reached directly from the public network.

**Phase gate:** Rust ExtAuthz denies every forged/mismatched request in tests and remains within the agreed p99/CPU/memory budget under load.

### Phase 4 — Zone Envoy and S3 integration

Goal: replace the current Central Docker-only proxy with a Zone-local, authenticated gateway.

- [ ] **ZSG-4001 — Zone Envoy deployment**
  - Move the production storage listener into the Zone deployment.
  - Central Envoy routes only to the selected Zone listener over mTLS.
  - MinIO/S3 data ports are private; direct bypass is blocked by NetworkPolicy and firewall.

- [ ] **ZSG-4002 — Envoy ExtAuthz wiring**
  - Invoke `zone-storage-authz` before S3 routing.
  - Set `failure_mode_allow=false`.
  - Configure bounded body buffering only for bulk operations.
  - Strip client `x-aurora-*`, `x-owner-*`, `x-zone-*` and metering headers.

- [ ] **ZSG-4003 — Upstream signing**
  - Apply the Phase 0 decision: trusted Envoy AWS signing filter or Rust S3 adapter.
  - Keep Zone service credentials in the approved secret mechanism, never static public configuration.
  - Ensure signing service/region/host cannot be overridden by the browser.
  - Do not buffer unbounded object payloads in ExtAuthz.

- [ ] **ZSG-4004 — Storage API routes**
  - Define explicit routes for list, head, metadata, tag read/write, bulk operations and presign ticket issuance.
  - Reject arbitrary S3 paths, hosts and buckets not bound by assertion.
  - Apply request size, page size, object-key length and bulk item limits.

- [ ] **ZSG-4005 — HA and backpressure**
  - Run multiple Zone Envoy and verifier replicas.
  - Add S3 connection pool limits, circuit breaker, per-principal/bucket rate limit and graceful drain.
  - Prevent bulk operations from consuming the full worker pool.

**Phase gate:** a browser with only Trinity can list/inspect authorized storage through the Zone path; direct MinIO access, forged headers and wrong-resource routing fail.

### Phase 5 — Metering and Cost integration

Goal: preserve accurate usage billing without relying on client STS access keys.

- [ ] **ZSG-5001 — Trusted metering envelope**
  - Emit resource ID, access session, actor, Zone, operation, status and bytes from trusted gateway metadata.
  - Never use client-provided owner/access-key headers as billing evidence.

- [ ] **ZSG-5002 — Durable usage event**
  - Publish `StorageUsageEvent` to the approved durable usage topic/stream with stable event ID.
  - At-least-once delivery and Cost inbox idempotency are mandatory.
  - A usage publish failure must not roll back an already completed S3 operation; it must enter bounded retry/quarantine.

- [ ] **ZSG-5003 — Presigned transfer metering**
  - Correlate presigned upload/download with provider-side access logs or Zone usage collector.
  - Do not charge based only on browser callbacks or requested content length.
  - Preserve `resource_id` and immutable owner snapshot lineage in Cost projection.

- [ ] **ZSG-5004 — Update billing God View**
  - Replace access-key-only identity assumptions in `god_view/billing/storage_usage_billing_god_view.md`.
  - Document list/head/tag/bulk request metering and provider-side presigned byte metering.
  - Keep Controlplane ownership as SoT and Cost ownership projection as the charging lookup.

**Phase gate:** successful operations are metered exactly once at the Cost ledger boundary; unknown ownership/usage is durable and visible, never silently dropped.

### Phase 6 — Cloud Console migration

Goal: remove browser STS/notification coupling.

- [x] **ZSG-6001 — Access-session API client**
  - Replace `requestBucketStsToken` with access-session request/status calls.
  - Do not decode protobuf/hex credential payloads in the browser.
  - Scope query keys by Trinity/session, Zone, workspace and bucket.

- [x] **ZSG-6002 — Object list/actions** *(Console metadata slice shipped; backend transfer and full failure-test gate remain)*
  - Use Zone Gateway HTTP contracts for list, head, metadata, tag and bulk.
  - Apply abort, pagination, bounded concurrency and idempotency semantics.
  - Display `not ready`, `stale`, `forbidden` and `degraded` distinctly.

- [ ] **ZSG-6003 — Upload/download**
  - Use presign ticket endpoints.
  - Keep URLs only in memory and clear them on expiry/context/logout.
  - Remove browser-side STS client construction and raw credential decoding.

- [ ] **ZSG-6004 — Realtime**
  - Consume only `ACCESS_READY`, operation status and runtime hints.
  - Rehydrate durable state over HTTP after reconnect; do not trust missed notification messages.

- [ ] **ZSG-6005 — Security and responsive tests**
  - Verify no secret appears in notification payloads, browser storage, console logs or telemetry.
  - Test object browser at 360/768/1024/1440px using the Console Table-First design.

**Phase gate:** Cloud Console has no S3 credential dependency for list/metadata/tag/bulk and no STS result decoder remains.

### Phase 7 — Validation, rollout and removal

- [ ] **ZSG-7001 — End-to-end security tests**
  - user A vs user B bucket;
  - wrong workspace/tenant/Zone;
  - forged assertion and header;
  - stolen `access_session_id` with another Trinity session;
  - replayed assertion;
  - expired/revoked/policy-revision mismatch;
  - direct MinIO bypass.

- [ ] **ZSG-7002 — Reliability tests**
  - Central Envoy failover;
  - Zone Envoy/verifier restart;
  - NATS leader failover;
  - KV cache miss/watch gap;
  - Kafka duplicate/out-of-order access command;
  - S3 timeout/429/5xx;
  - Cost topic outage and recovery.

- [ ] **ZSG-7003 — Load and resource tests**
  - 100k active access records;
  - prepare/revoke churn;
  - list/head/tag request burst;
  - concurrent bulk delete;
  - Envoy/verifier CPU, RAM, NATS disk and watch lag;
  - bounded memory during large object operations.

- [ ] **ZSG-7004 — Staged rollout**
  - Deploy Rust verifier and Zone KV bucket dark/observe-only first.
  - Enable new path for an internal Zone and test tenant.
  - Enable read-only list/head/tag, then bulk, then presign tickets.
  - Roll back at deployment/route level; there is no STS or notification-secret fallback.

- [x] **ZSG-7005 — Remove legacy STS backend and transport**
  - Removed the Controlplane endpoint/service/entity/DTO and legacy outbox producer.
  - Removed JO routing/result/notification support and Dataplane STS execution.
  - Removed STS protobuf messages, direct SDK dependency and public-MinIO endpoint configuration.
  - Cloud Console callsites remain only as explicit Phase 6 migration debt and
    must be deleted in the Console slice; no backend compatibility wrapper exists.

## 9. Rust service security boundary

| From | To | Allowed |
| --- | --- | --- |
| Central Envoy | Zone Storage Envoy | mTLS, signed internal assertion only |
| Zone Storage Envoy | `zone-storage-authz` | Envoy ExtAuthz gRPC over mTLS |
| `zone-storage-authz` | Zone NATS | Read `AURORA_ZONE_ACCESS`, public assertion-key projection only |
| Dataplane | Zone NATS | Write access record and revoke/consume namespace only |
| Zone Storage Envoy | S3/MinIO | Same-Zone endpoint, trusted upstream signing/adapter |
| Zone Storage Gateway | Controlplane DB/Redis/Vault | Forbidden |
| Browser | Zone NATS/MinIO admin/Authz service | Forbidden |
| Notification Service | STS/access assertion secret | Forbidden |

The new Rust service must not receive credentials to `AURORA_ZONE_CONFIG`, `AURORA_ZONE_HEALTH`, `AURORA_ZONE_COORDINATION`, Central Auth-State Redis or Controlplane PostgreSQL.

## 10. Failure semantics

- Missing Central Access Record: deny before Zone routing.
- Missing Zone Access Record: deny; do not fall back to S3.
- Assertion/record mismatch: deny and emit redacted security telemetry.
- NATS unavailable: fail closed for new requests; do not use an unbounded stale cache.
- Central Envoy unavailable: browser shows degraded/retry state; existing S3 direct URLs follow their own bounded expiry.
- Duplicate access job: idempotent CAS, no new scope.
- Legacy `storage.object.sts` command/result after the removal boundary:
  reject/quarantine through the normal unknown-contract path; never execute,
  notify or reconstruct credentials.
- Duplicate usage event: Cost inbox dedupe by event ID.
- S3 success followed by usage publish failure: persist retry/quarantine; never silently discard metering.
- Graceful shutdown: stop new ExtAuthz checks, drain in-flight S3 requests, preserve access records until TTL, and do not acknowledge an access job before KV write durability.

## 11. Definition of done

- `zone-storage-authz` is a tested, least-privilege Rust Envoy ExtAuthz service.
- Trinity never leaves Central as a raw cookie or secret.
- Browser never receives S3 STS credentials for the new path.
- Central ownership check creates the only authorization decision; Zone KV only gates Zone readiness/revocation.
- Zone Gateway rejects forged, stale, replayed and cross-resource assertions.
- NATS access bucket sizing and failover are measured for the agreed active-record ceiling.
- S3 direct ports cannot bypass Envoy/authz/metering.
- Cost usage has stable resource lineage and idempotent delivery.
- Cloud Console uses list/metadata/tag/bulk Gateway contracts and presigned transfer tickets.
- Relevant God Views, protobuf copies, Envoy routes, NetworkPolicies and deployment manifests are updated together.
- Legacy STS backend/transport is absent, and the completed Cloud Console
  migration contains no STS endpoint, decoder or notification handling.
