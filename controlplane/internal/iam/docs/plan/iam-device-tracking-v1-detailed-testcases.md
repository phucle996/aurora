# IAM Device Tracking V1 - Detailed Test Cases

## TC-DV-001 - List my devices success
- Concern: `C1,C3`
- Preconditions: U1 has >=3 devices and valid access token
- Test Data: U1 token; limit=20 offset=0
- Steps: 1) GET /api/v1/me/devices 2) Verify payload 3) Verify DB owner scope
- Expected Result: 200; only U1 devices; fields include status/last_seen
- Layer Coverage: Router,Middleware,Handler,Service,Repo,DB
- Severity if Fail: `P1`
- Evidence Required: API response + DB snapshot

## TC-DV-002 - List my devices unauthorized
- Concern: `C1,C5`
- Preconditions: No access token
- Test Data: None
- Steps: GET /api/v1/me/devices
- Expected Result: 401 unauthorized generic
- Layer Coverage: Middleware,Handler
- Severity if Fail: `P0`
- Evidence Required: API response

## TC-DV-003 - Revoke own device success
- Concern: `C2,C3`
- Preconditions: U1 owns D2; D2 not revoked
- Test Data: U1 token; device D2
- Steps: 1) POST /api/v1/me/devices/D2/revoke 2) Check DB devices 3) Check DB refresh_tokens
- Expected Result: 204; D2 status revoked; related refresh tokens invalidated
- Layer Coverage: Router,Handler,Service,Repo,DB
- Severity if Fail: `P0`
- Evidence Required: API response + DB before/after

## TC-DV-004 - Revoke other user device denied
- Concern: `C2,C5`
- Preconditions: U1 token; D-U2 belongs to U2
- Test Data: U1 token; D-U2
- Steps: POST /api/v1/me/devices/D-U2/revoke
- Expected Result: 403/denied generic; no ownership leak; DB unchanged
- Layer Coverage: Handler,Service,Repo,DB
- Severity if Fail: `P0`
- Evidence Required: API response + DB unchanged proof

## TC-DV-005 - Logout other devices success
- Concern: `C3`
- Preconditions: U1 current device D1 + D2,D3 active
- Test Data: U1 token on D1
- Steps: 1) POST /api/v1/me/devices/logout-others 2) Check devices 3) Check refresh tokens
- Expected Result: 200; D2,D3 revoked; D1 remains active; non-D1 sessions revoked
- Layer Coverage: Router,Middleware,Handler,Service,Repo,DB
- Severity if Fail: `P0`
- Evidence Required: API response + DB before/after

## TC-DV-006 - Logout all devices success
- Concern: `C3`
- Preconditions: U1 has active devices and sessions
- Test Data: U1 token
- Steps: 1) POST /api/v1/me/devices/logout-all 2) Verify all devices and sessions
- Expected Result: 200; all U1 devices revoked; all U1 refresh tokens removed
- Layer Coverage: Handler,Service,Repo,DB
- Severity if Fail: `P0`
- Evidence Required: API response + DB snapshots

## TC-DV-007 - Refresh denied after device revoked
- Concern: `C4`
- Preconditions: Refresh cookie tied to revoked device D2
- Test Data: Revoked D2 refresh cookie
- Steps: POST /api/v1/auth/refresh
- Expected Result: 401 invalid session; access/refresh cookies cleared
- Layer Coverage: Handler,Service,Repo,DB
- Severity if Fail: `P0`
- Evidence Required: API response headers + Set-Cookie

## TC-DV-008 - Invalid device_id format on revoke
- Concern: `C5`
- Preconditions: Valid user token
- Test Data: device_id=not-uuid
- Steps: POST /api/v1/me/devices/not-uuid/revoke
- Expected Result: 400 invalid request
- Layer Coverage: Handler,Service
- Severity if Fail: `P2`
- Evidence Required: API response

## TC-DV-009 - Pagination boundary list devices
- Concern: `C7`
- Preconditions: Valid user token
- Test Data: limit=0/-1/999; offset=-1
- Steps: GET /api/v1/me/devices with boundary params
- Expected Result: Fallback defaults applied; no crash; 200
- Layer Coverage: Handler,Service,Repo
- Severity if Fail: `P2`
- Evidence Required: API responses

## TC-DV-010 - Concurrent logout-others idempotency
- Concern: `C6`
- Preconditions: U1 with multiple active sessions
- Test Data: N parallel requests (N>=10)
- Steps: 1) Fire concurrent POST logout-others 2) Verify final state
- Expected Result: No corruption; final state equal to single successful run
- Layer Coverage: Service,Repo,DB
- Severity if Fail: `P1`
- Evidence Required: Concurrency log + final DB state

