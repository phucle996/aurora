# Billing Pricing Schedule Metadata Update — God View

This critical workflow changes Global base-schedule display metadata only. It
never changes a rate, effective interval, model, charge kind, module adjustment
or historical version.

## API-scope contract

`PATCH /api/v1/billing/critical/pricing-schedules/{code}/metadata` is an
operator critical route. The request body is `{metadata_version, display_name}`.
ACR checks session, CSRF, proof and rate limits; it forwards the exact method,
path, proof marker and body after removing caller identity/Zone overrides and
injecting verified identity. Cost API requires fresh session proof and
`billing:pricing_schedule:publish` authorization.

## Phase 1 — edge and ACR

```mermaid
sequenceDiagram
    participant UI as Cost Console
    participant E as Central Envoy
    participant A as ACR ExtAuthz
    participant AR as Auth State Redis
    participant V as Proof verifier
    participant API as Cost API
    UI->>E: PATCH schedule metadata with proof and JSON body
    E->>A: CheckRequest method/path/used headers/body
    A->>A: CORS and pre-auth rate limit
    A->>AR: verify Alias/session
    A->>A: CSRF and post-auth rate limit
    A->>V: verify exact method/path/body proof and consume nonce
    alt failure
        A-->>E: local 401/403/429/503; no API call
        E-->>UI: bounded error
    else success
        A->>A: remove caller x-user/x-zone/proof overrides
        A->>A: inject trusted identity and proof marker
        A-->>E: allow exact path/body
        E->>API: forward trusted request
    end
```

## Phase 2 — proof → handler → service → repository transaction

```mermaid
sequenceDiagram
    participant SP as RequireSessionProof
    participant AU as Fresh Authorize
    participant H as PricingScheduleHandler.UpdateMetadata
    participant S as PricingScheduleService.UpdateMetadata
    participant R as PricingScheduleRepository.UpdateMetadata
    participant DB as Billing PostgreSQL
    SP->>AU: verified one-time proof marker
    AU->>H: billing:pricing_schedule:publish
    H->>H: bind code and display_name/metadata_version
    H->>S: named metadata command
    S->>R: trim/bound values and OCC token
    R->>DB: BEGIN; SELECT schedule FOR UPDATE
    R->>DB: compare metadata_version
    alt stale or missing schedule
        DB-->>R: rollback; conflict/not-found
        R-->>H: 409/404
    else valid
        R->>DB: UPDATE display_name and increment metadata_version
        R->>DB: COMMIT
        R-->>H: updated metadata
        H-->>SP: 200 JSON
    end
```

Only the metadata row changes. Any rate change must use the separate immutable
version publish workflow and its outbox. The failure boundary is before any
wallet/usage workflow; retries are safe after a fresh OCC read.
