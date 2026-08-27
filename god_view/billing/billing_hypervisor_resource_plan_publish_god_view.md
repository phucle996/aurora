# Hypervisor Resource Plan Publish — Workflow God View

Cost owns the commercial identity of a VM resource bundle. A plan is Global;
Zone-specific price resolution remains the independent Hypervisor pricing
workflow. A plan revision fixes vCPU, memory and boot-disk limits under the
`LIMIT_HOURLY` billing model. It is not a generic platform tier.

## Critical API boundary

- Cost Console reads `GET /api/v1/billing/hypervisor/resource-plans`.
- A user with the required personal platform permission creates the first revision with `POST /api/v1/billing/critical/hypervisor/resource-plans`.
- That permission also authorizes publishing a later immutable revision with `POST /api/v1/billing/critical/hypervisor/resource-plans/{plan_id}/revisions`.
- Both mutation routes require the pricing-schedule publish permission and a
  session proof signed over the exact request body. Capacity BIGINT values and
  revision numbers are decimal strings at the HTTP boundary; Cost stores only
  integer columns and normalizes `effective_from` to UTC.

## Durable flow

1. Transport parses IDs/timestamps and decimal strings. Transport also rejects missing/null/zero times, trailing JSON, malformed decimal strings
   and oversized or empty required text. The service checks capacity business bounds (boot disk <=65536 GiB);
   revision OCC and monotonic effective times are checked at the repository durable boundary.
2. The workflow repository atomically writes the plan/revision, closes the
   prior revision window when appropriate, and inserts the protobuf outbox row.
   PostgreSQL exclusion/OCC fences prevent competing effective windows.
3. The relay claims rows with `FOR UPDATE SKIP LOCKED`, lease and jittered
   retry. It appends the protobuf event to
   `billing.hypervisor.resource-plan.changed.v1`; a crash leaves the outbox
   claimable. One row is leased at a time for 30s; XADD and WAITAOF are pipelined
   on one physical connection without MULTI. Published requires local AOF plus
   configured replica fsync ACKs; expired claim tokens cannot settle. Cluster mode
   resolves the stream primary and reloads topology after errors. Unknown publication
   outcomes retry the same immutable event ID with jitter.
4. Controlplane's isolated projection consumer writes the immutable revision
   locally under a per-plan transaction advisory lock acquired before the CTE snapshot.
   The insert computes the nearest successor boundary; older overlapping windows are
   shortened. Delivery permutations converge; immutable conflicts are logged and rejected,
   infrastructure failures stay pending. The projection includes its flat `state` and `allow_create` policy, then warms
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

## Administrative reads and configuration

Administrative list/history remain methods in the existing hypervisor_resource_plan
handler/service/repository/entity files, not a separate catalog branch. See the resource
plan list and history God Views. Customer effective-only endpoints are unchanged.

HYPERVISOR_RESOURCE_PLAN_REPLICA_ACKS defaults to 1 (HA); Compose explicitly sets 0,
still requiring local AOF. HYPERVISOR_RESOURCE_PLAN_DURABLE_WAIT defaults to 2s (1ms..5s).
HYPERVISOR_RESOURCE_PLAN_REDIS_CLUSTER enables a dedicated cluster client for this relay.
AOF must be enabled and ACLs must permit WAITAOF; missing guarantees cause retry,
never a silent fallback to XADD-only publication. Other module clients are unchanged.

## Existing development projection audit (one-time, not a worker)

For an already populated CP database, audit overlapping revision windows and oversized
boot disks before rollout. Do not resize existing plans or VMs automatically. In a
maintenance transaction close only windows extending past the next revision using
LEAD(effective_from) OVER (PARTITION BY plan_id ORDER BY revision_number), preserving
any earlier explicit end. This is one-time data repair, not a reconciliation workflow.
Fresh databases use corrected baseline constraints; no new table/column is added.

## Regression verification

- CP integration tests apply all six delivery orders of three revisions and concurrent
  replays against PostgreSQL, then assert non-overlapping effective windows.
- Cost integration tests exercise latest/effective reads, keyset history, OCC and
  claim-token/lease fences against PostgreSQL. Redis tests require AOF and verify
  standalone local durability, missing replica ACKs and Cluster primary routing.
- The failover test is explicitly opt-in: set `AURORA_TEST_ALLOW_CLUSTER_FAILOVER=1`
  only with a disposable `AURORA_TEST_REDIS_CLUSTER`. It promotes a replica and checks
  recovery using the same cached ClusterClient and immutable event identity.
- `cost-console/test/hypervisor_resource_plan.test.cjs` executes the actual component
  with mocked API responses: a scheduled latest revision is the OCC token; a conflict
  refreshes data but never automatically retries the mutation. Point
  `AURORA_TEST_NODE_MODULES` to a test-only jsdom installation to run it with Node.
