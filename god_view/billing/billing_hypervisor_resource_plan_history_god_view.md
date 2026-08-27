# Hypervisor Resource Plan Administrative Revision History

## API scope
GET /api/v1/billing/hypervisor/resource-plans/{plan_id}/revisions?limit=50&before=<revision>.
This is a personal/platform-impact read with billing:pricing_schedule:read authorization.
It is not the customer catalog and does not require critical proof.

## Phase 1 — Client → Envoy → ACR
Cost Console sends Billing Alias ID/secret cookies, GET method and exact path/query,
no body or session proof. Envoy CheckRequest carries these facts; ACR checks origin/CORS,
rate and Alias/source-session binding in Auth-State Redis. ACR overwrites x-user-id,
x-user-name, x-zone-id and x-tenant-id with verified Alias values, strips admin/session-proof
cryptographic headers and untrusted workspace headers, then forwards the unchanged
GET path/query with an empty body. There is no owner rewrite or plan lookup at ACR.
Local 401/403/429/503 rejections never reach Cost.

## Phase 2 — Cost HTTP → administrative resource-plan service → resource-plan repository
The same resource-plan owner exposes a revision-history read with its own query; it does not call publish.
Transport validates plan UUID, limit and the positive decimal BIGINT before cursor.
A CTE returns revisions by descending revision_number, including scheduled revisions.
Each row is flat, with explicit plan_id, capacity, UTC effective window, audit reason,
is_latest and is_effective. The response contains revisions[] and next_cursor, never nested
plan/revision aggregates. Missing/empty history returns an empty list.
The UI initializes the editor from the latest revision and displays historical rows read-only.
On 409 publish conflict it refreshes and requires another explicit user action; no automatic retry.

## Failure / verification
Malformed boundary: 400; DB unavailable: 503. History reads never mutate schedules.
Tests cover future revisions, descending keyset pages and current/latest distinction.
