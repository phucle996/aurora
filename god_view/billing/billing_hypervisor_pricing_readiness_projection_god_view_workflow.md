# Billing → Hypervisor Pricing Readiness Projection — God View

This background workflow prevents VM creation when Hypervisor allocations
cannot be rated. Billing PostgreSQL is pricing authority. Cost publishes a
durable fact stream; Hypervisor owns the projection used by its create gate.

## Contract and ownership

Every 15 seconds Cost resolves the five required Global charge kinds: allocated
vCPU, memory and disk, plus network in/out. Each snapshot must be effective,
owned by module `hypervisor`, valid and checksum-correct. GPU stays excluded
while provider enforcement is disabled.

Cost emits bounded JSON to
`billing.pricing.hypervisor.rateability.changed.v1`. The payload contains schema
version, ready flag, missing kinds, UTC observation/expiry and a SHA-256
fingerprint. It contains no price amount, wallet, owner, VM or Zone adjustment.

## Phase 1 — Cost computes and durably publishes

Cost resolves snapshots from Billing PostgreSQL, computes the flat payload,
`XADD`s the module stream and executes `WAITAOF`. It does not write a
Controlplane cache key. Absence of a Zone adjustment means Global inheritance
`1/1` and does not make pricing unready.

## Phase 2 — Hypervisor stream transport

`transport/stream.PricingReadinessProjectionConsumer` owns consumer-group
lifecycle, bounded JSON decoding, timestamp parsing and fingerprint wire shape.
Transport poison is ACKed. Service/repository infrastructure failure remains in
the PEL and is reclaimed after the lease interval.

## Phase 3 — Hypervisor projection service/repository

`HypervisorPricingReadinessProjectionService` validates schema, ready/missing
consistency and the maximum one-minute validity window. Expired replay is a
successful no-op. The module-local Redis repository atomically fences by
`observed_at`, then writes
`controlplane:hypervisor:pricing-readiness:v1` with TTL through one Lua
operation. An older replay cannot replace a newer projection.

## Phase 4 — Personal VM create gate

`HypervisorPricingReadinessGateService` reads only the Hypervisor-owned local
projection. Missing, expired or `ready=false` returns
`HYPERVISOR_PRICING_UNAVAILABLE` before image lookup or VM/outbox mutation.
Commercial admission is a separate projection and must also allow the owner.

## Failure and recovery

| Failure | Behavior |
|---|---|
| Required Global schedule missing | Cost emits `ready=false` with exact missing kind |
| Cost unavailable | Local TTL expires; new VM create fails closed |
| Projection repository unavailable | Stream item stays pending and is reclaimed |
| Duplicate/older event | Observed-time fence makes it a no-op |
| Malformed event | Transport settles poison; prior projection expires normally |
| Existing VM | Delete remains available; runtime billing recovery is independent |

## Code map

- `cost-manager/api/internal/service/hypervisor_estimate_service.go`
- `controlplane/internal/hypervisor/transport/stream/pricing_readiness_projection.go`
- `controlplane/internal/hypervisor/service/pricing_readiness_projection_service.go`
- `controlplane/internal/hypervisor/repository/pricing_readiness_projection_repo.go`
- `controlplane/internal/hypervisor/service/personal_vm_service.go`
