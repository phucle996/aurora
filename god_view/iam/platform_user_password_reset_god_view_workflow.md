# Platform User Password Reset — God View

This workflow lets a platform operator reset a weaker global user's password.
It is a personal/platform-owned critical mutation. The user-directory list is a
separate read workflow; its browser page may host this action but does not own
its authority or durable transition.

## API scope and client policy

| Part | Contract |
|---|---|
| Browser API | `PUT /api/v1/critical/iam/users/:id/password` |
| Owner branch | Personal only; the browser never sends `/personal` |
| Body | `{ "password": "opaque-secret" }`; it is signed as the exact raw body and is never logged or trimmed |
| Browser policy | The `/personal/users` reset form evaluates the same five current requirements while typing: at least 8 characters, lowercase, uppercase, digit and non-alphanumeric character. It shows the live checklist and disables Confirm until all pass. This is guidance only; it is not authority. |
| Authorization | Session proof, `Authorize("iam:users:manage", L1Registry, "2")`, then the repository rechecks `target.role_level > caller_level` in the mutation statement |

The Console shows the reset action only to an actor whose compiled session
permission contains `iam:users:manage`. It does not call the backend for each
keystroke and does not derive a password rule from an error response. The
Controlplane password validator remains authoritative.

## Phase 1 — Client → Envoy → ACR

Before the mutation, `criticalFetchJSON` obtains a one-time challenge from
`POST /api/v1/auth/session-proof/challenge`. The browser signs the canonical
message containing that challenge, nonce, `PUT`, the query-free public path,
SHA-256 of the exact serialized JSON body and timestamp.

| Boundary | Exact behavior |
|---|---|
| Client → Envoy | `PUT /api/v1/critical/iam/users/:id/password`; Trinity cookies; `Content-Type: application/json`, `X-Aurora-Requested-With: cloud-console`, challenge ID, timestamp and Ed25519 signature headers |
| Envoy → ACR | ext-authz `CheckRequest` receives original method, path, bounded complete body and client headers |
| ACR local | Applies CORS, critical rate budget, CSRF, Trinity session/device state and one-time proof verification/consumption. Invalid, expired or replayed proof returns locally; no upstream request occurs. |
| ACR → Controlplane | Removes raw proof signature/timestamp and all untrusted identity/context headers; rewrites only after verified personal ownership to `/api/v1/personal/critical/iam/users/:id/password`; injects verified actor headers, `x-session-proof-verified=true` and the consumed challenge UUID. |

## Phase 2 — Controlplane durable mutation

```mermaid
sequenceDiagram
    participant R as Gin route
    participant P as RequireSessionProof
    participant A as Authorize
    participant H as UserHandler
    participant S as UserService
    participant Repo as UserRepository
    participant DB as PostgreSQL
    participant T as User activity timeline

    R->>P: Require injected verified marker and challenge UUID
    P->>A: Valid proof only
    A->>H: Check iam:users:manage and caller level <= 2
    H->>H: Parse target UUID, strict bind, validate password policy
    H->>S: Flat ResetUserPassword command with trusted caller level
    S->>S: Hash opaque password; clear plaintext before repository
    S->>Repo: ResetUserPassword
    Repo->>DB: CTE locks target global role, requires weaker target, updates hash and writes old hash to password_history
    DB-->>S: Durable update result
    S->>T: Append password-reset security activity
    S-->>H: Success or failure
```

The repository does not accept a caller level from browser input and does not
use the user-directory projection. It returns `ErrUserNotFound` when there is
no target and `ErrActionNotAllowed` when the target is not weaker than the
already authorized caller.

## Failure and settlement

| Boundary | Result |
|---|---|
| Client checklist is incomplete | No mutation request is sent |
| Invalid/replayed proof, session or CSRF failure | ACR local denial; no password is read or written upstream |
| Missing ACR proof marker/challenge | `403 verified session proof is required` before authorization |
| Missing compiled permission, wrong caller level or protected target | `403` and no password update |
| Invalid body or server-side policy failure | `400 invalid password`; Console normally prevents this but server fails closed |
| Target missing | `404` |
| Hash or PostgreSQL failure before CTE commits | `500`; password unchanged |
| Timeline append fails after commit | `500` although password is already durable; retry must not assume the old password remains valid |

No outbox, retry or recovery worker owns this mutation. The exact request proof
is one-time, so a browser retry obtains and signs a fresh challenge.
