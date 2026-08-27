# Tenant Mail Consumer Drain — God View

## API-scope contract

This is the tenant owner branch, not a self-user API. The browser never selects
an internal owner-prefixed route. Controlplane authorizes `email:consumer:delete`
with the existing required-level selector `"*"`; PostgreSQL rechecks tenant
resource authority. Drain and Delete are actions of the consumer file branch;
they do not add a separate handler/service/repository dependency.

## Phase 1 — Client → Envoy → ACR

Client sends `POST /api/v1/critical/mail/consumers/:id/drain`
with JSON `{"expected_config_version":"7","timeout_seconds":30}`, Content-Type, Origin/CSRF headers,
Trinity cookies (`access_token`, `access_key`, `access_secret`),
`client_device_id`, `workspace_id`, and the critical request proof headers
`x-session-proof-signature`, `x-session-proof-timestamp`,
`x-session-proof-challenge-id`. Trace headers are optional.

Envoy sends method/path/headers/body and peer context in its ext_authz CheckRequest.
ACR applies CORS/CSRF, rate limiting, verified session/local state checks and
consumes the request-bound proof. Denial is returned locally through Envoy;
no Mail mutation occurs. Success rewrites the path to
`/api/v1/tenant/critical/mail/consumers/:id/drain`,
preserving method and JSON body.

ACR removes incoming proof material/verified marker, administrative proof headers
and runtime-assertion headers. It overwrites `x-user-id`, `x-user-name`,
`x-user-level`, `x-tenant-id`, `x-client-device-id`, `x-zone-id` from its
verified context; removes caller `x-workspace-id` and injects the selected workspace
cookie. It injects `x-session-proof-verified=true` and the consumed challenge ID.
Workspace selection is not ownership proof: the repository must check it.
Controlplane runs RequireSessionProof, then permission authorization, before
`TenantConsumerHandler.Drain`.

## Phase 2 — Consumer transport, service and durable request

Handler strictly parses one bounded JSON object, a positive int64 decimal-string
version, timeout 1..3600 seconds, and resource/context UUIDs. Service loads its
flat drain target and verifies current version and enabled/paused state.
Repository rechecks the durable facts in its own CTE: `hierarchy.tenant_workspaces`: id=workspace, zone_id=Zone, tenant_id=tenant; `hierarchy.tenant_memberships`: actor belongs to that tenant with status=active.
The target row lock, state/version/parallelism fence, absence of live consumer
outbox, `enabled|paused → draining`, and one outbox insertion are atomic.
Only one concurrent request wins. The existing single `desired_state` column
is used; no additional actual-state column or product fields on generic outbox.
HTTP 202 returns operation_id, consumer_id and draining; 409 asks the user to
refresh a stale or busy consumer.

## Phase 3 — JO command dispatch and DP admission

The committed `mail.mail_outbox_records` entry contains the sealed command;
target state stays inside the product protobuf, not generic outbox columns.
JO changefeed/dispatch validates the durable event/Zone/epoch fence and publishes
the registered Mail topic through the configured Zone Kafka command transport.
At-least-once delivery is expected, not exactly-once provider mutation.

DP validates and decrypts the command at intake, acquires the execution lease and
runs the consumer executor. The watchdog renews the lease; lease loss cancels
the current execution and schedules the exact command/epoch with jitter.
Unknown provider outcomes retry the same attempt, never fabricate FAILED.
An immutable command-hash-bound terminal receipt in the dedicated Zone completion KV allows replay
to republish the result without applying the completed mutation again.
Unrecoverable publication/retry-queue failure requests process restart so Kafka
can redeliver the unsettled source.

## Phase 4 — Consumer drain barrier and runtime settlement

`MailConsumerDrainV1` binds consumer UUID, exact current config version,
parallelism, and bounded timeout. DP CAS-writes DRAINING to the matching Zone
consumer head without advancing immutable configuration version or hash.
Runtime registration and the barrier CAS the same consumer head. The flat
`runtime_generations` list contains every generation admitted before the barrier,
including generations pinned to an older config version. Each generation has a
unique UUID and a protobuf journal at `mail.consumer.runtime.{id}.{generation}`;
it is never identified only by a reusable lease token. `runtime_protocol=1`
certifies that a new consumer identity was created under this journal contract.
Legacy heads remain usable for ordinary runtime but cannot be certified drained
without establishing evidence for their pre-journal work.

Pause/COW requests graceful intake stop rather than aborting the old generation.
Already-owned work retains its pinned config and bounded lease permission.
Kafka also waits for dirty terminal offset commits. Runtime CASes its journal
from prepared to running before touching the broker, then to settled only after
the suite confirms settlement. It removes only its own head membership by CAS.
An empty replacement cannot clear an older generation. Lease expiry/absence is
never completion evidence.

Drain may finish retiring a prepared generation by CAS-fencing a late start, or
a settled generation whose process died before removing head membership. It
cannot retire a running generation just because its pod/lease disappeared.
Drain polls head membership with jitter and writes
`MailConsumerDrainedV1` under
`mail.consumer.drain.result.{consumer_id}.{config_version}`.
Timeout is an unknown outcome and exact-command retry, not a successful drain.
Drain does not promise the customer's externally produced queue is globally empty.

Deferred production limitation: a process that dies after broker admission still
requires durable per-message handoff/recovery in each broker suite. The generation
journal deliberately keeps this operation DRAINING rather than inferring success.
It is not a per-message inbox and does not claim exactly-once JMAP submission.
Completion receipt GC likewise stays disabled until terminal settlement/replay
retirement is enforced end-to-end. No new PostgreSQL state column or generic
outbox product field is introduced.

### Deferred decision — 2026-08-27

The owner explicitly accepted committing the reviewed implementation without
adding a per-message inbox in this change. Future work will evaluate replacing
the Mail Zone KV persistence with Zone-local PostgreSQL and design the durable
inbox there; this is not a connection to Controlplane/Billing PostgreSQL.
No PostgreSQL migration, inbox worker or early source-broker ACK is implemented
or authorized by this deferral. Existing broker settlement semantics remain.
A crash after a generation starts broker work can still leave Drain pending;
Delete must remain blocked rather than treat missing lease/pod evidence as drained.
The future design must specify durable handoff before any early ACK, pinned
configuration, HA claim fencing and ambiguous-send handling. An inbox alone does
not guarantee exactly-once mail delivery. Receipt GC/replay retirement remains a
separate outstanding item; accepting this commit does not certify production readiness.

## Phase 5 — JO confirms drained

JO verifies the result against the locked Mail outbox. Typed success must match
resource UUID, Zone/workspace, current version and parallelism. Exactly one
tenant resource row moves `draining → drained` in the transaction that settles
the outbox. Non-success never marks drained. Stale processing cannot downgrade
terminal success. Replayed terminal results only retry notification.
Delete remains unavailable until this confirmed state is visible to the client.

## Phase 6 — Timeline, notification and client refresh

After the database transaction commits, JO `results/notify.rs` publishes
`JobNotificationEvent` to Redis `stream:{job_notifications}`.
The notification-service job stream calls `JobNotificationService`, which
persists activity/inbox projections before the realtime publisher sends to
Centrifugo. Failed notification publication keeps the result retryable; a
terminal replay reconstructs notification context without repeating mutation.
Cloud Console invalidates its consumer list after a command and polls every
three seconds while rows are draining/deleting. It refetches confirmed state;
the HTTP command response is not proof of completed resource work.

## Implementation map

- `controlplane/internal/mail/transport/http/handler/tenant_consumer_handler.go`
- `controlplane/internal/mail/service/tenant_consumer_service_impl.go`
- `controlplane/internal/mail/repository/tenant_consumer_repo_impl.go`
- `controlplane/internal/mail/domain/entity/tenant_consumer.go`
- `dataplane/src/executor/mail/consumer.rs`
- `dataplane/src/executor/mail/runtime/consumer_supervisor.rs`
- `job-orchestrator/src/results/mail/consumer.rs`
- `cloud-console/src/features/mail/api.ts`
- `cloud-console/src/app/(console)/mail/components/ConsumersTab.tsx`
