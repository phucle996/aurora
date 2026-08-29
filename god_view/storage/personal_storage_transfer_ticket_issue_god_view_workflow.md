# Personal Storage Transfer Ticket Issue and Revoke — God View

This workflow turns an already prepared personal storage access session into a
short-lived, one-time browser transfer capability. ACR signs only the verified
actor/workspace/Zone/session and exact operation envelope. Zone Control reads the storage
projection and is the only component that converts it into a public object
path. No access key, secret key, STS credential, or presigned URL is returned.

## API-scope contract

The browser uses `POST /zone-control/v1/transfer-tickets` to issue and
`DELETE /zone-control/v1/transfer-tickets/{ticket_id}` to revoke. The request
is personal storage because `access_session_id` was created by the personal
access-session workflow and is rebound to the verified user by ACR. Tenant
storage remains a separate no-op branch.

## REST input and output

### Request headers used

| Header | Use |
|---|---|
| `Cookie` | ACR verifies the Trinity session and derives the Zone. |
| `Origin` | ACR CORS and Public Edge CORS allow-list. |
| `X-Aurora-Requested-With` | CSRF signal for issue and revoke. |
| `X-Aurora-Access-Session-Id` | Opaque session marker carried to Zone Envoy. |
| `Content-Type` | Must be `application/json`. |
| `traceparent` | Trace correlation only, never authority. |

### JSON payload

| Field | Issue | Revoke |
|---|---|---|
| `capability` | Exactly `storage.object` | Exactly `storage.object` |
| `operation` | `upload`, `download`, `multipart_initiate`, `multipart_upload_part`, `multipart_complete`, `multipart_abort` | `revoke` |
| `access_session_id` | Prepared session UUID | UUID that issued the ticket |
| `resource.bucket_name` | Physical bucket | Same bucket |
| `resource.object_key` | Non-empty safe object key | Same key for audit binding |
| `constraints.content_length` | Required for upload | Omitted |
| `constraints.content_type` | Optional printable MIME value | Omitted |

Issue returns JSON with `method`, `url`, `transfer_ticket` and
`expires_at_unix_seconds`. Revoke returns `204` or `404`. Every issuer
response has `Cache-Control: no-store`.

The returned `transfer_ticket` is specifically a Console/browser capability
and its later presence in `X-Aurora-Transfer-Ticket` selects the Public Edge
ticket branch. Direct SDK clients do not call this workflow and must omit that
header; they retain their own MinIO SigV4 credential and enter the separate SDK
workflow. Public Edge never uses `Origin`, User-Agent or SigV4 syntax to choose
between these branches.

### Trusted ACR response headers

ACR returns only an Envoy mutation, never a ticket:

| Header | Value |
|---|---|
| `x-aurora-access-session-id` | Body session UUID |
| `x-aurora-control-assertion` | URL-safe base64 JSON assertion |
| `x-aurora-control-signature` | Vault Ed25519 signature |
| `x-aurora-control-key-id` | Vault key version |
| `x-session-proof-verified` | Critical-proof marker |

ACR removes caller workspace, critical-proof, admin and caller assertion
headers. It does not read Zone KV or storage policy.

## Key and state contract

| Key | Owner | Rule |
|---|---|---|
| Schema-2 ACR assertion | Vault-signed request facts | Authenticated actor/workspace/Zone/session, operation and exact method/path/body; no Storage policy. |
| `AURORA_ZONE_ACCESS/{access_session_id}` | Zone Control | Sole capability SoT; missing means not ready. |
| `AURORA_ZONE_ADMISSION/{resource_id}` | Zone Control | Issue requires current `ALLOW` using the resource id derived from the Zone access record. |
| `AURORA_ZONE_TRANSFER/{ticket_id}` | Issuer and Public Authorizer | File KV, history 1, bounded TTL, CAS state. |
| `operation_id` | ACR and audit | Correlates issue or revoke attempts. |
| `jti` | Zone authorizer replay cache | One assertion use per process. |

## Phase 1 — Client → Envoy → ACR

Envoy creates an ext_authz `CheckRequest` with the exact method, full path,
headers and bounded JSON body. ACR runs CORS, pre-auth rate limit, Trinity
verification, post-auth rate limit, CSRF and generic envelope validation. The
ACR branch is generic and does not branch on bucket or object semantics.

```mermaid
sequenceDiagram
    participant B as Cloud Console
    participant CE as Central Envoy
    participant A as ACR ExtAuthz
    participant AR as Auth State Redis
    participant V as Vault

    B->>CE: POST transfer ticket route with Cookie and JSON
    CE->>A: CheckRequest method path headers body
    A->>A: CORS rate limit body size and CSRF
    A->>AR: Verify Trinity session and resolve workspace and Zone
    A->>A: Validate capability operation session workspace and Zone UUIDs
    A->>A: Hash exact path and body
    A->>V: Sign assertion with Zone Control key
    alt invalid session or envelope
        A-->>CE: Local denial
        CE-->>B: 401 403 429 or 503
    else valid generic envelope
        A-->>CE: OkResponse with trusted assertion headers
        CE->>CE: Remove caller proof workspace and assertion headers
        CE->>CE: Overwrite x-aurora control headers
        CE->>CE: Forward exact method path and body
    end
```

Revoke follows the same sequence with `DELETE` and a ticket UUID suffix. The
path hash binds the assertion to that exact ticket and prevents path replay.

## Phase 2 — Zone Control authorizer and issuer

Zone Envoy Lua preserves only ACR assertion headers and the opaque access
session marker. The Rust authorizer verifies signature, issuer, audience, Zone,
method/path/body hashes, time window and replay `jti`. It then reads the Zone
access record, binds actor/workspace/Zone/session, checks record integrity,
expiry, actions, bucket, prefix and policy revision, and uses that record's
resource id for commercial admission.

```mermaid
sequenceDiagram
    participant ZE as Zone Control Envoy
    participant ZA as Zone Control Authorizer
    participant KV as AURORA_ZONE_ACCESS
    participant AD as AURORA_ZONE_ADMISSION
    participant ZC as Zone Control
    participant TV as AURORA_ZONE_TRANSFER

    ZE->>ZA: CheckRequest signed assertion and body
    ZA->>ZA: Verify Ed25519 replay and request binding
    ZA->>KV: GET access session projection
    KV-->>ZA: Actor Zone bucket actions prefix expiry
    ZA->>AD: Require ALLOW for record resource id on issue
    ZA->>ZA: Validate object scope and operation-specific constraints
    ZA->>ZA: Encode TransferGrantV1 protobuf bytes and base64url header
    ZA-->>ZE: OkResponse transfer grant header
    ZE->>ZC: Forward body and trusted grant
    alt issue
        ZC->>ZC: Generate UUID and 32 byte secret
        ZC->>ZC: Decode canonical protobuf grant
        ZC->>TV: CAS create protobuf Issued ticket
        TV-->>ZC: Durable ticket with TTL
        ZC-->>ZE: JSON URL ticket and expiry
        ZE-->>CE: HTTP 200 no-store
        CE-->>B: Opaque ticket response
    else revoke
        ZC->>TV: Read protobuf ticket and CAS Issued to Revoked
        TV-->>ZC: Revoke result
        ZC-->>ZE: 204 or 404
        ZE-->>CE: Revoke response
        CE-->>B: 204 or 404
    end
```

Storage-specific invariants are enforced here:

- Upload requires `PutObject`, a positive size no greater than 5 GiB and an
  allowed printable content type.
- Download requires `GetObject` and has no upload size constraint.
- Multipart operations require `PutObject`; upload parts require a positive size
  up to 5 GiB and part number `1..10000`. Upload IDs and optional download version
  IDs are validated then percent-encoded as query values, not inserted raw.
- The grant binds the final public method and path including query. Initiate and
  complete use `POST`, part upload uses `PUT`, abort uses `DELETE`, and versioned
  download uses `GET`. Zone Control preserves this binding in the ticket and
  rejects non-printable or oversized public paths before KV creation.
- Object keys cannot contain NUL, traversal segments, empty segments or leave
  the access record prefix.
- Revoke requires the same actor, Zone and access-session identity and a UUID
  ticket path.

The KV schemas used only in this phase are:

- `AURORA_ZONE_ACCESS/{access_session_id}` JSON: `access_session_id`,
  `binding_hash`, `actor_id`, `resource_id`, `bucket_name`, `workspace_id`,
  `zone_id`, `actions`, `key_prefix`, `expires_at_unix_seconds`,
  `policy_revision`. Issue consumes these fields to derive the grant; revoke
  uses the signed ticket/actor binding and does not widen this capability.
- `AURORA_ZONE_ADMISSION/{resource_id}` JSON: `policy_version`, `decision`,
  `effective_at_unix_seconds`, `valid_until_unix_seconds`. Issue requires a
  positive version and a currently effective `ALLOW`; revoke skips admission.
- `AURORA_ZONE_TRANSFER/{ticket_id}` protobuf `TransferTicketV1`:
  `schema_version`, `ticket_id`, `secret_sha256`, `capability`, `actor_id`,
  `zone_id`, `resource_id`, `workspace_id`, `operation_id`, `method`,
  `public_path`, optional `content_length`, optional `content_type`,
  `issued_at_unix_seconds`, `expires_at_unix_seconds`, `one_time`, `state`.
  Issue CAS-creates `Issued`; revoke preserves all bindings and CASes only
  `state` to `Revoked`.

## Phase 3 — ticket settlement and recovery

The browser keeps the plaintext ticket only in memory. A failed KV create,
missing access projection, NATS timeout or corrupt grant is a bounded `503`.
The browser obtains a fresh assertion and ticket; it never replays the old
assertion.

| Result | Browser behavior | State |
|---|---|---|
| `200` issue | Use the ticket once before expiry | `Issued` |
| `204` revoke | Forget the ticket | `Revoked` |
| `403` | Show expired, used or forbidden | No new state |
| `404` revoke | Treat as already gone | No new state |
| `503` | Exponential retry with a new `jti` | Existing KV remains authority |

## Security and failure invariants

- Ticket secrets are stored only as SHA-256 digests.
- Public authorizer performs `Issued → Consuming` using KV CAS.
- A second request with the same ticket is denied even inside TTL.
- Ticket KV retention is five minutes and ticket TTL is shorter.
- ACR and Zone dependency failures fail closed.
- Browser workspace selection is signed by ACR and must equal the Zone record;
  it cannot select a different capability owner, resource, bucket or Zone.
- Ticket issue and revoke are generic at ACR but storage-specific at Zone
  Control, preserving dependency direction.

## Code map

- `acr/src/storage/control_assertion.rs`
- `acr/src/gateway/ext_authz.rs`
- `zone-control-edge-gateway/authorizer/src/transfer_ticket.rs`
- `zone-control-edge-gateway/authorizer/src/authorization.rs`
- `zone-control/src/transfer_ticket/app.rs`
- `zone-control/src/transfer_ticket/store.rs`
- `proto/zone/transfer/v1/transfer_ticket.proto`
- `zone-public-edge-gateway/authorizer/src/main.rs`
