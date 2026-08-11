# Job Orchestrator Architecture

Job Orchestrator (JO) is the Central durability bridge between Controlplane,
Kafka, Shared Redis, and NATS Core. It relays committed encrypted commands,
settles validated Zone results against Controlplane authority, and runs bounded
repair workers. It never owns a business aggregate, decrypts Zone payloads, or
calls a Zone workload API.

## Runtime topology

```text
                       PostgreSQL logical WAL
                                 |
                                 v
                    +------------------------+
                    | ChangefeedWorker        |
                    | validate + route        |
                    +-----------+------------+
                                | Kafka durable command
                                v
 +------------+            +----------+            +----------------+
 | Control-   |<-----------|  Kafka   |----------->| Zone Dataplane |
 | plane PG   | result     +----------+  command   +----------------+
 +-----+------+ settlement      |  ^                       |
       ^                        |  |                       | result/report
       |                        v  |                       v
       |                  +-----------+              +----------+
       +------------------|ResultWorker|--------------|  Kafka   |
                          +-----------+              +----------+

 Shared Redis Stream --> mail watch bridge --> NATS Core --> Zone mail watch
 Zone mail report --> NATS Core --> mail ingest --> Redis TTL snapshot/PubSub
 Kafka Zone report --> zone-state and storage-usage workers --> Controlplane PG
```

Kafka is the durable Central-to-Zone command/result/report transport. NATS Core
is soft state only. Shared Redis carries bounded streams, locks, TTL snapshots,
and Pub/Sub wake-ups; it is not a business source of truth.

## Module ownership

| Runtime unit | Input | Durable settlement or output | Code owner |
|---|---|---|---|
| Changefeed | PostgreSQL logical replication | Kafka command after validated committed outbox record | `changefeed/` |
| Result worker | Kafka result | Controlplane PostgreSQL transaction, then notification/ownership stream | `results/` |
| Ownership relay | Controlplane PostgreSQL storage outbox | Shared Redis ownership stream and post-ACK marker | `outbox/` |
| Zone state | Kafka Zone reports | Controlplane health/state facts | `zone_state/` |
| Zone metadata repair | Kafka metadata query | Kafka compacted Zone metadata | `zone_state/metadata.rs` |
| Storage usage | Kafka Zone snapshot | Controlplane usage state and best-effort Redis wake-up | `storage_usage/` |
| Mail watch | Shared Redis stream | NATS Core Zone watch | `mail_runtime/watch.rs` |
| Mail report ingest | NATS Core | Shared Redis TTL snapshot and Pub/Sub | `mail_runtime/ingest.rs` |
| Mail reconciliation | PostgreSQL snapshot | Kafka Zone command after fencing | `reconcile/mail/` |
| Managed Service reconciliation | PostgreSQL outbox | Resets delivery marker only; WAL replays the immutable command | `reconcile/managed_service.rs` |

`RuntimeWorkers` owns the Zone state, metadata, storage, mail report, mail
ingest, mail watch, and watchdog futures directly. They are not detached tasks.
The process exits on a terminal critical sibling worker and a cancellation token
stops the changefeed before OpenTelemetry shutdown.

## Durable boundaries and replay rules

```text
Controlplane transaction commits outbox
  -> WAL record becomes visible
  -> JO validates metadata and publishes Kafka with acks=all
  -> only then may the WAL progress position advance

Zone result arrives from Kafka
  -> JO validates contract and authoritative outbox fence
  -> JO settles one Controlplane transaction
  -> notification or ownership follow-up is durable/guarded as applicable
  -> only then commits the Kafka offset
```

- Kafka consumers commit manually only after the PostgreSQL side effect or a
  durable quarantine/DLQ record.
- Managed Service settlement fences `instance_id`, `operation_id`, generation,
  and authoritative outbox state. Notification failure leaves the result
  unsettled for redelivery.
- Managed Service reconciliation uses `FOR UPDATE SKIP LOCKED` on bounded stale
  batches and resets only delivery markers. It never publishes Kafka directly.
- Zone watchdog uses a Shared Redis lease, but its SQL timestamp predicate and
  no-op state guard make lease overlap idempotent.
- Runtime workers retry with exponential delay capped at 30 seconds and
  deterministic per-pod jitter. A restart is the only permitted ambiguous
  outcome for a cancelled in-flight durability action.

## Dependency and security boundaries

```text
JO reads:  PostgreSQL CDC/snapshots, Kafka, Shared Redis, NATS Core
JO writes: PostgreSQL settlement/markers, Kafka, Shared Redis, NATS Core
JO never:  Zone KV, Zone private key, Kubernetes API, workload API, plaintext
           customer credentials, rendered mail, or Controlplane HTTP handlers
```

PostgreSQL capabilities are deliberately separate: `role-cdc-read` for WAL and
snapshots, `role-job-dispatch-rw` for post-ACK delivery markers, and
`role-job-result-rw` for result settlement CTEs. Startup fails closed if the
required capability is unavailable; a broader CDC identity is never a write
fallback.

Connection configuration is typed by downstream in `src/config/`. JO captures
the environment once before spawning workers, resolves connection records from
Vault, and fails fast on ambiguous TLS/auth configuration. Production transport
invariants are TLS/mTLS where configured, idempotent Kafka producers with
`acks=all`, bounded retries, and no silent downgrade to plaintext.

## What is intentionally outside this document

This document explains JO internals. Business state transitions remain in their
own domain workflow documentation: managed-service lifecycle, mail
configuration, storage, hypervisor, and Zone lifecycle. Telemetry contracts are
in [TELEMETRY.md](./TELEMETRY.md).

