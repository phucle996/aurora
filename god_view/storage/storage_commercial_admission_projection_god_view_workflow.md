# Storage Commercial Admission Projection — God View

This workflow consumes an owner-scoped commercial decision, projects it inside
Central Storage, resolves Storage-owned bucket topology and atomically stages
per-bucket Zone delivery. Cost never supplies or validates bucket targets.

## Contract and ownership

Input stream: `billing.commercial.admission.storage.changed.v1`, group
`controlplane-storage-commercial-admission-v1`. The payload is the minimal
`CommercialAdmissionChangedV1`: event, owner, policy version, decision, reason
and UTC validity only. Central owner key is `(owner_id, owner_type)`; resource
key is `(resource_id, zone_id)`.

## Phase 1 — Cost relay → Storage stream

Cost maps a committed financial transition to the minimal owner contract,
appends it with Redis durability fencing and keeps retries in its own outbox.
It does not query `storage.personal_buckets`, `storage.tenant_buckets` or emit a
resource target list.

## Phase 2 — Storage stream transport

`transport/stream.CommercialAdmissionProjectionConsumer` bounds and decodes the
protobuf, parses UUID/RFC3339Nano fields and verifies envelope identity. Shape
poison is ACKed; service/repository infrastructure failure stays pending. The
reclaim loop keeps its Redis cursor and also reads new entries every cycle, so
an old infrastructure failure cannot starve a later suspension event.

## Phase 3 — Storage projection service

`service.StorageCommercialAdmissionProjectionService` validates only business
invariants: owner type, positive policy version, decision/reason pairing and
validity ordering. It has no resource selection logic and passes one flat owner
projection to the repository.

## Phase 4 — Owner/resource/outbox transaction

`repository.StorageCommercialAdmissionProjectionRepo.Apply` owns one PostgreSQL
transaction. It monotonic-upserts `storage.commercial_admission_projection`,
reads the fenced winner, then resolves all current personal or tenant buckets
through Storage and Hierarchy durable ownership joins. For each owned bucket it
upserts `storage.resource_admission_projection` and writes
`storage.commercial_admission_zone_outbox` in the same transaction.

The repository calls the narrow `CommercialAdmissionZonePayloadEncoder`; the
Storage-owned protobuf `controlplane.storage.v1.StorageAdmissionChangedV1`
contains exactly one resolved bucket. Times are normalized to UTC. Any SQL or
encoding failure rolls back owner, resource and outbox writes together.

## Failure and security invariants

- Cost cannot choose a bucket ID, name or Zone through this contract.
- Redis is at-least-once; policy-version fences prevent downgrade.
- Missing/expired local state never means allow.
- The owner projection and each immediate bucket/outbox projection commit or
  roll back together.
- Storage-to-Zone delivery belongs to its separate relay workflow.

## Code ownership map

| Boundary | Owner |
|---|---|
| Ingress transport | `controlplane/internal/storage/transport/stream/commercial_admission_projection.go` |
| Business validation | `controlplane/internal/storage/service/commercial_admission_projection_service.go` |
| Owner/resource/outbox transaction | `controlplane/internal/storage/repository/commercial_admission_projection_repo.go` |
| Storage Zone protobuf encoder | `controlplane/internal/storage/transport/proto/commercial_admission_projection_encoder.go` |
| Baseline schema | `controlplane/internal/storage/migrations/000003_commercial_admission.up.sql` |
