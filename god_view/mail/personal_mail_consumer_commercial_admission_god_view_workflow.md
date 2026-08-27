# Personal Mail Consumer Commercial Admission — God View

This Personal workflow is isolated from Tenant admission and gates only Personal
consumer resume.

## API-scope contract

Browser or SDK sends neutral `POST /api/v1/mail/consumers/:id/resume` with
`{"expected_config_version":N}`. ACR verifies user session and route proof,
removes spoofed authority headers, selects the Personal branch and injects
trusted authority. Controlplane checks `email:consumer:update` at the required
role level; repository CTEs recheck durable Personal workspace ownership.

## Phase 1 — Cost outbox → Mail stream

The committed owner-scoped `CommercialAdmissionChangedV1` is durably appended
to `billing.commercial.admission.mail.changed.v1`. Mail transport owns ACK/PEL
and wire validation; service owns business invariants; repository owns the
monotonic local projection. There is no wallet ID or resource list on the wire.

## Phase 2 — Personal resume gate

Personal resume requires effective, unexpired `ALLOW` keyed by trusted
`(user_id, PERSONAL)`. Repository CTEs atomically check commercial admission
projection during the update mutation. It never uses any tenant decision. Failure
returns `ErrCommercialAdmissionUnavailable` (HTTP 503) without mutating consumer
state or creating an outbox record. Pause and delete remain available.

## Recovery and security invariants

- Cost outbox is replay authority; Mail projection is rebuildable.
- Missing state never means allow.
- SDK/UI cannot provide a commercial decision.
- Personal and Tenant API workflows retain separate authority branches.

## Code map

- `controlplane/internal/mail/transport/stream/commercial_admission_projection.go`
- `controlplane/internal/mail/service/commercial_admission_projection_service.go`
- `controlplane/internal/mail/repository/commercial_admission_projection_repo.go`
- `controlplane/internal/mail/repository/personal_consumer_repo_impl.go`
- `controlplane/internal/mail/service/personal_consumer_service_impl.go`
