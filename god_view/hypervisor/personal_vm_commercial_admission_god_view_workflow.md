# Personal Hypervisor VM Commercial Admission — God View

This workflow projects a committed owner-scoped commercial decision into the
Hypervisor module and fails VM creation closed. Cost owns the financial
derivation. Hypervisor owns its rebuildable local decision projection and does
not know wallet identity, balance, ledger state or another module's resources.

## Contract and ownership

Cost emits `billing.admission.v1.CommercialAdmissionChangedV1` to
`billing.commercial.admission.hypervisor.changed.v1`. The payload contains only
`event_id`, owner identity/type, monotonic `policy_version`, `decision`, reason
and UTC validity. It contains no wallet ID and no resource targets.

The Hypervisor projection key is `(owner_id, owner_type)`. Only an effective,
unexpired `ALLOW` permits a personal VM create. Missing, stale, unavailable or
`SUSPEND_BILLABLE` state fails closed. Delete remains allowed.

## Phase 1 — Cost outbox → module stream

The Cost relay claims a committed wallet-policy outbox row, maps it to the
minimal commercial contract and appends it independently to Storage,
Hypervisor and Mail streams. Each append is followed by `WAITAOF`. Cost marks
the outbox row published only after every module append is durable. Partial
publication retries and may duplicate a stream; event identity and policy
version make replay safe.

## Phase 2 — Hypervisor stream transport

`transport/stream.CommercialAdmissionProjectionConsumer` owns the consumer
group, bounded protobuf decoding, UUID/time parsing and envelope/event identity
validation. Invalid transport poison is ACKed and deleted. A typed flat command
is the only input to service. Infrastructure failure remains in the PEL.

## Phase 3 — Hypervisor projection service

The transport validates positive policy version, owner type, decision/reason
pairing and validity ordering as part of the wire contract.
`service.HypervisorCommercialAdmissionProjectionService` maps the validated
command to the module-owned flat projection without repeating those checks.

## Phase 4 — Hypervisor projection repository

`repository.HypervisorCommercialAdmissionProjectionRepo` monotonically upserts
`hypervisor.commercial_admission_projection`. Duplicate/lower policy versions
settle as idempotent no-ops. PostgreSQL failure prevents stream settlement.

## Phase 5 — Personal VM create gate

After ACR/session authorization and the L2 resource-plan fast path, the VM create
repository checks commercial admission in the same CTE as owner, workspace,
Zone, image and resource-plan authority. A denied admission creates no VM,
provider command or allocation event. There is no separate service-side
commercial-admission read that could race the mutation.

## Recovery and security invariants

- Cost's committed outbox is source delivery authority; the Hypervisor table is
  rebuildable.
- UI/SDK input cannot supply owner decision, policy version or financial facts.
- The commercial contract cannot describe a VM, bucket, mail consumer or Zone.
- Module streams, commands, services and repositories remain independent.

## Code ownership map

| Boundary | Owner |
|---|---|
| Redis/protobuf/ACK/PEL | `controlplane/internal/hypervisor/transport/stream/commercial_admission_projection.go` |
| Business validation | `controlplane/internal/hypervisor/service/commercial_admission_projection_service.go` |
| Monotonic projection | `controlplane/internal/hypervisor/repository/commercial_admission_projection_repo.go` |
| Flat command/projection | `controlplane/internal/hypervisor/domain/entity/commercial_admission_projection.go` |
| Minimal wire contract | `proto/cost-manager/api/billing/admission/v1/commercial_admission.proto` |
