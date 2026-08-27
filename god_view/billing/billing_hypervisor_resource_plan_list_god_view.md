# Hypervisor Resource Plan Administrative List

## API scope
Cost Console calls GET /api/v1/billing/hypervisor/resource-plans?limit=50&after=<plan UUID>.
This is personal/platform-impact authorization, not self-user access. Read permission is
billing:pricing_schedule:read; no critical proof is required for GET. Customer effective
catalog routes under /billing/wallet remain unchanged and never expose scheduled revisions.

## Phase 1 — Client → Envoy → ACR
Client sends the Billing Alias ID/secret cookies and ordinary browser headers, no body
or session proof. Envoy supplies method, exact path/query and headers in CheckRequest.
ACR checks origin/CORS, rate and Alias/source-session binding in Auth-State Redis.
It overwrites caller x-user-id, x-user-name, x-zone-id and x-tenant-id with the verified
Alias values, strips admin/session-proof cryptographic headers and untrusted workspace
headers, and forwards the same GET path/query with an empty body. No owner rewrite or
resource-plan lookup occurs. Rejection is local (401/403/429/503), without upstream access.

## Phase 2 — Cost HTTP → resource-plan service → resource-plan repository
The route's personal authorization middleware checks read permission. Transport validates
limit 1..100 and non-nil cursor UUID. The existing resource-plan service and repository own these reads in their resource-plan files.
Administrative and customer-effective queries remain separate methods, without a catalog file branch.
The repository CTE pages stable plan IDs, including future-only plans, and returns flat
plan metadata, latest non-cancelled revision number and currently effective revision number
(0 when none). All BIGINT fields are decimal strings at HTTP. next_cursor is absent/empty
at end. This read never writes state or an outbox.
The UI retains selected plan identity across refresh; metadata is not a mutation authority.
OCC is enforced again by the publish repository.

## Failure / verification
Invalid pagination: 400 before service. Database failure: bounded 503. Keyset pagination
does not guarantee a frozen catalog across requests; new revisions are seen on refresh.
Test future-only plans, latest versus effective revision, multiple pages and bigint precision.
