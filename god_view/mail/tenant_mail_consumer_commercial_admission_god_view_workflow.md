# Tenant Mail Consumer Commercial Admission — God View

This Tenant workflow is isolated from Personal admission and gates only consumer
resume inside the verified Tenant range.

## API-scope contract

Browser or SDK sends neutral `POST /api/v1/mail/consumers/:id/resume` with
`{"expected_config_version":N}`. ACR verifies the user session, route proof and
Tenant membership, removes spoofed authority headers, selects the Tenant branch
and injects trusted authority. Controlplane checks `email:consumer:update` at the
required Tenant role level; the repository CTE rechecks durable membership,
workspace and Tenant ownership.

## Phase 1 — Cost outbox → Mail stream

The committed owner-scoped `CommercialAdmissionChangedV1` is durably appended
to `billing.commercial.admission.mail.changed.v1`. Mail transport owns ACK/PEL
and wire validation; service owns business invariants; repository owns the
monotonic local projection. The wire carries no wallet ID or resource list.

## Phase 2 — Tenant resume gate

Tenant resume requires an effective, unexpired `ALLOW` keyed by trusted
`(tenant_id, TENANT)`. The Tenant repository CTE checks that projection inside
the update mutation. It never reads a Personal decision or falls back to the
actor's platform authority. Failure returns
`ErrCommercialAdmissionUnavailable` (HTTP 503) without changing consumer state
or creating an outbox record. Pause and delete remain available.

## Recovery and security invariants

- Cost outbox is replay authority; the Mail projection is rebuildable.
- Missing state never means allow.
- SDK/UI cannot provide a commercial decision or owner scope.
- Tenant membership and ownership are rechecked at the durable mutation boundary.
- Personal and Tenant admission decisions cannot substitute for each other.

## Code map

- `controlplane/internal/mail/transport/stream/commercial_admission_projection.go`
- `controlplane/internal/mail/service/commercial_admission_projection_service.go`
- `controlplane/internal/mail/repository/commercial_admission_projection_repo.go`
- `controlplane/internal/mail/repository/tenant_consumer_repo_impl.go`
- `controlplane/internal/mail/service/tenant_consumer_service_impl.go`
