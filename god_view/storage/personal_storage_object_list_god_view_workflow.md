# Personal Storage Object List — God View

This workflow lists S3 objects through the Zone Control Edge Gateway. It is not a
Controlplane list API and it has no `/personal` or `/tenant` path rewrite. It
is personal because the access-session preparation workflow has already bound
this access id to one personal bucket, actor, workspace and Zone.

## API-scope contract

Browser calls
`GET /zone-control/v1/storage/buckets/{physical_bucket}/objects?list-type=2&...`
with Trinity cookies and `x-aurora-access-session-id`. This path is an
ACR-local authentication and request-attestation boundary, not a neutral owner
route. ACR requires an authenticated Trinity session and signs actor,
workspace, Zone, session id and exact request facts. It does not read or decide
Storage policy and does not run Controlplane `Authorize`. Zone Control reads
the capability authority and makes the complete authorization decision.

For list action, Zone Control requires `ListBucket` in session actions. Query must have
exactly one `list-type=2`. `prefix`, if session key scope is non-empty, is
required and must begin with `key_prefix`. Only reviewed query parameters are
permitted: `prefix`, `delimiter`, `max-keys` from 1 to 1000,
`continuation-token`, `start-after`, `encoding-type=url` and
`fetch-owner=false`, each at most once. Encoded separators, dot segments,
double slash, encoded percent and malformed percent encoding fail closed.

## REST input and output

### Request headers used

| Header | Use |
|---|---|
| `Cookie` | Envoy supplies it to ACR for Trinity session verification. It is stripped by Zone Control Envoy before MinIO. |
| `x-aurora-access-session-id` | Opaque UUID correlation handle. ACR validates its shape, overwrites it toward Zone and never treats it as a bearer credential. |
| `Origin` | ACR CORS enforcement. |
| `X-Requested-With` or `Sec-Fetch-Site` | Not required for `GET`, but may be present. |
| `traceparent` | Normal propagation, not part of access assertion. |

### Query payload

| Field | Contract |
|---|---|
| `physical_bucket` | Zone authorizer requires it to equal the Zone access record bucket byte-for-byte. |
| `list-type=2` | Mandatory exactly once. |
| `prefix` | Optional only when session scope is empty. Required and scope-prefixed when `key_prefix` is non-empty. |
| `max-keys` | Optional `1..1000`. |
| `continuation-token` and `start-after` | Either optional individually, never together. |
| Other query parameters | Denied. |

### Response headers

| Layer | Headers that matter |
|---|---|
| Browser response | S3/MinIO response headers pass through. No Aurora assertion material is exposed. |
| ACR to Zone Control | Injects `x-aurora-access-session-id`, `x-aurora-control-assertion`, `x-aurora-control-signature`, `x-aurora-control-key-id`, all overwrite semantics. |
| Zone authorizer to MinIO | Removes the four assertion/access headers and injects `x-aurora-actor-id`, `x-aurora-resource-id`, `x-aurora-control-capability=storage.object`, `x-aurora-control-action=ListBucket`, `x-aurora-operation-id`, `x-aurora-bucket-name`. |
| Zone Lua before MinIO | Removes all other `x-aurora-*`, all `x-amz-*`, all `x-minio-*`, `authorization`, `cookie`, and `x-csrf-token`. |
| Upstream signer | Envoy generates its own AWS Signature V4 after path rewrite. |

### Response payload

| Status | Payload |
|---|---|
| `200` | MinIO `ListObjectsV2` XML. Cloud Console parses `Contents`, size, last modified and continuation token. |
| `401` / `403` | ACR authentication/request-shape denial or Zone capability/admission/request-binding denial. |
| `429` | ACR Zone Control rate limit. |
| `503` | Zone authorizer unavailable, overloaded dependency or missing Zone access projection. |
| `4xx` / `5xx` from MinIO | S3 error body after authenticated proxying. |

## Key contract

| Key / component | Store | Operation | Invariant |
|---|---|---|---|
| Assertion `jti` | ACR signed JSON | One assertion per request, expires in 10 seconds | Schema 2 includes authenticated actor/workspace/Zone/session plus exact external method, full path SHA-256, empty body SHA-256 and classified action; it contains no resource policy. |
| Zone authorizer replay cache | Per-process Moka | Insert assertion `jti` with 30 second TTL | Replay shield is local only. |
| `AURORA_ZONE_ACCESS/{access_session_id}` | JetStream KV | Authorizer watch/direct read | Sole capability SoT for resource, bucket, action set, prefix, expiry and policy revision; actor/workspace/Zone must bind the assertion. |
| `AURORA_ZONE_ADMISSION/{resource_id}` | JetStream KV | Zone authorizer read | Current `ALLOW` admission is required before MinIO. |
| Central Zone cluster | Central Envoy | Route prefix to `zone_control_edge_gateway_cluster` | Current config is a static `zone-z1` Kubernetes DNS endpoint. A non-z1 assertion fails closed at Zone authorizer; production needs xDS per Zone before other Zones work. |

## Phase 1 — Client → Envoy → ACR assertion issue

ACR performs CORS, Zone Control pre-auth limit, Trinity verification, post-auth
limit and request-shape classification. Its Auth-State read is only the normal
Trinity user/device session lookup. It validates UUID context and the reviewed
route, then creates schema-2 request-fact JSON, base64url encodes it and requests
Vault Transit signature. It never reads a Storage capability record.

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Central Envoy
    participant A as ACR ExtAuthz
    participant AR as Auth-State Trinity session
    participant V as Vault Transit

    B->>E: GET Zone Control list path and access session header
    E->>A: CheckRequest exact path query headers empty body
    A->>AR: Verify Trinity user/device session
    A->>A: CORS rate limits UUID context and reviewed route/body shape
    alt authentication or request shape is invalid
        A-->>E: Deny 401, 403 or 429
        E-->>B: Local error with no Zone call
    else valid request
        A->>V: Sign encoded assertion over exact request
        V-->>A: Key version and Ed25519 signature
        A-->>E: Overwrite four trusted Zone headers
        E->>E: Route to Zone Control Edge cluster
    end
```

## Phase 2 — Central Envoy mTLS and Zone Control Edge routing

Central Envoy sends the original external path and ACR-injected headers to the
configured Zone Control cluster over mTLS. The zone gateway accepts only client
certificates chained to Central client CA with SAN `central-envoy`. Its Lua
filter removes client-derived sensitive headers, retains only the four assertion
headers, then calls Zone authorizer ExtAuthz with a 64 KiB body cap. For list
route, Envoy regex rewrites external path to `/{bucket}?{query}` and uses
private MinIO cluster with SigV4 after rewrite.

```mermaid
sequenceDiagram
    participant CE as Central Envoy
    participant ZE as Zone Control Envoy
    participant L as Zone Lua sanitization
    participant ZA as Zone Control Authorizer
    participant S as MinIO SigV4 filter

    CE->>ZE: mTLS original list path plus signed headers
    ZE->>L: Begin request
    L->>L: Remove cookie auth x-amz x-minio and untrusted Aurora headers
    L->>ZA: ExtAuthz CheckRequest exact external path and body
    alt authorizer denies or is not ready
        ZA-->>ZE: 403 or 503 mapping
        ZE-->>CE: error response
    else authorizer allows
        ZA-->>ZE: Inject audit identity/action and remove assertion headers
        ZE->>ZE: Rewrite path to bucket plus original query
        ZE->>S: Sign rewritten S3 canonical request
    end
```

## Phase 3 — Zone authorization and MinIO response

Zone authorizer verifies Ed25519 key id, assertion size and base64 JSON, issuer,
audience, schema, own Zone id, 10 second lifetime, exact method, path hash and
body hash. It inserts assertion `jti` into local replay cache. It reads the Zone
access record, binds session/actor/workspace/Zone, checks record integrity,
expiry, `ListBucket`, bucket/prefix/query, then requires current resource
admission. Only it derives trusted resource and bucket headers for MinIO.

```mermaid
sequenceDiagram
    participant ZA as Zone Control Authorizer
    participant K as Assertion keyring and replay cache
    participant KV as AURORA_ZONE_ACCESS
    participant AD as AURORA_ZONE_ADMISSION
    participant ZE as Zone Control Envoy
    participant M as Private MinIO
    participant B as Browser

    ZA->>K: Verify signature lifetime request hash and one-time jti
    ZA->>KV: Read matching access record
    ZA->>AD: Require current ALLOW for record resource id
    alt all fences and admission match
        KV-->>ZA: matching record
        ZA-->>ZE: Allow and inject audit headers
        ZE->>M: GET rewritten bucket query with Envoy SigV4
        M-->>ZE: ListObjectsV2 XML
        ZE-->>B: 200 XML response
    else record missing
        KV-->>ZA: no record
        ZA-->>ZE: 503 not ready
        ZE-->>B: 503
    else assertion or record mismatch
        ZA-->>ZE: 403 deny
        ZE-->>B: 403
    end
```

## Failure and security rules

| Condition | Behavior |
|---|---|
| Access prepare has not reached Zone | Authorizer returns `503`, and UI may retry. It must not fail open to MinIO. |
| Assertion replay at same replica | Moka replay cache rejects it. Cross-replica exactly-once is not claimed; mutating operations carry `operation_id`. |
| Client sends forged Aurora/S3 auth headers | ACR overwrites trusted headers and Zone Lua strips all caller S3/Aurora material before Envoy signs. |
| Central route points to wrong Zone | Assertion Zone mismatch denies at that Zone. Current static dev cluster therefore has availability, not cross-Zone authorization, limitation. |
| Query canonicalization disagreement | ACR signs the exact bytes; Zone alone applies the capability and strict path/query contract before Envoy rewrite. |
| Wallet admission missing or suspended | Zone denies before MinIO even if the access record and signature are otherwise valid. |
| MinIO is down | Authorized request may return upstream `5xx`; access record is unchanged. |

## Code map

- `acr/src/storage/control_assertion.rs`
- `acr/src/gateway/ext_authz.rs`
- `dev/central/envoy/routes/https_routes.yaml`
- `dev/central/envoy/envoy.yaml`
- `zone-control-edge-gateway/envoy.yaml`
- `zone-control-edge-gateway/authorizer/src/authorization.rs`
- `zone-control-edge-gateway/authorizer/src/request_binding.rs`
