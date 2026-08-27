# Mail global base-price version publish

`POST /api/v1/billing/critical/mail/pricing-schedules/:code/versions`

The transport accepts only decimal-string BIGINT bracket fields and UTC timestamps.
The Mail pricing workflow validates the business rules, uses a Mail-fenced CTE
(`module_code='mail'` and `mail.delivery.accepted_recipient`), persists the immutable
version, brackets and durable outbox record atomically, and emits only its own Mail
cache-invalidation hint. Mail's own outbox loop republishes the Engine fact and
Mail cache hint from Mail-fenced rows. There is no generic cross-module relay.

The version command and bracket commands remain flat rather than nesting entities.

Mail owns its binary L2 snapshot (one-hour TTL), one-minute L1, and module-only
invalidation channel. Its warm-up loop runs every 15 seconds so a deleted or
expired L2 entry does not wait an hour for recovery. Controlplane reads and
validates the Protobuf snapshot directly; there is no JSON readiness stream or
separate readiness projection.
