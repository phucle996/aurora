# Personal Hypervisor VM Delete — God View

This workflow deletes one personal VM and closes its billable allocation at
the instant the Zone provider confirms deletion. Controlplane PostgreSQL owns
the requested lifecycle transition, the Dataplane owns the Proxmox mutation,
and Billing PostgreSQL owns the resulting allocation interval and charges.
Power state and UI runtime telemetry never decide whether billing is active.

## API-scope contract

This is a platform-owned personal workflow. The browser/SDK calls the neutral
public route `DELETE /api/v1/critical/hypervisor/vms/{vm_id}`. ACR consumes a
session proof for this exact empty-body request, then chooses the internal
`/api/v1/personal` branch only from the verified session, removes caller
authority headers, and injects the verified user, Zone and workspace. The
Controlplane authorizer requires `hypervisor:vm:delete`; the repository then
rechecks the durable personal workspace owner and immutable VM Zone.

The request has no JSON body. A successful first request returns `202` with
the VM and operation identifiers. A replay while deletion is pending returns
the same operation. `404` does not reveal a VM outside the verified owner and
workspace. Delete does not accept provider node, provider VMID, payer, Zone,
allocation limits or an effective billing timestamp from the caller.

## Phase 1 — Client → Envoy → ACR

Envoy sends `DELETE`, the neutral path, cookies, `Origin`, request headers and
an empty body in its ext_authz `CheckRequest`. ACR runs CORS and pre/post-auth
rate limits, verifies the IAM session and CSRF rules for the mutation, rejects
direct `/personal` routes, removes caller `x-user-id`, `x-tenant-id`,
`x-zone-id` and `x-workspace-id`, then overwrites identity/Zone from the
verified session and workspace from the signed workspace cookie. It rewrites
the path exactly to `/api/v1/personal/critical/hypervisor/vms/{vm_id}` and Envoy forwards
the unchanged method and empty body. Delete deliberately skips resource-plan
L2 so a retired plan can never block cleanup. Any failure is a local 401/403/429/503;
there is no Controlplane call.

## Phase 2 — Controlplane delete command transaction

The shared personal VM HTTP handler owns create/read/delete transport methods;
delete is not wired through a separate handler branch. Its delete method parses
only the path VM UUID and trusted context. Authorization runs before the
repository. One CTE locks the VM through its personal
workspace owner, accepts only `READY` or the same pending delete, creates one
`hypervisor.vm.delete` outbox command with the immutable provider identity,
and marks the VM `DELETING`. The command outbox carries only command/result
authority and contains no subscriber-specific delivery markers. The protobuf
payload is envelope-encrypted for its Zone.

The request transaction never closes billing and never deletes the row before
the provider result. A pending create cannot be deleted because its provider
identity is not yet authoritative. Delete command dùng generated type từ
Controlplane transport root duy nhất `transport/proto/hypervisor`; không có
nhánh `transport/rpc` riêng.

## Phase 3 — Job Orchestrator → Kafka → Dataplane → Proxmox

The normal Hypervisor outbox relay publishes the encrypted command only to the
row's immutable Zone. Dataplane validates schema, job resource ID, VM UUID,
provider name and positive provider VMID. It discovers that exact Aurora VM;
a same-name/different-VMID or same-VMID/different-name collision fails
permanently. Missing VM is success only with durable provider completion evidence.
Otherwise it acquires the
Hypervisor mutation permit, stops the VM when necessary, waits for the task,
reads the final cumulative `netin`/`netout`, and CAS-persists that terminal
observation into the flat per-VM Zone KV metering cursor. A failed final
observation or cursor CAS makes delete retry before purge. Before DELETE, it
persists a flat `VmDeleteJournalV1` in Zone KV with immutable VM identity, node,
and the observed baseline of deletion tasks owned by the configured provider
principal. It then deletes the VM with disk purge and journals the returned UPID.
An exact-command retry queries provider task status, reads inventory again and
requires both the immutable name and VMID to be absent before it returns
`VmDeleteResultV1`. A still-present matching VM retries; an identity collision
fails permanently. Provider response bodies never cross the trust boundary.

`JobExecutionRuntime` registers this command and its Kafka delivery with the
execution watchdog. Its ten-second scans renew a 30-second fenced Zone KV lease;
the execution deadline is separate and is not extended by renewal. A deadline
cancels the local future, not a provider task already accepted. The watchdog
therefore sends the same job ID/version/attempt/delivery epoch and ciphertext
directly to the existing retry scheduler after 30–32 seconds (lease TTL + jitter),
never to JO as a terminal `FAILED` result. The VM remains `DELETING`; replay checks
the same VM identity and continues deletion. Absence without a provider timestamp
is still an unknown outcome, never an invented completion time.

The scheduler waits for Kafka retry-publish ACK before settling the source under
its assignment fence. Crash before ACK/settlement replays the original delivery.
Timeout recovery does not increment the business attempt or consume its budget.
The watchdog retains backpressured retries in a bounded queue without blocking
lease renewals. Overflow exits this critical task to trigger process restart and
uncommitted-source replay. Completion-phase executions cannot be timed out by a
stale watchdog snapshot. No Saga, rollback or independent reconciliation is added.
Retry-publish failure exits the critical scheduler for supervised restart and
source replay; merely leaving the offset uncommitted would not redeliver it in
the same assignment. A stale source ACK after a successful retry publish remains
fenced without forcing another restart solely for rebalance.

## Phase 4 — Result settlement and client notification

Job Orchestrator verifies source domain, topic, job version and the durable
outbox fence. VM create and delete settlement share the VM result owner module;
delete is not a detached result branch. `PROCESSING` preserves `DELETING`. JO
accepts `SUCCEEDED` only with the immutable provider identity carried by the
durable delete command after Dataplane has waited for provider deletion. Its
settlement CTE first deletes the matching `DELETING` row from `personal_vms`;
the outbox can become terminal only when that delete returned the VM identity.
Both changes commit in one transaction. PostgreSQL additionally rejects every
direct or future `personal_vms` delete whose `OLD.status` is not `DELETING`, so
the lifecycle fence cannot be bypassed by another repository path. `FAILED`
settles the job error but retains the VM in `DELETING` and keeps its allocation
active; it cannot manufacture READY after a potentially partial provider mutation.
Duplicate terminal
results are no-ops.

Only after that transaction commits does the existing notification path
enqueue the stable job event. Enqueue failure leaves the result Kafka offset
unsettled so replay reconstructs the event from the terminal outbox row.
Notification Service idempotently persists both the user activity timeline and
notification inbox in Scylla before publishing Centrifugo; its Redis entry is
ACKed only after that sequence succeeds. The realtime event is a wake-up
signal, never deletion authority; clients refetch the neutral VM list/detail
surface and observe the committed absence of the VM.

## Phase 5 — Durable allocation termination export

The successful delete settlement CTE captures the locked VM owner, Zone and
provider-confirmed completion time before deleting the VM row, computes the
next positive allocation source version and appends one flat `TERMINATE` row
to `hypervisor_allocation_outbox` in the same transaction. The dedicated
Hypervisor allocation relay builds deterministic `RESOURCE_DELETED` and
allocation `TERMINATE` events from that immutable row. It publishes ownership
first and allocation second through Redis Streams plus the configured
durability fence, then marks the allocation row `published_at`. Crash or Redis
failure leaves the row claimable. The relay cannot claim it while an earlier
version for the same resource remains unpublished. Cost Engine
closes the open allocation interval at that UTC instant, and hourly settlement
bills only its intersection with closed hourly windows. A predecessor gap is
retryable rather than poison/DLQ. No VM runtime sample is consulted.

## Failure and recovery invariants

- Controlplane commit without Kafka publish is recovered from the outbox.
- A lost/duplicate Dataplane result is recovered/replayed under the same job
  fence; provider deletion is idempotent by immutable identity.
- A watchdog deadline is unknown outcome, not provider failure: retry the same
  delete operation and keep `DELETING` until an actual result is confirmed.
- Provider failure retains `DELETING` and the job error is available through the
  existing notification/timeline path. Billing termination requires actual evidence.
- Final network evidence failure prevents provider purge; a bounded rotating
  cursor scan publishes closed windows even after the VM leaves inventory.
- Result commit without allocation delivery is recovered from the separate
  allocation outbox row; the VM row is not required.
- Billing termination can never precede provider-confirmed deletion.
- Centrifugo notification can never precede the committed VM-row deletion.
- A VM-delete result notification is replayed until Notification Service can
  persist the stable user timeline/inbox event and publish realtime.
- Reusing the old VM name is possible only after the provider result removes
  the old row; allocation/resource UUIDs never reuse identity.

## Durable completion revision — 2026-08-27

DP persists intent before DELETE and task/completion evidence using journal CAS.
If the HTTP ACK is lost, the same command performs a bounded lookup of active and
archived `qmdestroy` tasks for the journaled node, VMID and provider principal,
excluding baseline/known-failed UPIDs. Only one matching new task can be adopted;
ambiguous matches, incomplete history or provider errors remain unknown.
Deployment must keep that principal exclusive to Aurora mutations and retain
provider task history through recovery. History loss or an uncorrelatable outside
mutation is not automatically recoverable and must never generate a timestamp.

Running tasks remain exact-command retries. Confirmed failed tasks are recorded
and removed from the current task slot before a bounded next provider attempt;
retries no longer loop forever on a permanently failed UPID. Success persists the
provider end timestamp before JO settlement. VmDeleteResultV1 carries that UTC
instant for TERMINATE, not result arrival time. Legacy task/timestamp evidence is
still read. No reconciliation or rollback workflow is added.

Generic terminal receipts live in `AURORA_ZONE_JOB_COMPLETION`, with history 1,
file storage, Zone replicas, no TTL and a 512 MiB discard-new quota. Reads still
honor legacy config-bucket receipts. Terminal settlement acknowledgement and
transport-enforced replay retirement are not implemented yet, so automatic receipt
GC remains disabled; quota exhaustion fails closed rather than evicting evidence.
Receipt schema 2 preserves both SUCCEEDED and FAILED, including the error code;
unknown outcomes never become terminal receipts. Schema-1 success receipts remain
readable. Quiesce old DP binaries before switching writers: mixed old/new writers
cannot share this new receipt bucket safely merely through legacy read fallback.
