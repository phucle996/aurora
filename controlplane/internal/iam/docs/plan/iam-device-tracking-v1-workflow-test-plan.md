# IAM Device Tracking V1 - Workflow Test Plan

## 1) Scope & Source of Truth

- Primary implementation source: `controlplane/internal/iam/docs/plan/iam-device-tracking-v1-implementation-plan.md`
- Behavior source: `controlplane/internal/iam/docs/spec/iam-device-tracking-v1-spec.md`
- Runtime code surfaces:
  - `controlplane/internal/iam/route.go`
  - `controlplane/internal/iam/transport/http/handler/device_handler.go`
  - `controlplane/internal/iam/service/device_service.go`
  - `controlplane/internal/iam/repository/device_repo.go`
  - `controlplane/internal/iam/repository/refresh_token_repo.go`
  - `controlplane/internal/iam/service/refresh_token_service.go`

SoT ownership:
- Device lifecycle: `iam.devices`
- Session continuation: `iam.refresh_tokens`
- API behavior: IAM handlers + middleware access chain

---

## 2) Test Objectives

1. Validate user device management workflow end-to-end:
   - list devices,
   - revoke one device,
   - logout other devices,
   - logout all devices.
2. Validate security boundaries:
   - auth required,
   - owner-only semantics,
   - no resource enumeration leak.
3. Validate runtime parity:
   - revoked device cannot continue refresh flow.
4. Validate data integrity:
   - device status updates and refresh-token revocations consistent.

---

## 3) Environment & Preconditions

Mode: `Local` (real Postgres + Redis preferred).

Required preconditions:
- IAM migrations applied on test schema.
- Test users:
  - `U1` with at least 3 devices (`D1`, `D2`, `D3`) and active refresh tokens.
  - `U2` with at least 1 device for cross-owner negative tests.
- Access token + refresh token cookies for U1/U2.
- Current-request device context available for `logout-others` (JWT claim `device_id`).

---

## 4) Concern Inventory

- C1: Authn/authz boundary on `/api/v1/me/devices*`.
- C2: Owner-only revoke semantics.
- C3: Multi-device logout correctness (device rows + refresh tokens).
- C4: Revoked continuation block on refresh.
- C5: Error semantics generic and consistent.
- C6: Data write integrity under concurrent actions.
- C7: Basic performance baseline for list/revoke/logout APIs.

---

## 5) Scenario Matrix

### Critical (P0/P1)

#### TC-DV-001
- Title: List my devices success
- Concern: C1, C3
- Preconditions: U1 authenticated
- Steps:
  1. Call `GET /api/v1/me/devices?limit=20&offset=0` with U1 token.
  2. Verify response payload.
- Expected:
  - HTTP 200.
  - Only devices owned by U1 returned.
  - Fields include lifecycle and last-seen info.
- Layer coverage: Router, middleware, handler, service, repo, DB
- Severity if fail: P1
- Evidence: API response, DB query `SELECT user_id,id,status,last_seen_at FROM iam.devices WHERE user_id=<U1>`

#### TC-DV-002
- Title: List my devices unauthorized
- Concern: C1, C5
- Preconditions: no access token
- Steps:
  1. Call `GET /api/v1/me/devices`.
- Expected:
  - HTTP 401.
  - Generic message (`unauthorized`), no device hints.
- Layer coverage: Middleware, handler
- Severity if fail: P0
- Evidence: API response

#### TC-DV-003
- Title: Revoke own device success
- Concern: C2, C3
- Preconditions: U1 authenticated, device `D2` belongs to U1 and not revoked
- Steps:
  1. Call `POST /api/v1/me/devices/{D2}/revoke`.
  2. Query `devices` row and related refresh tokens.
- Expected:
  - HTTP 204.
  - `iam.devices.status='revoked'`, `revoked_at` non-null for `D2`.
  - Refresh tokens tied to `D2` are removed/invalidated per repo contract.
- Layer coverage: Router, middleware, handler, service, repo, DB
- Severity if fail: P0
- Evidence: API response + DB snapshots before/after

#### TC-DV-004
- Title: Revoke another user device denied
- Concern: C2, C5
- Preconditions: U1 authenticated, target device `D-U2` belongs to U2
- Steps:
  1. U1 calls `POST /api/v1/me/devices/{D-U2}/revoke`.
- Expected:
  - Denied (`403` or generic equivalent by contract).
  - No ownership leak in response.
  - Device row unchanged.
- Layer coverage: Handler, service, repo
- Severity if fail: P0
- Evidence: API response + DB verify unchanged

#### TC-DV-005
- Title: Logout other devices success
- Concern: C3
- Preconditions: U1 authenticated on `D1` (current), has other devices D2/D3 active
- Steps:
  1. Call `POST /api/v1/me/devices/logout-others`.
  2. Verify devices and refresh tokens.
- Expected:
  - HTTP 200.
  - All U1 devices except `D1` set `revoked`.
  - Refresh tokens for revoked devices removed.
  - `D1` remains active.
- Layer coverage: Router, middleware, handler, service, repo, DB
- Severity if fail: P0
- Evidence: API response + DB before/after (devices + refresh_tokens)

#### TC-DV-006
- Title: Logout all devices success
- Concern: C3
- Preconditions: U1 authenticated, multiple active devices/tokens
- Steps:
  1. Call `POST /api/v1/me/devices/logout-all`.
  2. Verify all U1 device statuses and refresh tokens.
- Expected:
  - HTTP 200.
  - All U1 devices revoked.
  - All U1 refresh tokens removed.
- Layer coverage: Handler, service, repo, DB
- Severity if fail: P0
- Evidence: API response + DB verification

#### TC-DV-007
- Title: Refresh denied after device revoked
- Concern: C4
- Preconditions: refresh token tied to device D2, D2 status set `revoked`
- Steps:
  1. Call `POST /api/v1/auth/refresh` with revoked-device refresh cookie.
- Expected:
  - HTTP 401 invalid session.
  - Access/refresh cookies cleared by handler.
- Layer coverage: Refresh handler, refresh service, refresh repo, DB
- Severity if fail: P0
- Evidence: API response + response cookies

### Important (P1/P2)

#### TC-DV-008
- Title: Invalid device_id format on revoke
- Concern: C5
- Steps: call revoke with malformed UUID.
- Expected: HTTP 400 invalid request.
- Layer coverage: Handler, service
- Severity if fail: P2
- Evidence: API response

#### TC-DV-009
- Title: Pagination boundary on list
- Concern: C7
- Steps: query with `limit<=0`, large `limit`, negative `offset`.
- Expected: default fallback behavior from service contract.
- Layer coverage: Handler, service, repo
- Severity if fail: P2
- Evidence: API responses

#### TC-DV-010
- Title: Concurrent logout-others idempotency
- Concern: C6
- Steps:
  1. Fire N concurrent `logout-others` requests for same user/session.
  2. Re-check final DB state.
- Expected:
  - No partial corruption.
  - Final state equivalent to single successful execution.
- Layer coverage: Service, repo, DB
- Severity if fail: P1
- Evidence: request logs + DB final snapshot

---

## 6) Detailed Critical Path Execution Order

1. Run auth boundary tests: `TC-DV-002`.
2. Run ownership + revoke tests: `TC-DV-003`, `TC-DV-004`.
3. Run session-wide actions: `TC-DV-005`, `TC-DV-006`.
4. Run refresh continuation gate: `TC-DV-007`.
5. Run resilience/perf baseline: `TC-DV-009`, `TC-DV-010`.

---

## 7) Evidence Pack Requirements

Per test case collect:
- API request/response (status, body, headers, cookie effects).
- DB verification queries:
  - `iam.devices` rows for impacted user/device IDs.
  - `iam.refresh_tokens` rows for impacted user/device scope.
- Runtime logs (handler warnings/errors for negative flows).

Minimum DB query set:
- `SELECT id,user_id,status,revoked_at,last_seen_at FROM iam.devices WHERE user_id=$1 ORDER BY created_at;`
- `SELECT id,user_id,device_id,expires_at FROM iam.refresh_tokens WHERE user_id=$1 ORDER BY issued_at;`

---

## 8) Coverage Targets

- Scenario coverage: `>= 10/10` planned scenarios.
- Concern coverage: `7/7` concerns (C1..C7).
- Layer coverage: API, DB, Security all covered; cache/async marked N/A for current implementation slice.
- Critical-path coverage: `>= 7/7` critical scenarios (`TC-DV-001..007`).

Uncovered critical concerns policy:
- Any uncovered P0/P1 scenario => automatic `NO_GO` until waived explicitly.

---

## 9) GO/NO_GO Gate

`GO` only if:
1. All P0 scenarios pass.
2. No data integrity mismatch between device status and refresh-token revocation.
3. No unauthorized access/ownership bypass found.
4. Refresh continuation block on revoked device verified.

`NO_GO` if any of above fail.

---

## 10) Known Current Gaps (from implementation state)

These are expected to appear as coverage gap or N/A in this plan iteration:
- Admin/system intervention APIs (`/admin/devices/:id/suspicious|revoke`) not fully implemented yet.
- Audit event persistence contract for device actions not complete yet.
- DTO hardening/sanitization for device list response still basic.

