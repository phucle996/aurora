# Tenant Mail Consumer Delete — God View

Deletion is a separate user action after the Drain workflow has confirmed
`drained`. It no longer performs drain inside Delete.

## API-scope contract

This is the tenant owner branch, not a self-user API. The browser never selects
an internal owner-prefixed route. Controlplane authorizes `email:consumer:delete`
with the existing required-level selector `"*"`; PostgreSQL rechecks tenant
resource authority. Drain and Delete are actions of the consumer file branch;
they do not add a separate handler/service/repository dependency.

## Phase 1 — Client → Envoy → ACR

Client sends `DELETE /api/v1/critical/mail/consumers/:id`
with JSON `{"expected_config_version":"7","reason":"console delete"}`, Content-Type, Origin/CSRF headers,
Trinity cookies (`access_token`, `access_key`, `access_secret`),
`client_device_id`, `workspace_id`, and the critical request proof headers
`x-session-proof-signature`, `x-session-proof-timestamp`,
`x-session-proof-challenge-id`. Trace headers are optional.

Envoy sends method/path/headers/body and peer context in its ext_authz CheckRequest.
ACR applies CORS/CSRF, rate limiting, verified session/local state checks and
consumes the request-bound proof. Denial is returned locally through Envoy;
no Mail mutation occurs. Success rewrites the path to
`/api/v1/tenant/critical/mail/consumers/:id`,
preserving method and JSON body.

ACR removes incoming proof material/verified marker, administrative proof headers
and runtime-assertion headers. It overwrites `x-user-id`, `x-user-name`,
`x-user-level`, `x-tenant-id`, `x-client-device-id`, `x-zone-id` from its
verified context; removes caller `x-workspace-id` and injects the selected workspace
cookie. It injects `x-session-proof-verified=true` and the consumed challenge ID.
Workspace selection is not ownership proof: the repository must check it.
Controlplane runs RequireSessionProof, then permission authorization, before
`TenantConsumerHandler.Delete`.

## Phase 2 — Consumer delete request

The handler validates UUID/context, strict JSON, positive int64 decimal-string
version and bounded reason. `drain_timeout_seconds` is removed from this API
and reserved in the delete protobuf; timeout belongs to Drain.
The service builds `MailConsumerDeleteV1` using the monotonic next config fence.
The repository locks the scoped consumer and checks `hierarchy.tenant_workspaces`: id=workspace, zone_id=Zone, tenant_id=tenant; `hierarchy.tenant_memberships`: actor belongs to that tenant with status=active.
Only current version + drained + no live operation may atomically change
`drained → deleting` and append the sealed delete job. The record still exists.
The database BEFORE DELETE trigger rejects every hard-delete outside deleting.

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

## Phase 4 — DP applies the resource tombstone

The consumer executor requires the matching DRAINING Zone head and typed durable
drain receipt, journal protocol 1 and empty runtime-generation membership. It then
CAS-writes the newer DELETED tombstone. A duplicate matching
tombstone is idempotent. Merely enqueueing or requesting runtime cancellation is
not a deletion success. The generic completion receipt preserves a completed
outcome across result publication retries.

The remaining in-flight message crash recovery gap is described in the
[Tenant drain workflow](tenant_mail_consumer_drain_god_view_workflow.md);
they must be closed before this flow is declared production-ready.

## Phase 5 — JO settles resource-first deletion

Only a valid successful result may remove the tenant row, with predicates
`desired_state='deleting'` and active config_version below the tombstone fence.
The same transaction persists the projection tombstone, stages billing ownership
deletion and settles the generic outbox SUCCEEDED. Failure keeps the resource
record; no saga/rollback/reconciliation is introduced. Contract/infrastructure
errors must never be converted into a guessed successful delete.

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
