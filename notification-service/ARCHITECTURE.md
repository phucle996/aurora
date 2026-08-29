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
                             |
                   durable Streams
              job_notifications | user_activity
                        |        |
                        v        v
              +---------------------------+
              | Notification Service      |
              |                           |
              | transport::stream:        |
              |   job consumer            |
              |   activity consumer       |
              | auth reply router         |
              | transport::http_handler   |
              +-------+------------+------+
                      |            |
           durable    v            v realtime
                  ScyllaDB     Centrifugo
              activity/inbox   notifications:<user_id>
```

`job_notifications` and `user_activity` are at-least-once Redis Streams.
A reconnecting client rehydrates its durable history/inbox through the self-user
API while live notifications arrive via Centrifugo WebSocket channels.

## Module ownership

| Module | Owns | Must not own |
|---|---|---|
| `transport/http_handler/realtime.rs` | Bounded Centrifugo connect HTTP webhook and response mapping | Redis protocol or authorization policy |
| `transport/http_handler/timeline.rs` | Self-user activity/inbox query and mark-read HTTP handlers | Direct Scylla queries or auth logic |
| `transport/stream/job_stream.rs` | PEL/XCLAIM, validation, ACK/XDEL for job notification Stream | Job aggregate settlement |
| `transport/stream/activity_stream.rs` | PEL/XCLAIM and activity projection delivery | Realtime publish for every activity |
| `service/auth.rs` | User/admin credential decision and channel grants via AuthVerifier | Shared Redis commands or Centrifugo HTTP |
| `service/activity.rs` | Activity timeline business queries and mutations | HTTP request handling or raw database queries |
| `service/notification.rs` | Notification inbox business queries and mark-read logic | Direct Scylla queries |
| `service/job_notifications.rs` | Scylla activity/inbox projection before notification publish | Producer business transition |
| `repo/timeline.rs` | ScyllaDB queries and mutations for timeline events | Business rules or HTTP serialization |
| `infra/scylla/` | Scylla connection, session management and schema execution | Business query composition |
| `infra/redis/auth_bus.rs` | Bounded auth request/reply and reply router | Business authorization state |
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
  turns into a durable queue and does not replace the stored projection.
- `user_activity` producers use a capacity guard. They do not trim a Stream
  that may still have a pending-entry list.

## Process lifecycle

`Runtime::build` resolves connections, creates the reply router and two
stream consumers, then retains their task handles. Startup fails fast if the
required Redis, Scylla, Centrifugo, or telemetry dependency cannot be created.

On SIGINT/SIGTERM, Axum drains HTTP first. `Runtime::shutdown` broadcasts a
watch cancellation signal, waits only until `shutdown_timeout`, and aborts any
remaining supervised task. `TelemetryGuard` then flushes tracing and metrics
while the bounded log writer remains alive.

## Security boundaries

- The self-user API derives the subject from the verified edge identity; it
  never accepts a client `user_id`.
- Centrifugo channel subjects are UUID-validated before a
  `notifications:<user_id>` channel is formed. Runtime reads do not traverse
  Notification Service; the browser uses a short-lived ACR assertion directly
  against Zone Public Edge.
- Connect input is bounded to 64 KiB.
- The service has no NATS, PostgreSQL, Zone KV, Kubernetes, or business-aggregate
  capability. Scylla is a projection store, never durable business authority.
