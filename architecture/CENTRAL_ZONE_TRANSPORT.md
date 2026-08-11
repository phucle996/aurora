# Central–Zone Transport Architecture

Kafka is Aurora's durable Central–Zone transport. NATS Core carries soft runtime
state only, and each Zone's NATS JetStream KV is its own runtime database.
Neither replaces the other.

```text
Controlplane PostgreSQL -- logical WAL --> Job Orchestrator -- Kafka --> Dataplane
                                                           <-- Kafka -- result/report

Dataplane -- NATS Core soft report/watch --> Job Orchestrator
Dataplane -- Zone JetStream KV --> Zone-local runtime projection
```

## Transport contract

- Wire format is versioned Protobuf. `JobCommandV1`, result/report, metadata,
  storage-size, and DLQ records never use JSON.
- Kafka delivery is at least once. Producers are idempotent with stable keys,
  `acks=all`, compression, and bounded retry. Consumers manually commit only
  after the durable side effect or durable sanitized DLQ succeeds.
- Every Zone command has a concrete non-nil Zone UUID and uses the matching
  per-Zone command topic. `platform`, `global`, empty, or cross-Zone routes
  fail closed. Production ACLs isolate producer topics and consumer groups per
  Zone.
- Controlplane seals the complete Zone-bound payload with HPKE before committing
  the outbox. JO validates public metadata and relays exact ciphertext; it has
  no Zone private key and never decrypts. Dataplane validates route/schema/AAD,
  decrypts in memory, and never writes plaintext to logs, Kafka, Redis, or KV.

## Durable command and result lanes

```text
Aggregate mutation + protected outbox commit
  -> WAL visible to JO
  -> JO validates and publishes Kafka command
  -> Kafka ISR acknowledgement
  -> source LSN/delivery marker may advance

Dataplane terminal result
  -> Kafka result record
  -> JO validates authoritative outbox/operation fence
  -> one Controlplane settlement transaction
  -> notification/ownership follow-up where required
  -> Kafka offset commit
```

Crash before a Kafka acknowledgement leaves the source unsettled and replays.
Crash after an acknowledgement but before source settlement can duplicate a
record; stable event identity and executor/settlement idempotency converge it.
There is no exactly-once claim across PostgreSQL, Kafka, external providers, and
runtime projections.

## Runtime support lanes

| Lane | Direction | Settlement |
|---|---|---|
| Zone metadata snapshot | JO → per-Zone compacted Kafka → Dataplane KV | Full aggregate plus Zone KV CAS; invalid records DLQ before offset commit |
| Metadata repair query | Dataplane → Kafka → JO → compacted snapshot | JO commits query only after snapshot Kafka ACK |
| Zone report | Dataplane → Kafka → JO → Controlplane PostgreSQL | Timestamp/fence guarded state update |
| Storage size snapshot | Dataplane → Kafka → JO → Controlplane read model | Consumer stops on transient durable side-effect failure |
| Mail runtime watch/report | Shared Redis ↔ JO ↔ NATS Core ↔ Dataplane | Soft state and TTL snapshot only |

Zone-specific runtime workflow contracts live in `god_view/zone_runtime/` or
the domain that owns their business state. This document is architecture, not a
business/API workflow specification.

