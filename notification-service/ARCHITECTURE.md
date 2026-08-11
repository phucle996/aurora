# Notification Service Architecture

Notification Service is a Central projection and delivery service. It owns
durable self-user activity and inbox projections in ScyllaDB plus the
Centrifugo publishing adapter. It does not own IAM, billing, job lifecycle, or
any customer resource aggregate.

## Runtime topology

```text
                         Shared Redis
                    +-------------------+
 ACR auth RPC  <---->| Pub/Sub reply bus  |<----> Centrifugo connect
                    +-------------------+
                         |          |
                         |          +--> soft realtime Pub/Sub
                         v
               durable Streams
          job_notifications | user_activity
                    |        |
                    v        v
          +---------------------------+
          | Notification Service      |
          |                            |
          | job consumer               |
          | activity consumer          |
          | realtime Pub/Sub consumer |
          | auth reply router          |
          +-------+------------+-------+
                  |            |
       durable    v            v soft state
              ScyllaDB     Centrifugo
          activity/inbox   notifications:<user_id>
                             runtime:<user_id>
```

`job_notifications` and `user_activity` are at-least-once Redis Streams.
Realtime Pub/Sub is an advisory wake-up channel only. A reconnecting client
must rehydrate its durable history/inbox through the self-user API rather than
assume a realtime message was delivered.

## Module ownership

| Module | Owns | Must not own |
|---|---|---|
| `inbound/connect.rs` | Bounded Centrifugo connect HTTP input and response mapping | Redis protocol or authorization policy |
| `application/auth.rs` | User/admin credential decision and channel grants | Shared Redis commands or Centrifugo HTTP |
| `infra/redis/auth_bus.rs` | Bounded auth request/reply and reply router | Business authorization state |
| `inbound/job_stream.rs` | PEL/XCLAIM, validation, ACK/XDEL for job notification Stream | Job aggregate settlement |
| `application/job_notifications.rs` | Scylla activity/inbox projection before notification publish | Producer business transition |
| `inbound/activity_stream.rs` | PEL/XCLAIM and activity projection delivery | Realtime publish for every activity |
| `inbound/realtime_pubsub.rs` | Bounded soft realtime envelope ingestion | Durable queue semantics |
| `application/runtime_updates.rs` | Runtime wake-up validation and channel selection | Resource ownership or lifecycle decision |
| `infra/scylla/` | Scylla schema/client/store adapter | Producer aggregate authority |
| `infra/centrifugo.rs` | The only Centrifugo API credential and publish adapter | Durable event state |

## Durability, ordering, and recovery

```text
Redis Stream entry
  -> validate bounded protobuf envelope
  -> write Scylla projection
  -> publish Centrifugo when the workflow requires it
  -> ACK and XDEL only after durable delivery condition
```

- Activity storage keys converge by `(user_id, month_bucket, occurred_at,
  event_id)`; inbox keys converge by `(user_id, month_bucket, created_at,
  notification_id)`.
- Managed Service uses one stable `command_event_id` as notification identity.
  `PROCESSING` inserts the row; terminal results update it with monotonic
  `status_version` and retain original timestamps. A stale replay cannot lower a
  terminal projection.
- Poison Stream records are quarantined as metadata before ACK. Raw payload is
  not copied into the quarantine record or logs.
- A Scylla failure keeps the Stream entry pending. A Centrifugo failure never
  turns Pub/Sub into a durable queue and does not replace the stored projection.
- `user_activity` producers use a capacity guard. They do not trim a Stream
  that may still have a pending-entry list.

## Process lifecycle

`Runtime::build` resolves connections, creates the reply router and three
inbound consumers, then retains their task handles. Startup fails fast if the
required Redis, Scylla, Centrifugo, or telemetry dependency cannot be created.

On SIGINT/SIGTERM, Axum drains HTTP first. `Runtime::shutdown` broadcasts a
watch cancellation signal, waits only until `shutdown_timeout`, and aborts any
remaining supervised task. `TelemetryGuard` then flushes tracing and metrics
while the bounded log writer remains alive.

## Security boundaries

- The self-user API derives the subject from the verified edge identity; it
  never accepts a client `user_id`.
- Centrifugo channel subjects are UUID-validated before a
  `notifications:<user_id>` or `runtime:<user_id>` channel is formed.
- Connect input is bounded to 64 KiB and realtime envelopes to 256 KiB.
- The service has no NATS, PostgreSQL, Zone KV, Kubernetes, or business-aggregate
  capability. Scylla is a projection store, never durable business authority.
- Customer runtime logs, metrics, and events remain Zone-local through Zone
  Public Edge; Notification Service only publishes durable completion or
  explicitly soft Central runtime wake-ups.

Telemetry implementation and operational signals are documented in
[TELEMETRY.md](./TELEMETRY.md). End-to-end API and stream workflows remain in
the relevant God Views.

