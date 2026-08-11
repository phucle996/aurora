# Storage Bucket Usage Projection — God View

This runtime workflow projects physically observed MinIO usage into
Controlplane PostgreSQL. It is not an API request and it does not use the
storage command/result outbox. PostgreSQL `used_bytes` is the durable read model
for bucket list/detail and a soft realtime notification wakes the UI after a
changed projection.

## Runtime contract

| Item | Contract |
|---|---|
| Trigger | Every 15 seconds from the current Zone Dataplane leader. |
| Producer authority | Only a leader session that still permits external side effects. |
| Eligibility | Zone metadata must read successfully, status must be `active`, and `services.storage` must be true. |
| Source observation | MinIO `ListBuckets`, then `ListObjectsV2` pages for names beginning `ws-` or `tn-`. |
| Transport | `StorageBucketSizesSnapshotV1` on `{prefix}.storage.sizes.v1`, key `zone_id`. |
| Consumer | JO consumer group `aurora-job-orchestrator-storage-sizes-v1`. |
| Durable SoT | Controlplane PostgreSQL `personal_buckets.used_bytes` or `tenant_buckets.used_bytes`. |
| Soft projection | Shared Redis Pub/Sub `aurora:realtime:notifications`; loss does not block Kafka offset commit. |

## Message and key contract

| Field/key | Store | Constraint / operation |
|---|---|---|
| `StorageBucketSizesSnapshotV1.event_id` | Kafka protobuf | Exactly 16 bytes. It is diagnostic, not a per-bucket dedup table. |
| `zone_id` | Kafka protobuf | Exactly 16 bytes and used as producer key. JO currently parses but discards it after validation. |
| `observed_at_unix_ms` | Kafka protobuf | Carried but not used as a PostgreSQL ordering fence. |
| Bucket entries | Kafka protobuf | At most 50,000; non-negative size; non-empty name ≤128 beginning `ws-` or `tn-`. |
| `storage.personal_buckets.used_bytes` | PostgreSQL | `UPDATE ... IS DISTINCT FROM` returns owner only if value changed. |
| `storage.tenant_buckets.used_bytes` | PostgreSQL | Same distinct update, then query active tenant member ids. |
| `aurora:realtime:notifications` | Shared Redis Pub/Sub | JSON `{kind:"storage",user_id,payload:{sizes}}`, best effort only. |
| Dead-letter topic | Kafka | Invalid snapshots are DLQ-published before source offset commit. |

## Phase 1 — Leader-gated MinIO scan in Dataplane

A Dataplane process starts the scanner only as a Zone leader duty. At each
cycle it waits 15 seconds, reads Zone metadata from `AURORA_ZONE_CONFIG`, skips
when metadata read fails or storage is disabled/inactive, and checks leader
permission both before scanning and before publishing. It lists all buckets,
ignores system names outside `ws-`/`tn-`, scans every paginated object list and
uses saturating addition for object sizes. It publishes a complete snapshot only
when non-empty.

```mermaid
sequenceDiagram
    participant L as Zone leader session
    participant KV as AURORA_ZONE_CONFIG
    participant DP as Dataplane storage scanner
    participant M as Private MinIO
    participant K as Storage sizes Kafka

    L->>L: Wait 15 seconds
    L->>KV: Read Zone metadata
    alt metadata unavailable, inactive or storage disabled
        KV-->>L: skip cycle
    else leader may perform side effects
        L->>DP: Start one scan
        DP->>M: ListBuckets
        loop each ws or tn bucket and every page
            DP->>M: ListObjectsV2
            M-->>DP: object sizes and continuation
        end
        DP->>L: Recheck leader permission
        alt non-empty snapshot and still leader
            L->>K: Publish StorageBucketSizesSnapshotV1 keyed by Zone
        else empty or leadership lost
            L->>L: Skip publish
        end
    end
```

## Phase 2 — JO validation, PostgreSQL projection and soft notification

JO polls Kafka manually. It requires payload ≤8 MiB, decodes schema version 1,
validates byte lengths/count/names/non-negative sizes, and sends malformed
records to DLQ before committing their source offset. For a valid snapshot it
iterates entries. Personal update joins workspace and returns one owner only
when value changed. Tenant update returns all active members only when its
bucket changed. Database side-effect error exits listener without committing
this or later partition offsets. After all durable updates, it publishes a
per-user size map to Redis. Pub/Sub failure is logged and ignored because the
database projection is already authoritative.

```mermaid
sequenceDiagram
    participant K as Storage sizes Kafka
    participant JO as JO storage usage worker
    participant PG as Controlplane PostgreSQL
    participant R as Shared Redis PubSub
    participant U as Cloud Console

    K-->>JO: Snapshot record
    JO->>JO: Decode size and namespace validation
    alt invalid snapshot
        JO->>K: Publish DLQ record
        JO->>K: Commit source offset
    else valid snapshot
        loop each bucket
            alt ws bucket
                JO->>PG: UPDATE personal used_bytes if distinct
                PG-->>JO: changed owner or no row
            else tn bucket
                JO->>PG: UPDATE tenant used_bytes and active members
                PG-->>JO: changed member ids or no row
            end
        end
        alt any PostgreSQL update fails
            JO->>JO: Return error without committing offset
        else all updates durable
            JO->>R: Publish changed sizes per user
            R-->>U: best effort refresh hint
            JO->>K: Commit source offset
        end
    end
```

## Phase 3 — Read-side visibility

Personal bucket list/detail reads `used_bytes` directly from PostgreSQL after
normal ACR and Controlplane authorization. UI can receive Pub/Sub hint, but
loss/reordering of notification cannot alter the value it sees after refetch.

```mermaid
sequenceDiagram
    participant U as Cloud Console
    participant R as Shared Redis PubSub
    participant E as Envoy and ACR
    participant CP as Controlplane storage read
    participant PG as PostgreSQL

    R-->>U: Optional storage size changed hint
    U->>E: Refetch authorized bucket list or detail
    E->>CP: Personal rewritten request and trusted context
    CP->>PG: Read used_bytes projection
    PG-->>CP: latest committed value
    CP-->>U: authoritative JSON response
```

## Failure, ordering and accuracy rules

| Condition | Actual behavior |
|---|---|
| MinIO scan fails part way | Entire cycle is dropped and no partial snapshot is published. |
| Snapshot is empty | No snapshot is emitted, so a bucket disappearing from MinIO does not itself set Central usage to zero. |
| Kafka publish fails | Error is logged. Next successful leader cycle retries with a new full snapshot. |
| Invalid source snapshot | DLQ publish must succeed before source offset is committed. |
| PostgreSQL update fails | Worker stops with error before committing current offset, causing replay. Replays are safe with `IS DISTINCT FROM`. |
| Redis notification fails | Offset commits after durable PG update. UI eventually learns by refetch. |
| Snapshot ordering | `observed_at_unix_ms` is not used to fence out-of-order snapshot writes. A late snapshot can overwrite a newer `used_bytes`; this is an accuracy discrepancy. |
| Snapshot source Zone | JO validates that `zone_id` is 16 bytes but discards it before updating by globally unique bucket name. The consumer does not prove that snapshot Zone equals stored bucket Zone. |
| Tenant runtime path | Worker supports `tn-` projections although tenant HTTP handlers are currently no-op. This is a runtime data path, not evidence that tenant storage API is enabled. |

## Code map

- `dataplane/src/leader/leadership.rs`
- `dataplane/src/leader/infra/storage.rs`
- `dataplane/src/infra/kafka.rs`
- `job-orchestrator/src/storage_usage/worker.rs`
- `job-orchestrator/src/storage_usage/store.rs`
- `proto/platform_transport.proto`
