# Storage Object Management — God View

> [!IMPORTANT]
> Object/bucket mutations use the same PostgreSQL outbox → JO WAL → Central Kafka → Dataplane Zone →
> Kafka result lifecycle defined in
> [`bucket_creation_god_view.md`](bucket_creation_god_view.md).

## 1. Boundary

- Browser/SDK calls Envoy public route; ACR validates identity/session proof.
- Controlplane owns authorization, ownership and business metadata.
- JO owns CDC dispatch/result routing, not business authorization.
- Dataplane owns physical MinIO operation in the selected Zone.
- Cost receives projected `owner_id + owner_type`; it never queries CP DB in charging path.

## 2. Generic mutation sequence

```mermaid
sequenceDiagram
    participant C as Client
    participant CP as Controlplane
    participant PG as PostgreSQL outbox
    participant PG as PostgreSQL + outbox
    participant JO as Job Orchestrator
    participant K as Central Kafka
    participant DP as Dataplane
    participant M as MinIO

    C->>CP: authorized object/bucket mutation
    CP->>PG: business mutation + outbox one transaction
    PG-->>JO: logical WAL
    JO->>K: JobCommandV1 to exact Zone, acks=all
    K-->>DP: manual consume
    DP->>M: idempotent physical operation
    DP->>K: durable terminal/retry result
    K-->>JO: result consume
    JO->>PG: guarded completion transaction
```

## 3. Invariant

- `job_id`, resource ID, Zone and schema are immutable for an attempt chain.
- Retry increments attempt and publishes durable retry before settling original.
- Poison command/result goes DLQ before commit.
- Rebalance epoch and contiguous settlement prevent stale/high offset commits.
- Physical delete precedes business hard-delete.
- External MinIO operation requires stable idempotency semantics.
- Plaintext infrastructure credential never enters Kafka or PostgreSQL business payload.

## 4. Size/read model

Bucket size is a separate periodic snapshot workflow documented in
[`bucket_list_god_view.md`](bucket_list_god_view.md). It uses
`aurora.storage.sizes.v1`, not command/result topics and not Redis Streams.

## 5. References

- Platform transport: [`kafka_platform_transport_god_view.md`](../platform/kafka_platform_transport_god_view.md)
- Bucket create/delete: [`bucket_creation_god_view.md`](bucket_creation_god_view.md)
- Bucket list/size: [`bucket_list_god_view.md`](bucket_list_god_view.md)
- Ownership: [`resource_ownership_god_view.md`](../billing/resource_ownership_god_view.md)

## 6. Zone Storage Gateway access-session path (staged)

The non-secret access-session path is the only backend authorization flow for
Console object operations. The legacy STS endpoint, command, executor and
secret-bearing result have been removed. The path is not release-ready until
Zone mTLS certificates, assertion public keys, the Envoy route/S3 signing
adapter and the Cloud Console migration are complete.

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Central Envoy/ACR
    participant CP as Controlplane
    participant R as Auth-State Redis
    participant JO as Job Orchestrator
    participant K as Kafka
    participant DP as Dataplane Zone
    participant ZG as Zone Storage Authz
    participant S3 as Private MinIO/S3

    B->>E: POST /api/v1/storage/buckets/{id}/access-sessions
    E->>CP: trusted actor/workspace/Zone context
    CP->>CP: repository owner check
    CP->>PG: storage.access.prepare outbox (metadata only)
    CP->>R: protobuf StorageAccessRecord at storage_access:{session_id} with TTL
    PG-->>JO: WAL/changefeed
    JO->>K: durable command to exact Zone
    K->>DP: prepare access projection
    DP->>DP: CAS AURORA_ZONE_ACCESS/{session_id}
    B->>E: storage request + Trinity + access_session_id
    E->>R: verify central projection
    E->>ZG: signed assertion over mTLS
    ZG->>DP: read matching Zone access record
    ZG->>S3: authorized private S3 request
    S3-->>B: object/list/tag response
```

Invariants:

- The Central repository remains the ownership/IDOR authority; the Zone KV
  entry is only an execution/readiness projection.
- The access-session command and result contain no S3 access key, secret key or
  session token. `ACCESS_READY` is not sent through the user notification lane.
- ACR signs with the dedicated Vault Transit asymmetric key; Zones receive only
  the versioned public key. Missing key material, Redis, KV or mTLS fails closed.
- The Zone verifier compares session, binding hash, actor, resource, workspace,
  Zone, action, policy revision, canonical path/body hashes and expiry. The
  assertion `jti` is atomically replay-fenced in a bounded cache.
- There is no STS/notification-secret fallback. Rollback is limited to
  deployment and route configuration, and object traffic remains disabled when
  the Gateway trust chain is incomplete.

Implementation detail and rollout gates are tracked in
[`zone_storage_gateway_access_refactor_plan.md`](zone_storage_gateway_access_refactor_plan.md).
