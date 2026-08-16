# Storage Commercial Admission Reconcile — God View

This recovery workflow projects the already-committed owner admission decision
onto Storage buckets created or reassigned after the original ingress event. It
does not consume Redis, change the financial decision or publish Kafka directly.

## Contract and ownership

Billing PostgreSQL is not queried. Authority is the pair of Storage durable
facts: `commercial_admission_projection` for the owner decision and current
Storage/Hierarchy bucket ownership. Output is a resource projection plus a
Storage Zone outbox row in the same transaction.

## Phase 1 — Bounded worker trigger

`transport/worker.CommercialAdmissionReconcile` wakes every 30 seconds and
drains bounded batches until the repository returns zero. A failed batch stops
that pass and is retried at the next tick. The worker contains no SQL or policy
logic.

## Phase 2 — Service batch contract

`StorageCommercialAdmissionReconcileService` owns a fixed batch size of 100 and
invokes only its dedicated repository port. There is no transport input to
validate and no dependency on the ingress projection service.

## Phase 3 — CTE authority and atomic projection

The repository CTE selects at most 100 personal/tenant buckets whose local
resource projection is missing, behind the owner policy version or belongs to a
different durable owner. For each candidate the mutation CTE rechecks current
durable ownership before upsert. The Storage-owned per-bucket protobuf and Zone
outbox row are written in the same bounded PostgreSQL transaction.

## Failure and recovery rules

- Owner decision is copied, never synthesized or downgraded.
- A stale candidate that fails the mutation-time ownership recheck is skipped.
- SQL or encoding failure rolls back the whole bounded batch.
- More than 100 candidates are drained across independent bounded transactions.
- Zone publication remains owned by the separate Zone relay workflow.

## Code map

- `controlplane/internal/storage/transport/worker/commercial_admission_reconcile.go`
- `controlplane/internal/storage/service/commercial_admission_reconcile_service.go`
- `controlplane/internal/storage/repository/commercial_admission_reconcile_repo.go`
