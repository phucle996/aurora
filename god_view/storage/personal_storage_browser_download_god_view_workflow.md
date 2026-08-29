# Personal Storage Browser Download — God View

This workflow downloads one personal object through a one-time public transfer
ticket. The browser receives a Blob only after the Public Authorizer validates
the exact object path and ticket. It never receives an access key, secret key,
STS credential or reusable presigned URL.

## API-scope contract

The browser first issues a download ticket through the personal transfer-ticket
workflow. It then sends GET to the returned Public Edge URL with the ticket
header. The data path has no owner rewrite and no tenant context. The ticket
grant is the authority for one exact object.

## Input and output

| Input | Contract |
|---|---|
| X-Aurora-Transfer-Ticket | Opaque ticket id and secret, memory only. |
| Origin | Allowed Cloud Console origin. |
| :path | Exact bucket/object path bound by Zone Control (`/{bucket}/{key}` or `/{bucket}/{key}?versionId={version_id}` for version-specific download). |
| Request body | Empty. |
| Cookies and AWS auth | Not sent and removed if supplied. |

A successful response is the MinIO object body streamed to the browser with
safe Content-Type, Content-Length and ETag headers. The browser creates a
temporary object URL and revokes it after the download starts.

## Phase 1 — Browser → Zone Public Envoy → Public Authorizer

The sole browser/SDK selector is presence of `X-Aurora-Transfer-Ticket`; Envoy
does not classify by `Origin`, User-Agent or SigV4 syntax. The ordered route
selects `minio_transfer` before filters run. Lua preserves the ticket but strips
caller `Authorization` and `X-Amz-*` credentials, and `PublicAuthorizer` uses
the same header presence to enter the ticket branch. The header is only a
selector until its secret, bindings and admission have been verified.

An absent ticket would select the SDK branch and direct `minio` cluster. A
browser cannot silently fall back because it has no valid MinIO SigV4
credential. Conversely, a client that sends both SigV4 and a ticket is treated
only as a ticket client; its SigV4 headers are removed.

~~~mermaid
sequenceDiagram
    participant B as Browser
    participant PE as Zone Public Envoy
    participant PA as Public Authorizer
    participant TV as AURORA_ZONE_TRANSFER

    B->>PE: GET exact public object path with ticket
    PE->>PE: CORS check and remove cookie AWS and client auth headers
    PE->>PA: CheckRequest method path ticket headers
    PA->>TV: Read ticket id
    TV-->>PA: Issued download ticket
    PA->>PA: Verify secret expiry Zone method and exact path
    PA->>TV: CAS Issued to Consuming
    alt invalid expired consumed or path mismatch
        PA-->>PE: Permission denied
        PE-->>B: 403
    else valid
        PA-->>PE: OkResponse audit headers and remove ticket
        PE->>MI: Signed internal GET with bounded streaming
        MI-->>PE: Object body and metadata
        PE-->>B: Stream body ETag and content headers
    end
~~~

The authorizer performs no Controlplane or MinIO metadata lookup. Bucket and
object authorization settled in Zone Control when the ticket was created; this
phase rechecks only ticket bindings and current commercial admission.

This phase reads
`AURORA_ZONE_TRANSFER/{ticket_id}` as protobuf `TransferTicketV1` with
`schema_version`, `ticket_id`, `secret_sha256`, `capability`, `actor_id`,
`zone_id`, `resource_id`, `workspace_id`, `operation_id`, `method`,
`public_path`, optional `content_length`, optional `content_type`,
`issued_at_unix_seconds`, `expires_at_unix_seconds`, `one_time` and `state`.
Download requires exact `GET`/path, no bound content length and current
`Issued`; the CAS changes only `state` to `Consuming`. Before consuming, the
authorizer reads `AURORA_ZONE_ADMISSION/{resource_id}` fields `resource_id`,
`policy_version`, `decision`, `effective_at_unix_seconds` and
`valid_until_unix_seconds` and requires current `ALLOW`.

## Phase 2 — Public Edge stream and browser settlement

~~~mermaid
sequenceDiagram
    participant PE as Public Envoy
    participant SG as SigV4 signing filter
    participant MI as MinIO
    participant B as Browser

    PE->>SG: Authorized GET without browser ticket
    SG->>MI: Internal signed GET to bucket object path
    alt object exists
        MI-->>SG: 200 body ETag and type
        SG-->>PE: Stream response with backpressure
        PE-->>B: 200 response body
        B->>B: Create Blob URL and revoke after anchor click
    else object missing or MinIO failure
        MI-->>PE: 404 or 5xx
        PE-->>B: S3 error without ticket material
    end
~~~

Ticket consumption occurs before the upstream read. A failed read is not
replayable with the consumed ticket; the Console requests a new ticket before
retrying. Removing the ticket after authorization does not change the
previously selected `minio_transfer` route; that cluster signs the MinIO request
with Zone-owned credentials.

## Failure and recovery

| Failure | Result |
|---|---|
| Missing, corrupt, expired or consumed ticket | 403, no MinIO call. |
| Zone KV unavailable | 503, fail closed. |
| CORS preflight or origin rejected | Browser cannot start transfer. |
| MinIO 404 | Object absent, ticket already consumed. |
| Stream reset or 5xx | Request a fresh download ticket. |

## Code map

- cloud-console/src/features/storage/objects/api.ts
- cloud-console/src/app/(console)/storage/[id]/components/ObjectsTab.tsx
- zone-public-edge-gateway/envoy.yaml
- zone-public-edge-gateway/authorizer/src/main.rs
- proto/zone/transfer/v1/transfer_ticket.proto
