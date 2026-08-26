# Hypervisor Resource Plan Publish — Workflow God View

Cost owns the commercial identity of a VM resource bundle. A plan is Global;
Zone-specific price resolution remains the independent Hypervisor pricing
workflow. A plan revision fixes vCPU, memory and boot-disk limits under the
`LIMIT_HOURLY` billing model. It is not a generic platform tier.

## Critical API boundary

- Cost Console reads `GET /api/v1/billing/hypervisor/resource-plans`.
- An operator creates the first revision with `POST /api/v1/billing/critical/hypervisor/resource-plans`.
- An operator publishes a later immutable revision with `POST /api/v1/billing/critical/hypervisor/resource-plans/{plan_id}/revisions`.
- Both mutation routes require the pricing-schedule publish permission and a
  session proof signed over the exact request body. Capacity BIGINT values and
  revision numbers are decimal strings at the HTTP boundary; Cost stores only
  integer columns and normalizes `effective_from` to UTC.

## Durable flow

1. Transport parses IDs/timestamps and decimal strings. The service validates
   the plan invariant (code, bounds, monotonic revision and `LIMIT_HOURLY`).
2. The workflow repository atomically writes the plan/revision, closes the
   prior revision window when appropriate, and inserts the protobuf outbox row.
   PostgreSQL exclusion/OCC fences prevent competing effective windows.
3. The relay claims rows with `FOR UPDATE SKIP LOCKED`, lease and jittered
   retry. It appends the protobuf event to
   `billing.hypervisor.resource-plan.changed.v1`; a crash leaves the outbox
   claimable.
4. Controlplane's isolated projection consumer writes the immutable revision
   locally, including its flat `state` and `allow_create` policy, then warms
   its own typed L2 cache from that durable projection. VM create uses L2 only
   to compose its job and repeats plan/window/hash/**active-create policy** in
   the same CTE as `personal_vms` and the Hypervisor outbox insert. A valid
   non-create or retired event is persisted and fails admission; it is never
   dropped as poison transport input.

## Consumers and failure rules

- Cloud Console lists Cost-owned effective plans, estimates their Zone-adjusted
  hourly/monthly price, sends plan ID plus revision ID, and can only add data
  disks. CPU, memory and boot disk are never client-customizable.
- ACR owns only the shared critical boundary: session proof, verified owner
  selection and trusted headers. It does not know Hypervisor plans or Cost
  cache contracts.
- The Hypervisor service reads its own L2 only as a fast path to compose
  desired state. A miss or malformed entry fails retryably; it never falls
  back to the projection table on the request path. Its CTE is the sole
  admission authority and rechecks revision, effective window and content
  hash in the same transaction.
- VM delete also requires session proof, but never checks current plan status:
  retirement must not strand a previously allocated VM.
- Resize is intentionally outside this workflow and has no route or partial
  contract yet.
