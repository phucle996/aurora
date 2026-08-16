# Personal Storage SDK Object Download — God View

This is the direct S3-compatible personal download path for a provisioned
access key. It has no browser transfer ticket and no user-session context. The
Public Authorizer still enforces the Zone commercial admission projection before a
GET reaches MinIO.

## API-scope contract

The SDK sends `GET /{bucket}/{object-key}` to the Zone Public Edge with SigV4.
The bucket name selects the admission name index; the client cannot select a
different resource, wallet, Zone or owner by header.

Only `GET /{bucket}` or `GET /{bucket}/` is classified as a non-billable bucket
list and exempt from wallet admission. Any path with an object-key segment is
an object download and must pass admission even if the key ends in `/` or the
query contains `prefix`/`list-type`.

## Input and output

| Input | Contract |
|---|---|
| `Authorization` | SigV4 credential; retained for MinIO. |
| `X-Amz-Date`, `X-Amz-Content-Sha256`, optional security token | Retained for SigV4. |
| `Range`, `If-None-Match` | Passed to MinIO for normal S3 semantics. |
| `Cookie`, `X-Aurora-*` | Removed/ignored and never authority. |
| Request body | Empty for GET. |

Successful output is the MinIO object stream and safe S3 metadata. Admission
denial is `403` before MinIO; admission dependency failure is `503`.

## Key and state contract

| Key | Rule |
|---|---|
| `AURORA_ZONE_ADMISSION/name/{bucket}` | Must contain current `ALLOW` and the matching resource id. |
| MinIO IAM policy | Independently checks the access key and object path. |
| `storage.access.completed.v1` | Records response status and `bytes_sent` without credentials/path. |

## Phase 1 — Client → Zone Public Envoy → Public Authorizer

```mermaid
sequenceDiagram
    participant SDK as Personal SDK
    participant E as Zone Public Envoy
    participant A as Public Authorizer
    participant KV as AURORA_ZONE_ADMISSION

    SDK->>E: GET /bucket/object with SigV4 headers
    E->>E: Retain AWS headers; strip cookies and spoofed Aurora headers
    E->>A: CheckRequest method/path/headers
    A->>KV: Read name/{bucket} admission
    alt missing, expired, suspended or malformed
        A-->>E: 403 or 503
        E-->>SDK: No MinIO request
    else current ALLOW
        A-->>E: OkResponse with trusted x-aurora-resource-id
    end
```

The authorizer first classifies list only by the exact bucket-root path. Every
object GET then checks wallet version, exact bucket-name binding and the
effective window. It never performs a central wallet lookup.

## Phase 2 — Public Envoy → MinIO

```mermaid
sequenceDiagram
    participant E as Zone Public Envoy
    participant S as SigV4 upstream filter
    participant M as MinIO

    E->>S: Authorized GET
    S->>M: Signed GET to minio:9000
    M-->>S: Object stream or S3 error
    S-->>E: Stream response
    E-->>SDK: Object bytes and safe metadata
```

The stream is bounded by Envoy connection flow control. Access logging emits a
generic metering event with the trusted resource id and transfer counters only.

## Phase 3 — Metering projection and recovery

Zone OTel stores the completed access event in the local ClickHouse journal and
the Zone report later carries `download_bytes` to the Storage Cost Engine. A
wallet suspension takes effect on the next request after the admission event
reaches Zone KV; an in-flight GET is not replayed or charged twice.

## Code map

- `zone-public-edge-gateway/envoy.yaml`
- `zone-public-edge-gateway/authorizer/src/main.rs`
- `zone-control/src/admission.rs`
- `zone-control/src/zone_control_state.rs`
- `cost-manager/engine/src/service/storage/usage_report_settlement.rs`
