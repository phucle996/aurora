# Personal Storage Object Bulk Delete — God View

This workflow deletes objects through the S3 multi-object-delete subresource. It
is separate from bucket deletion and only accepts non-versioned delete XML. It
does not call Controlplane or create a storage outbox.

## API-scope contract

Browser calls
`POST /zone-control/v1/storage/buckets/{physical_bucket}/bulk-delete` with
non-empty Delete XML and `x-aurora-access-session-id`. ACR requires
`DeleteObject` action in an actor/Zone-bound Central record. The external path
must bind the stored bucket name. The key prefix contract has a deliberate
behavior: when `key_prefix` is non-empty, this bulk-delete path is rejected
because it has no `/objects/{key}` segment to prove prefix. For empty scope,
ACR and Zone do not inspect XML object keys beyond forbidding `VersionId`.

## REST input and output

### Request headers used

| Header | Use |
|---|---|
| `Cookie` | ACR authenticates Trinity session; stripped before MinIO. |
| `x-aurora-access-session-id` | ACR Central record lookup and Zone record correlation. |
| `Origin` | CORS. |
| `X-Requested-With` or `Sec-Fetch-Site` | Required ACR CSRF signal for `POST`. |
| `Content-Type: application/xml` | Cloud Console sets it. Gateway binds exact bytes but does not require this header value. |

### Path and body payload

| Part | Contract |
|---|---|
| Bucket | Must equal recorded physical bucket. |
| Path | Exactly `/bulk-delete`; no query is allowed. |
| Delete body | Non-empty valid UTF-8 required only insofar XML scan can read it. Both ACR and Zone ExtAuthz cap body at 64 KiB. |
| `VersionId` | ACR and Zone scan XML tokens case-insensitively and reject any `VersionId` element, including namespace-prefixed form. |
| Object keys | Cloud Console accepts 1 to 1000 selected keys. Server does not parse count, key syntax or individual scope for empty-prefix sessions. |

### Response headers

| Boundary | Headers |
|---|---|
| ACR to Zone | Four signed headers are injected with overwrite semantics. |
| Zone authorizer | Removes these four after verification and adds audit action `DeleteObject`. |
| Zone Lua | Removes all remaining Aurora/AWS/MinIO/cookie/client auth headers. |
| Zone to MinIO | Rewrites to `/{bucket}?delete` and signs with Envoy SigV4. |

### Response payload

| Status | Payload |
|---|---|
| `200` | MinIO multi-delete XML result. Cloud Console currently treats successful HTTP response as completion without parsing per-key errors. |
| `400` | Empty/oversized/unreadable body, `VersionId`, unsafe route, or MinIO XML error. |
| `401` / `403` | Session, CSRF, action, assertion or scope mismatch. |
| `503` | Zone authorizer/KV is unavailable or access projection not ready. |
| `5xx` | MinIO or proxy failure. |

## Key contract

| Key/component | Store | Operation | Invariant |
|---|---|---|---|
| `storage_access:{session_id}` | Central Auth-State Redis | ACR GET | Requires `DeleteObject`, actor/Zone/expiry/policy and bucket match. |
| Vault assertion | Signed request headers | Binds exact external POST route and raw XML SHA-256 | 10-second lease and fresh operation id. |
| Zone replay cache | Authorizer Moka | Claims jti | Local replay guard. |
| `AURORA_ZONE_ACCESS/{session_id}` | Zone JetStream KV | Record match | Non-empty prefix makes this route fail path scope before MinIO. |

## Phase 1 — Client → Envoy → ACR

ACR executes CORS, Zone Control rate limits, Trinity verification and CSRF. It
loads Central record, classifies exact `POST /bulk-delete` as `DeleteObject`,
requires allowed action, strict bucket path and body without `VersionId`. It
does not parse requested object keys. ACR generates one assertion whose body
hash covers raw XML and signs via Vault Transit.

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Central Envoy
    participant A as ACR ExtAuthz
    participant AR as Auth-State Redis
    participant V as Vault Transit

    B->>E: POST bulk delete XML and access session id
    E->>A: CheckRequest exact path headers and raw body
    A->>AR: Verify Trinity session and Central record
    A->>A: Check CSRF DeleteObject bucket scope and VersionId ban
    alt prefix-scoped session, invalid XML bytes or denied action
        A-->>E: Local 400 or 403
        E-->>B: No Zone request
    else full bucket session is allowed
        A->>V: Sign exact POST path and XML body hash
        A-->>E: Inject signed Zone headers
        E->>E: Route Zone Control cluster
    end
```

## Phase 2 — Zone Control edge rewrite

Central Envoy sends original request over mTLS. Zone Lua keeps only required
assertion headers and strips cookie/client credentials. Zone authorizer receives
unrewritten external body and independently checks the same semantics. On
allow, regex rewrite maps route to `/{bucket}?delete`; then Envoy performs SigV4
with the post-rewrite path and streams XML to private MinIO.

```mermaid
sequenceDiagram
    participant CE as Central Envoy
    participant ZE as Zone Control Envoy
    participant L as Zone Lua
    participant ZA as Zone Authorizer
    participant M as Private MinIO

    CE->>ZE: mTLS POST external bulk delete plus XML
    ZE->>L: Remove caller auth cookie and untrusted headers
    L->>ZA: ExtAuthz original path and raw body
    alt denied or unavailable
        ZA-->>ZE: 403 or 503
        ZE-->>CE: error
    else allowed
        ZA-->>ZE: Audit headers and remove assertion headers
        ZE->>ZE: Rewrite to bucket delete query
        ZE->>M: SigV4 signed multi-object delete
    end
```

## Phase 3 — Zone record recheck and physical delete

Zone authorizer verifies signature, key id, its Zone, issuer/audience, exact
path/body hashes, 10-second validity and jti. It reads KV and checks session
fields, `DeleteObject`, bucket and prefix semantics. MinIO then processes
individual delete elements. The gateway forwards result XML but current browser
client does not inspect whether MinIO reported individual failures.

```mermaid
sequenceDiagram
    participant ZA as Zone Authorizer
    participant K as Keyring and replay cache
    participant KV as Zone access KV
    participant ZE as Zone Envoy
    participant M as MinIO
    participant B as Browser

    ZA->>K: Verify assertion and consume jti
    ZA->>KV: Read access record
    alt record and request match
        ZA-->>ZE: Allow
        ZE->>M: POST bucket delete XML
        M-->>ZE: Multi-delete result XML
        ZE-->>B: 200 result XML
    else record missing
        ZA-->>ZE: 503
        ZE-->>B: 503
    else mismatch
        ZA-->>ZE: 403
        ZE-->>B: 403
    end
```

## Failure and security rules

| Condition | Behavior |
|---|---|
| Key prefix is non-empty | Bulk delete is unavailable by construction. Caller needs an endpoint with each key in a canonical path or a new reviewed body schema. |
| Client asks version delete | `VersionId` is forbidden. Versioned delete requires a separate workflow/capability. |
| Multi-delete body has 1000 long keys | It can exceed 64 KiB and is denied before MinIO despite UI count limit. |
| Per-object MinIO error in `200` XML | UI currently reports success because it does not parse XML error entries. This is a product correctness gap. |
| Retry after lost response | New request gets new assertion. Multi-delete is generally idempotent for missing keys but response semantics are MinIO-owned. |
| Static Central Zone route mismatch | Wrong Zone denies assertion rather than permitting cross-zone delete. |

## Code map

- `cloud-console/src/features/storage/objects/api.ts`
- `acr/src/storage/control_assertion.rs`
- `zone-control-edge-gateway/envoy.yaml`
- `zone-control-edge-gateway/authorizer/src/authorization.rs`
