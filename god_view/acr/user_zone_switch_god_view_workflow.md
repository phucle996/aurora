# User Zone Switch — ACR-local Workflow God View

This workflow reissues the current user's JWT with a different concrete
physical Zone. It is a local edge response: no Controlplane HTTP handler sees
the browser request, and it does not create a new user session, change tenant
membership, or grant permission in the destination Zone.

## API-scope contract

| Boundary | Contract |
| --- | --- |
| Method/path matched | `POST /api/v1/zone/go-to-zone?zone_code={code}` with a path beginning that prefix |
| Scope | self session context only; it is not `/me`, `/personal`, or `/tenant` business routing |
| Candidate selection | caller supplies `zone_code` query text only |
| Required credentials | user Trinity `access_token`, `access_key`, `access_secret` cookies |
| Target policy | physical Zone whose cached status is `active` or `draining` |
| Forbidden target | `global` is SRE-only and always rejected before cache lookup |
| Success | local `200`, XSSI JSON `{zone_code}`, replacement JWT cookie and Zone cookie |
| Upstream forward | never |

The query does not select a session key. ACR first uses the currently signed
JWT Zone and tenant to load runtime session state, validates the cookie secret
against that session, then replaces only the signed JWT Zone claim. The old
runtime record remains at its original Zone key.

## Input, output and key contract

| Input | Owner | Validation or use |
| --- | --- | --- |
| `zone_code` query pair | browser | first literal `zone_code=` pair, trimmed and lowercased; empty is `400` |
| `access_token` | browser credential | Vault-HMAC verified user JWT, then claims decoded |
| `access_key` | browser credential | must equal JWT `access_key` claim |
| `access_secret` | browser credential | SHA-256 must equal Auth-State session `ash` |
| `Origin`, IP, `client_device_id` | edge | CORS and pre-auth rate-limit only in this local path |
| request body | none | not parsed |

| State | Store | Operation | Invariant |
| --- | --- | --- | --- |
| `zone:code:{normalized_code}` | Shared Redis L2 and ACR Zone L1 | resolve code to `(zone_id,status)` | cache data is rebuildable, never membership authority |
| `iam:user_session:{old_zone}:{tenant}:{user}:{access_key}` | Auth-State Redis | read protobuf `UserAccessSession` | session binds the existing Trinity context and secret hash |
| Vault token HMAC | Vault plus per-pod token cache | verify old JWT then sign replacement JWT | ACR never signs with local key material |
| old session indexes | Auth-State Redis | untouched | switch is not rotation or new-login registration |

## Phase 1 — Client → Envoy → ExtAuthz dispatcher

The generic dispatcher applies CORS and the pre-auth `general` rate limit. It
then dispatches this exact local route before normal user verification. Thus
normal post-auth rate limiting, CSRF, `update_last_seen`, transparent session
rotation, tenant validation, and trusted upstream-header construction do not
run for this request.

```mermaid
sequenceDiagram
    participant UI as Cloud Console
    participant E as Envoy
    participant X as ExtAuthzService check
    participant CG as CORS branch
    participant RL as RateLimiter
    participant AR as Auth-State Redis
    participant H as User zone switch handler

    UI->>E: POST Zone switch query and Trinity cookies
    E->>X: CheckRequest method path origin IP cookies
    X->>CG: validate Origin if supplied
    alt rejected origin
        CG-->>E: local permission denied
        E-->>UI: denial response
    else origin accepted or absent
        X->>RL: pre-auth general IP and device buckets
        RL->>AR: INCR ratelimit pre keys
        alt rate budget exceeded
            RL-->>E: local resource exhausted
            E-->>UI: 429
        else route starts user Zone switch prefix
            X->>H: local switch request
        end
    end
```

The pre-auth limiter is fail-open on Redis connection/command failure and its
30-second Moka block cache is only an optimization after an observed overflow.
It is unrelated to the session Redis read in the next phase.

## Phase 2 — candidate parsing and Zone cache resolution

The handler reads the raw query string itself. It takes the first pair whose
text begins `zone_code=`; it does not URL-decode or accept another parameter
name. The candidate is resolved before the handler authenticates the Trinity.
This order is important for accurate latency and information-boundary tracing.

```mermaid
sequenceDiagram
    participant H as User zone switch handler
    participant Q as Query parser
    participant Z as Zone cache facade
    participant L1 as ACR Zone L1
    participant L2 as Shared Redis Zone cache
    participant SR as SharedRedisBus
    participant CP as Hierarchy Zone responder
    participant CR as DeniedHttpResponseBuilder

    H->>Q: find first literal zone code pair
    alt empty or absent
        Q->>CR: local 400 zone code required
    else global
        Q->>CR: local 400 Zone unavailable
    else physical candidate
        H->>Z: resolve normalized code
        alt fresh L1 hit
            L1-->>Z: Zone ID and status
        else L2 hit
            Z->>L2: GET zone code
            L2-->>Z: Zone ID and status or NOT_FOUND
            Z->>L1: refresh positive or negative entry
        else cache miss
            Z->>SR: request hierarchy Zone list
            SR->>CP: GetZoneList protobuf
            CP-->>SR: ZoneEntry list
            SR-->>Z: response within one second
            Z->>L1: refresh catalog entries
            Z->>L2: persist code snapshots
        end
        alt absent inactive or status not active draining
            Z->>CR: local 400 Zone unavailable
        else candidate is usable
            Z-->>H: selected Zone ID
        end
    end
```

Point resolution maintains a 30-second L1 positive entry and, after a complete
miss, a 180-second negative entry in L1 and Shared Redis. The full-catalog
request is guarded by a per-pod single-flight mutex and a one-second timeout.
It can serve an old bounded snapshot after refresh failure.

## Phase 3 — current Trinity verification and reissue

Only a usable physical target proceeds to credential processing. The old JWT
is verified before ACR reads the current runtime record. The JWT verification
may use a valid unexpired Moka cache entry keyed by SHA-256 of the raw token;
otherwise ACR verifies the Vault HMAC and caches valid claims. Invalid tokens
are never cached.

```mermaid
sequenceDiagram
    participant H as User zone switch handler
    participant CM as Cookie extractor
    participant TM as TokenManager
    participant L1 as User JWT signature cache
    participant V as Vault HMAC verifier
    participant SM as SessionManager
    participant AR as Auth-State Redis
    participant VS as Vault HMAC signer
    participant CR as DeniedHttpResponseBuilder

    H->>CM: read access token and access key
    alt missing token or key
        CM->>CR: local 401 Unauthorized
    else credentials present
        H->>TM: verify old user JWT
        alt valid unexpired L1 entry
            TM->>L1: get SHA256 token key
            L1-->>TM: user claims
        else cache miss
            TM->>V: verify HMAC over JWT signing input
            V-->>TM: valid claims or failure
            TM->>L1: cache valid claims
        end
        H->>H: compare JWT access key with cookie key
        H->>SM: load session by old Zone tenant user key
        SM->>AR: GET user session protobuf
        H->>CM: read access secret
        H->>H: compare SHA256 secret with session ash
        alt any credential check fails
            H->>CR: local 401 Unauthorized
        else verified current session
            H->>H: replace claims zone ID only
            H->>TM: generate replacement JWT
            TM->>VS: Vault sign HMAC
            VS-->>TM: versioned signature
            H->>CR: local 200 with token and zone cookies
        end
    end
```

The replacement retains `sub`, `uid`, level, tenant ID, issuer, `exp`, `iat`,
and the same access key; only the Zone claim and JWT signature change. The
handler does not create a destination-Zone Redis session, rotate the access
secret, migrate indices, check tenant membership, or call Controlplane
authorization. It only makes later normal requests carry a JWT whose Zone claim
is the selected Zone.

## Phase 4 — Envoy local response and cookie boundary

| Result | HTTP response | Cookies | Durable effect |
| --- | --- | --- | --- |
| usable target and verified Trinity | `200` XSSI JSON `{"zone_code":...}` | replacement `access_token`, `zone_code`; both Path `/` | none beyond Vault signing audit/logs |
| malformed or unavailable candidate | `400` XSSI error body | none | none |
| missing, invalid, expired or revoked Trinity | `401` XSSI error body | none | none |
| Vault signing failure | `500` XSSI `Zone unavailable` error | none | none |
| CORS/limiter denial before handler | dispatcher denial/`429` | none | limiter counters only |

`access_key` and `access_secret` cookies are deliberately not replaced. The
new JWT therefore remains bound to the same runtime session key. Cookies are
host-only when `app_public_domain` is blank; otherwise the configured Domain is
appended. The response has `Content-Type: application/json` and ends at Envoy.

```mermaid
sequenceDiagram
    participant H as User zone switch handler
    participant CR as DeniedHttpResponseBuilder
    participant X as ExtAuthzService
    participant E as Envoy
    participant UI as Cloud Console

    H->>CR: HTTP 200 XSSI JSON and Set-Cookie values
    CR-->>X: local denied CheckResponse
    X-->>E: no trusted headers and no upstream target
    E-->>UI: replacement Zone context
```

## AS-IS security and recovery observations

| Observation | Evidence in current execution order | Effect |
| --- | --- | --- |
| candidate is resolved before user authentication | local switch handler parses and resolves before JWT/session validation | unauthenticated callers can distinguish some candidate failures via local `400` |
| no CSRF gate on this state-changing local endpoint | handler returns before normal dispatcher CSRF verification | a permitted cross-site POST can attempt a Zone-context change subject to CORS and pre-auth limits |
| no post-auth rate limiter | handler runs before normal authentication flow | only the pre-auth general budget protects this route |
| no current-context Zone revalidation | handler directly verifies JWT and Redis session | Zone-cookie mismatch and normal Zone-resolution rules are not applied here |
| session record remains keyed at old Zone | only JWT claim is changed | a later normal request may fail its runtime-session lookup unless another workflow establishes a compatible session context |

These rows document the code path as it exists. They are not an authorization
decision for a future implementation; changing any of them requires a separate
workflow and security-contract change.

## Invariants and code map

1. A user JWT is never reissued with `zone_id=global`.
2. Client `x-zone-id`, `x-zone-code`, identity and owner headers do not select
   the destination; only query text is a candidate and cache resolution decides
   whether it exists.
3. No user/tenant/workspace header is injected because no upstream request is
   authorized.
4. The zone cache is availability data, not tenant authority. The next owner
   workflow must perform its own tenant and durable authorization checks.

| Component | Responsibility | Code |
| --- | --- | --- |
| dispatcher | CORS, pre-auth limit, local interceptor order | `acr/src/gateway/ext_authz.rs` |
| switch handler | query parsing, target status policy, direct Trinity verify, local response | `acr/src/user/zone_switcher.rs` |
| token manager | Vault-HMAC JWT verification/signing and valid-only L1 cache | `acr/src/token.rs` |
| session manager | current user runtime session protobuf lookup | `acr/src/user/session.rs` |
| Zone cache | L1/L2 point lookup and hierarchy request-reply refresh | `acr/src/infra/zone.rs` |
