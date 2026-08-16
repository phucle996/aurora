# Billing → Mail Pricing Readiness Projection — God View

This workflow prevents a Mail consumer from entering `ENABLED` when accepted
recipient evidence cannot be rated. Billing PostgreSQL is authority; Mail owns
the local projection used by resume workflows.

## Contract and ownership

Cost resolves `mail.delivery.accepted_recipient`, requiring module `mail`, raw
unit `RECIPIENT`, valid immutable brackets and checksum. Every 15 seconds it
emits bounded JSON to `billing.pricing.mail.rateability.changed.v1` with schema,
ready/missing, UTC observation/expiry and SHA-256 fingerprint. The payload has
no owner, wallet, consumer, multiplier or price amount.

## Phase 1 — Cost computes and durably publishes

Cost reads the active Global snapshot, builds the flat payload, `XADD`s the Mail
stream and executes `WAITAOF`. It never writes a key read directly by
Controlplane. Missing Zone adjustment is valid Global `1/1` inheritance.

## Phase 2 — Mail stream transport

`transport/stream.PricingReadinessProjectionConsumer` owns group lifecycle,
bounded JSON shape, timestamp parsing and fingerprint decoding. Poison settles.
Infrastructure failure stays pending for reclaim/retry.

## Phase 3 — Mail local projection

`MailPricingReadinessProjectionService` validates business consistency and
validity window. `MailPricingReadinessProjectionRepo` applies an observed-time
fence and writes `controlplane:mail:pricing-readiness:v1` with expiry. Duplicate
or older events cannot downgrade the local winner.

## Phase 4 — Personal/Tenant resume gates

`MailPricingReadinessGateService` reads only the Mail-owned projection after the
corresponding owner commercial-admission gate and before consumer/outbox
mutation. Missing, expired or `ready=false` returns
`MAIL_PRICING_UNAVAILABLE`. Create remains paused; pause/delete remain allowed.

## Recovery invariants

- Cost outage expires the local projection within 45 seconds.
- Repository failure leaves the stream event pending.
- Transport poison never becomes a local projection.
- UI and SDK share the same server-side gate.

## Code map

- `cost-manager/api/internal/service/mail_estimate_service.go`
- `controlplane/internal/mail/transport/stream/pricing_readiness_projection.go`
- `controlplane/internal/mail/service/pricing_readiness_projection_service.go`
- `controlplane/internal/mail/repository/pricing_readiness_projection_repo.go`
- `controlplane/internal/mail/service/personal_consumer_service_impl.go`
- `controlplane/internal/mail/service/tenant_consumer_service_impl.go`
