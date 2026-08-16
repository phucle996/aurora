# Personal Storage Object Head — God View

This workflow reads object metadata through Zone Control Edge. It does not return
object bytes. Cloud Console uses it before showing object detail and combines it
with the separate tag-read workflow.

## API-scope contract

Browser calls
`HEAD /zone-control/v1/storage/buckets/{physical_bucket}/objects/{object_key}`
with Trinity cookies and `x-aurora-access-session-id`. ACR authenticates the
session and signs actor/workspace/Zone/session plus exact request facts. Zone
KV supplies the resource policy and Zone requires `GetObject`. The literal
path bucket must equal the Zone record bucket; object path must start with its
`key_prefix` when scope is non-empty. Encoded slash/backslash/dot,
double slash, `%25`, `.` and `..` segments are rejected before Zone routing.

There is no Controlplane owner rewrite or `Authorize` call here. Ownership was
durably checked during access-session prepare, while Zone rechecks the
short-lived capability and current resource admission per request.

## REST input and output

### Request headers used

| Header | Use |
|---|---|
| `Cookie` | ACR authenticates Trinity session. Zone Lua strips it before MinIO. |
| `x-aurora-access-session-id` | Opaque Zone record correlation UUID; it is not bearer authority. |
| `Origin` | ACR CORS check. |
| `traceparent` | Trace only. No client-provided `x-amz-*` or `Authorization` survives. |

### Path payload

| Field | Contract |
|---|---|
| `physical_bucket` | Must exactly equal access record bucket name. |
| `object_key` | Non-empty. All components are path-sensitive and must be within configured prefix. |
| Query | Not allowed for object head. Any query makes path binding fail. |
| Body | Must be empty. |

### Response headers

| Result | Headers |
|---|---|
| `200` | MinIO metadata such as `content-type`, `etag`, `x-amz-meta-*`, optional `x-amz-version-id`. |
| ACR to Zone | Four overwritten access/assertion headers only. |
| Zone to MinIO | Assertion/access headers removed, audit headers added, Envoy generates SigV4 after rewrite. |
| `403` / `503` | No assertion material in response. |

### Response payload

`HEAD` has no response body. `403` represents failed authentication, Zone
capability binding or admission. `503` represents unavailable Zone authorizer/dependency or
missing Zone access record. MinIO may return normal S3 `404` or `5xx`.

## Key contract

| Key/component | Store | Operation | Invariant |
|---|---|---|---|
| Signed assertion | Vault Transit and request headers | Schema 2 authenticated actor/workspace/Zone/session plus exact external `HEAD`, path and empty-body hashes; no resource policy | 10-second maximum lease. |
| Zone replay cache | Authorizer Moka | Claim `jti` | Local replay protection only. |
| `AURORA_ZONE_ACCESS/{session_id}` | JetStream KV | Watch/direct read | Sole capability SoT; Zone derives resource/bucket/action/prefix/expiry and binds assertion identity. |
| `AURORA_ZONE_ADMISSION/{resource_id}` | JetStream KV | Zone read | Current `ALLOW` is required before MinIO. |

## Phase 1 — Client → Envoy → ACR

ACR applies CORS, Zone Control rate limits, Trinity session verification and
request classification. Its Auth-State read is only Trinity authentication. It
rejects invalid UUID context, unreviewed route shape or non-empty body, then
signs request facts through Vault without reading Storage policy.

```mermaid
sequenceDiagram
    participant B as Browser
    participant E as Central Envoy
    participant A as ACR ExtAuthz
    participant AR as Auth-State Trinity session
    participant V as Vault Transit

    B->>E: HEAD object route plus access session id
    E->>A: CheckRequest exact HEAD path and empty body
    A->>AR: Verify Trinity user/device session
    A->>A: Validate UUID context and reviewed HEAD route with empty body
    alt any check fails
        A-->>E: Local 401, 403 or 429
        E-->>B: error
    else request is authorized
        A->>V: Sign assertion path and body hash
        A-->>E: Inject overwriting Zone headers
        E->>E: Route to configured Zone Control cluster
    end
```

## Phase 2 — Zone gateway filter chain and route rewrite

Central Envoy uses client mTLS toward Zone Control Envoy. Zone Lua strips
cookies, authorization, all `x-amz-*`, `x-minio-*`, and unapproved
`x-aurora-*`. Zone ExtAuthz receives the original external `HEAD` path. After
authorization, regex rewrite maps it to `/{bucket}/{object_key}`, then upstream
AWS request signing signs that rewritten path for private MinIO.

```mermaid
sequenceDiagram
    participant CE as Central Envoy
    participant ZE as Zone Control Envoy
    participant L as Zone Lua
    participant ZA as Zone Authorizer
    participant M as Private MinIO

    CE->>ZE: mTLS HEAD external path plus assertion headers
    ZE->>L: Strip unsafe headers
    L->>ZA: ExtAuthz original method path empty body
    alt denied or not ready
        ZA-->>ZE: 403 or 503
        ZE-->>CE: error
    else allowed
        ZA-->>ZE: Audit headers and assertion removal
        ZE->>ZE: Rewrite to bucket and object path
        ZE->>M: HEAD SigV4 signed internal request
    end
```

## Phase 3 — Zone recheck and response

Zone authorizer verifies signature/key id, own Zone, request hashes, short
validity and one-time jti. It reads access KV, binds assertion identity and
rechecks record integrity, expiry, action, bucket and prefix. It then requires
current resource admission. MinIO is called only on success. Metadata headers flow back
through both Envoys; cookies and client authorization never reach MinIO.

```mermaid
sequenceDiagram
    participant ZA as Zone Authorizer
    participant K as Keyring and replay cache
    participant KV as Zone access KV
    participant AD as Zone admission KV
    participant ZE as Zone Envoy
    participant M as MinIO
    participant B as Browser

    ZA->>K: Verify assertion and reserve jti
    ZA->>KV: Read session projection
    ZA->>AD: Require ALLOW for record resource id
    alt projection and request match
        ZA-->>ZE: Allow
        ZE->>M: HEAD rewritten object
        M-->>ZE: metadata headers
        ZE-->>B: 200 HEAD metadata
    else missing projection
        ZA-->>ZE: 503
        ZE-->>B: 503
    else mismatch
        ZA-->>ZE: 403
        ZE-->>B: 403
    end
```

## Failure rules

| Condition | Behavior |
|---|---|
| Object key outside `key_prefix` | Zone rejects before MinIO. |
| Any object query | Zone rejects because object head has no reviewed query contract. |
| Admission missing or suspended | Zone rejects before MinIO. |
| Zone replay of assertion | Local authorizer rejects same `jti`. |
| MinIO object absent | Auth succeeds; MinIO returns its normal `404`. |
| Central static zone route mismatch | Target Zone authorizer rejects assertion whose Zone differs, so cross-Zone access fails closed. |

## Code map

- `acr/src/storage/control_assertion.rs`
- `zone-control-edge-gateway/envoy.yaml`
- `zone-control-edge-gateway/authorizer/src/authorization.rs`
- `zone-control-edge-gateway/authorizer/src/control_assertion.rs`
