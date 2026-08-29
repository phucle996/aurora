# Storage Commercial Admission Zone Relay — God View

This workflow delivers committed Storage admission commands to Dataplane and
projects the resulting decision into the owning Zone KV. It is independent
from Cost ingress and owns only outbox delivery, Zone validation and monotonic
KV settlement.

## Contract and ownership

`storage.storage_outbox_records` with topic
`storage.bucket.commercial_admission` is delivery authority. The protected
payload is `StorageAdmissionChangedV1`; outer job resource and target Zone must
match its `resource_id` and `zone_id`.

## Phase 1 — Generic Storage outbox relay

Job Orchestrator claims the committed Storage outbox row and publishes the
sealed `JobCommandV1` to the exact target Zone with at-least-once delivery. A
publish failure leaves the row retryable; it does not reconstruct admission
state.

## Phase 2 — Dataplane validation

Dataplane decrypts and decodes `StorageAdmissionChangedV1`, then checks outer
resource/Zone equality, UUID owner/event fields, owner type, positive policy
version, `ALLOW`/`SUSPEND_BILLABLE` reason pairing and RFC3339 validity window.
It also reads `AURORA_ZONE_CONFIG/storage.bucket.head.{resource_id}` and requires
exact active resource name, owner ID/type and Zone. A missing head is retryable
because an admission command may overtake initial bucket provisioning; an
already disabled/tombstoned exact head is a terminal no-op because the delete
workflow owns cleanup. Conflict/corruption fails closed.

## Phase 3 — Zone admission KV projection

Dataplane CAS-upserts the same JSON value into two keys:

| KV key | Purpose |
|---|---|
| `AURORA_ZONE_ADMISSION/{resource_id}` | Immutable bucket-ID lookup used by access-session and transfer-ticket paths |
| `AURORA_ZONE_ADMISSION/name/{resource_name}` | Physical-name index used by direct SDK S3 paths |

The exact stored JSON schema is `resource_id`, `resource_name`,
`policy_version`, `decision`, optional `restriction_reason`,
`effective_at_unix_seconds`, optional `valid_until_unix_seconds` and
`source_event_id`. Owner/workspace are deliberately absent: this KV decides
commercial admission only. Resource authority remains in access records or the
runtime head, depending on the consuming workflow.

Both keys use a monotonic `policy_version` fence. An older incoming version is
a no-op only after exact resource ID/name identity matches. Equal-version replay
is success only when the complete JSON value is identical; equal-version data
conflict, name reuse conflict, identity mismatch or corrupt state fails closed.
If one index already contains an identity-equal newer winner, even a stale
delivery repairs the other index to that newer complete value instead of
writing its stale payload. CAS exhaustion is retryable and cannot report
success until both reread to one exact winner.

After both admission indexes converge, Dataplane rechecks the runtime head. If
delete tombstoned the head between precheck and CAS, it revision-deletes both
exact admission keys before returning success. This closes the late-outbox
race without relying on Kafka delivery order.

Dataplane is the only admission KV writer. Zone Control has no admission Kafka
consumer, assignment or alternate outbox; readers never write or repair these
records from another transport.

## Phase 4 — Result settlement

Only after both admission KV indexes settle does Dataplane CAS-create
`AURORA_ZONE_JOB_COMPLETION/job.completion.{job_id}.{delivery_epoch}` as protobuf
`JobCompletionReceiptV1`, then emit success. The receipt schema is exactly
`schema_version=2`, `command_sha256`, `attempt`, `message`, `result_payload`,
`result_payload_schema_version`, `result_status` and optional `error_code`.
It is job replay evidence and contains no admission authority. Job Orchestrator
then marks the exact Storage outbox event terminal; a lost result reuses the
receipt and the same monotonic projection.

## Recovery and security invariants

- A crash after KV success but before PostgreSQL settlement replays the same
  policy version; delivery is explicitly at-least-once.
- A command that overtakes create retries on absent runtime head; a command
  racing or trailing delete cannot recreate admission after the tombstone.
- Zone comes from the durable row, never request input.
- Resource failures do not block unrelated rows.
- Relay code cannot query Cost or reconstruct a decision.
- Missing, corrupt or non-current admission fails closed in every reader.
- Successful bucket deletion owns cleanup: its Dataplane phase identity-checks
  and revision-deletes the name key then the ID key. This relay never guesses
  deletion from an admission event.

## Code ownership map

| Boundary | Owner |
|---|---|
| Central outbox writer | `controlplane/internal/storage/repository/commercial_admission_projection_repo.go` |
| Generic relay | `job-orchestrator/src/changefeed/dispatch.rs` |
| Zone executor | `dataplane/src/executor/storage/commercial_admission.rs` |
| Zone readers | `zone-control-edge-gateway/authorizer/src/zone_access.rs`, `zone-public-edge-gateway/authorizer/src/storage_access.rs` |
