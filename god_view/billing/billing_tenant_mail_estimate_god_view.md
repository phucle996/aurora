# Tenant Mail Accepted-Recipient Estimate — God View

This Tenant wallet read quotes accepted recipients for the verified Tenant
context. It does not reserve or debit funds.

## API-scope contract

Browser calls neutral
`GET /api/v1/billing/wallet/estimate/mail?recipient_quantity=1000`. Envoy sends
the exact request context to ACR. ACR verifies Tenant membership, CORS and rate,
removes spoofed identity/Tenant/Zone headers, rewrites to
`/api/v1/tenant/billing/wallet/estimate/mail`, and injects trusted user,
Tenant/workspace authority and Zone. Direct client access to `/tenant` is
denied. Cost authorization requires Tenant permission `billing:wallet:read`;
the repository/estimate workflow never accepts an owner or Zone from query.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant C as Cloud Console
    participant E as Envoy
    participant A as ACR
    participant API as Cost API
    C->>E: GET neutral Mail estimate
    E->>A: CheckRequest exact route/query/session context
    A->>A: CORS, rate and verified Tenant membership
    A-->>E: rewrite /tenant; inject trusted Tenant and Zone
    E->>API: GET internal Tenant estimate
```

## Phase 2 — Quote

Transport accepts quantity `1..1000000000`. Service resolves the same Global
Mail schedule plus only the trusted Zone's Mail multiplier, calculates exact
rational progressive pricing and returns a flat decimal-string BIGINT response
with immutable lineage. Missing/invalid pricing returns 503; invalid quantity
returns 400. Personal and Tenant calls do not share owner authority even though
they use the same pricing calculation implementation.

## Code map

- `cost-manager/api/internal/app/route.go`
- `cost-manager/api/internal/transport/http/handler/mail_pricing_handler.go`
- `cost-manager/api/internal/service/mail_estimate_service.go`
- `cloud-console/src/app/(console)/mail/components/ConsumersTab.tsx`

