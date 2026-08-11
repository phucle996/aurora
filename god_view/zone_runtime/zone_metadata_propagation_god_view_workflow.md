# Zone metadata propagation — God View

Projects Controlplane’s committed Zone status and desired-service changes into the configured Zone’s NATS KV. This is an asynchronous runtime workflow; it is not an HTTP response phase.

## Trigger and authority

| Trigger | Authoritative input | Owners | Durable settlement |
|---|---|---|---|
| WAL `INSERT` or `UPDATE` for `hierarchy.zones` or `hierarchy.zone_services` | PostgreSQL aggregate | JO ChangefeedWorker, Zone leader | Kafka `acks=all`, then consumer settlement after KV projection |

## Key contract

| Store | Key / topic | Value |
|---|---|---|
| Kafka | per-Zone metadata topic | `ZoneMetadataSnapshotV1` keyed by Zone UUID |
| NATS JetStream KV | `AURORA_ZONE_CONFIG/zone.metadata` | status plus desired service flags |

## Phase 1 — PostgreSQL WAL → JO

### Internal input and output

| Part | Contract |
|---|---|
| Input | decoded WAL `INSERT` or `UPDATE`; Zone ID comes from `zones.id` or `zone_services.zone_id` |
| JO reread | `query_zone_metadata` reads authoritative current status and all desired services through a read-only PostgreSQL session |
| Output | full `ZoneMetadataSnapshotV1` with event UUID, exact Zone UUID bytes, service map, timestamp and `schema_version=1` |
| Settlement | publication must receive Kafka `acks=all` before ChangefeedWorker advances replication position |

```mermaid
sequenceDiagram
    participant PG as PostgreSQL
    participant C as JO ChangefeedWorker
    participant K as Kafka metadata topic
    PG-->>C: WAL INSERT or UPDATE
    C->>PG: read current Zone status and all service desired states
    C->>K: publish full ZoneMetadataSnapshotV1 with acks=all
    alt Kafka ACK
        C->>C: advance replication position
    else publish failure
        C->>C: leave WAL unsettled for replay
    end
```

For `zone_services`, JO suppresses a record whose desired value equals its durable process cache. `actual_state` updates therefore do not republish desired metadata.

## Phase 2 — Kafka → Zone KV

### Internal input and output

| Part | Contract |
|---|---|
| Input | manual-consumed compacted-topic record under current Zone leader/Kafka assignment epoch |
| Validation | non-tombstone payload, Protobuf decode, `schema_version=1`, exact configured Zone UUID |
| Output | Config KV status then every service flag; Dataplane intake reads this projection |
| Settlement | poison record gets durable DLQ before source settle; transient KV failure leaves source unsettled |

```mermaid
sequenceDiagram
    participant K as Kafka metadata topic
    participant L as current Zone leader
    participant KV as Zone Config KV
    participant D as Dataplane intake
    K-->>L: manual poll snapshot
    L->>L: require schema=1 and exact configured Zone UUID
    alt invalid or tombstone
        L->>K: durable DLQ then settle source
    else valid
        L->>KV: apply status then each desired service flag
        alt KV apply succeeds
            L->>K: settle contiguous offset
            D->>KV: read metadata before intake
        else KV error
            L->>L: leave source unsettled for replay
        end
    end
```

Only the fenced Zone leader projects and settles. Missing, corrupt, or unavailable metadata makes intake fail closed; only `active` permits new work.

## Current limitation

The listener applies status and service entries sequentially; it does not atomically replace the entire KV aggregate or clear absent service entries. Changefeed also ignores WAL `DELETE`. The snapshot is complete at Kafka, but hard-delete detach/tombstone semantics are not implemented.
