# Hypervisor VM Allocation Lifecycle Projection — God View

This background workflow converts a terminal, durable Hypervisor VM mutation
into the allocation and ownership facts consumed by Billing. It has no browser
request and therefore no ACR phase. Controlplane PostgreSQL remains the VM
business Source of Truth; Billing PostgreSQL holds only rebuildable ownership
and allocation projections.

Runtime telemetry and VM power state are outside this workflow. A READY VM
continues to consume its selected CPU, memory, GPU and disk allocation while it
exists, including while it is stopped or rebooting.

## Scope and authority

| Item | Contract |
| --- | --- |
| Workflow owner | Controlplane Hypervisor result settlement, then JO Hypervisor allocation-export relay |
| Activation trigger | `hypervisor.vm.create` terminal `SUCCEEDED` settled as VM `READY` |
| Revision trigger | A future resize command terminal provider success |
| Termination trigger | `hypervisor.vm.delete` terminal provider success |
| Lifecycle SoT | Hypervisor PostgreSQL VM/outbox transaction |
| Transport | Shared Redis streams with PostgreSQL retry marker and WAITAOF fence |
| Allocation consumer | Cost Engine Hypervisor lifecycle consumer |
| Payer authority | Billing ownership projection; allocation payload is never payer authority |
| Power state | Never changes allocation billing |

## Phase 1 — Hypervisor result transaction creates a durable allocation fact

The VM command remains asynchronous. Create never synchronously calls Cost.
When JO accepts the signed Zone result, it locks the authoritative VM and
outbox. A valid create success changes the VM to READY, marks the provider
command SUCCEEDED and appends one flat `hypervisor_allocation_outbox` row in
one transaction. The allocation row is the durable export authority; the
provider command outbox carries no subscriber delivery state. Failed create
retains the failed VM record and appends no allocation fact.

```mermaid
sequenceDiagram
    participant K as Kafka jobs.results.v1
    participant J as Job Orchestrator result worker
    participant H as Hypervisor PostgreSQL
    K-->>J: terminal VM result
    J->>H: lock VM and command outbox
    alt valid create success
        J->>H: VM -> READY; command -> SUCCEEDED; append ACTIVATE fact
        H-->>J: committed durable allocation export
    else failed or invalid
        J->>H: retain failed VM or quarantine result
        H-->>J: no allocation activation
    end
```

The activation boundary is the first durable DP observation after the requested VM
configuration/start is confirmed. DP persists it before guest-agent polling and returns
`provider_completed_at_unix_ms`; JO uses it for `provisioned_at` and ACTIVATE, not
its processing time. Delete returns the Proxmox task end time, journaled in Zone KV,
for TERMINATE. The generic outbox `completed_at` remains JO processing time.
Missing deletion evidence is an unknown outcome, not a guessed timestamp.
DP now records a pre-submit task baseline and uses a bounded, exact-scope provider
history lookup to recover a lost DELETE ACK. This requires retained task history
and an exclusive Aurora provider principal; ambiguous/missing evidence remains
unknown rather than an invented allocation termination time.

## Phase 2 — JO relays ownership and allocation facts

The independently restartable Hypervisor allocation-export relay claims only
committed `hypervisor_allocation_outbox` rows whose export marker is
unpublished. Every row persists a positive `source_version`; a unique index on
`(resource_id,source_version)` is the durable order fence. The row already
contains the immutable allocation captured by result settlement, so the relay
does not re-read a live VM that may later be deleted. It claims only the
earliest unpublished version per resource and emits two separate facts:

- `ResourceOwnershipChangedV1` to the existing ownership stream;
- `HypervisorAllocationChangedV1` to the Hypervisor allocation stream.

Both use deterministic event IDs derived from the source operation. The relay
marks the allocation-export row published only after both Redis writes pass WAITAOF.
A crash between either write and the marker update causes duplicates, which
the two consumer inboxes absorb by event ID and payload hash.

```mermaid
sequenceDiagram
    participant H as Hypervisor allocation export outbox
    participant J as JO Hypervisor allocation relay
    participant O as Shared Redis ownership stream
    participant A as Shared Redis allocation stream
    J->>H: claim earliest unpublished fact with lease
    H-->>J: immutable resource, owner, Zone, limits, effective_at
    J->>O: XADD deterministic ownership event; WAITAOF
    J->>A: XADD deterministic allocation event; WAITAOF
    alt both durable
        J->>H: mark published_at
    else any write/fence fails
        J->>H: release claim and retain bounded error
    end
```

The allocation payload carries resource UUID, Zone UUID, source version,
effective UTC timestamp, CPU cores, memory MiB, disk GiB and optional GPU
SKU/count. It carries no wallet balance, price, cookie, credential or provider
secret. GPU fields must remain empty/zero until the VM provisioning workflow
can enforce that allocation.

## Phase 3 — Cost Engine applies a flat allocation projection

The Cost Engine consumer validates the Redis envelope and canonical protobuf,
then applies one transaction in Billing PostgreSQL. An event inbox protects
event identity; a per-resource head protects monotonic source version; flat
allocation intervals preserve history.

```mermaid
sequenceDiagram
    participant A as Shared Redis allocation stream
    participant E as Cost Engine lifecycle consumer
    participant B as Billing PostgreSQL
    A-->>E: HypervisorAllocationChangedV1
    E->>E: validate UUIDs, bounds, timestamp and payload hash
    E->>B: insert/lock event inbox and allocation head
    alt ACTIVATE
        E->>B: insert open allocation interval version 1
    else REVISE
        E->>B: close current interval; insert next interval
    else TERMINATE
        E->>B: close current interval; mark head TERMINATED
    end
    B-->>E: commit
    E->>A: XACK then XDEL
```

Revision gaps and a termination whose predecessor has not applied remain in
the Redis PEL for retry. Conflicting event hashes, a second activation or an
invalid transition at an already-present head are quarantined. A lower or
duplicate version with the same event evidence is idempotent. This distinction
prevents concurrent consumers from permanently losing a valid delete event.

## Failure and security rules

| Failure | Behavior |
| --- | --- |
| Failed create | No ownership or allocation activation |
| Redis unavailable | Hypervisor allocation export remains pending |
| Crash after XADD before marker | Duplicate relay; consumer inbox absorbs it |
| Allocation arrives before ownership | Allocation projection may apply; hourly settlement remains UNRATED until ownership exists |
| Out-of-order source version | Do not overwrite head or close the wrong interval |
| Unknown GPU SKU/nonzero unsupported GPU | Reject contract; never charge an unenforced limit |
| Power off/reboot | No lifecycle billing event |
| Provider delete outcome unknown | Retry exact command; do not fabricate FAILED or a billing termination timestamp |
| Secret/owner spoofing | Payload is built only from locked Hypervisor rows, never client or provider names |

## Code map

- `controlplane/internal/hypervisor/migrations/000002_hypervisor_tables.up.sql`
- `job-orchestrator/src/results/hypervisor/vm.rs`
- `job-orchestrator/src/outbox/hypervisor_allocation.rs`
- `proto/cost-manager/engine/hypervisor_allocation_event.proto`
- `cost-manager/engine/src/service/hypervisor/allocation_lifecycle.rs`
- `cost-manager/api/migrations/000004_tables_settlement.up.sql`
