# Storage Commercial Admission Zone Relay — God View

This workflow delivers committed Storage admission outbox rows to the owning
Zone. It is independent from ingress projection and owns only claim, publish,
retry and settlement.

## Contract and ownership

`storage.commercial_admission_zone_outbox` is delivery authority. A row is
keyed by `(resource_id, zone_id)` and contains the exact Storage-owned protobuf,
source event and policy version. Destination is
`{topic_prefix}.storage.commercial.admission.{zone_id}.v1`; record key is
`{source_event_id}:{resource_id}`.

## Phase 1 — Worker trigger

`transport/worker.CommercialAdmissionZoneRelay` owns lifecycle and a one-second
trigger. It invokes batches until no work remains. It contains no SQL, Kafka or
decision logic.

## Phase 2 — Service orchestration

`service.StorageCommercialAdmissionZoneRelayService` creates one claim token,
claims at most 100 rows and invokes the outbound publisher once per flat
delivery. A bounded publish error releases only that row; other rows remain
isolated. Successful publish is followed by durable settlement.

## Phase 3 — Repository lease

`repository.CommercialAdmissionZoneRelayRepo` claims with
`FOR UPDATE SKIP LOCKED`; leases expire after one minute. `Release` increments
retry state. `MarkPublished` succeeds only when resource, Zone, policy version
and claim token still match, so stale settlement cannot mark a replacement.

## Phase 4 — Kafka transport

`transport/kafka.CommercialAdmissionZonePublisher` builds topic/key from the
typed delivery. Before the first publish to a Zone, it idempotently provisions
the six-partition destination with a 30-day retention policy and caches only a
successful result. Missing Kafka create-topic authority leaves the PostgreSQL
outbox pending. It publishes stored bytes unchanged with `acks=all` and never
decodes or rewrites policy content.

## Recovery and security invariants

- A crash after Kafka success but before PostgreSQL settlement replays after
  lease expiry; delivery is explicitly at-least-once.
- Zone comes from the durable row, never request input.
- Resource failures do not block unrelated rows.
- Relay code cannot query Cost or reconstruct a decision.
- Zone decodes `controlplane.storage.v1.StorageAdmissionChangedV1`; its KV uses
  Storage-owned `policy_version` and `decision` fields.

## Code ownership map

| Boundary | Owner |
|---|---|
| Worker | `controlplane/internal/storage/transport/worker/commercial_admission_zone_relay.go` |
| Service | `controlplane/internal/storage/service/commercial_admission_zone_relay_service.go` |
| Lease/settlement repo | `controlplane/internal/storage/repository/commercial_admission_zone_relay_repo.go` |
| Kafka publisher | `controlplane/internal/storage/transport/kafka/commercial_admission_zone_publisher.go` |
| Outbox schema | `controlplane/internal/storage/migrations/000003_commercial_admission.up.sql` |
