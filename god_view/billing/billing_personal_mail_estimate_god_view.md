# Personal Mail Accepted-Recipient Estimate — God View

This self-user read quotes a selected number of successfully accepted
recipients. It neither reserves money nor predicts delivery success.

## API-scope contract

Browser calls neutral
`GET /api/v1/billing/wallet/estimate/mail?recipient_quantity=1000`. Envoy sends
the exact method/path/query, cookies, origin and headers in ACR `CheckRequest`.
ACR checks CORS/rate and the verified self session, removes spoofed identity and
Zone headers, rewrites to `/api/v1/personal/billing/wallet/estimate/mail`, and
injects trusted `x-user-id` and `x-zone-id`. Direct client access to the internal
path is denied. This `/personal` self read has no permission middleware and no
session proof.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant C as Cloud Console
    participant E as Envoy
    participant A as ACR
    participant API as Cost API
    C->>E: GET neutral estimate with decimal quantity
    E->>A: CheckRequest exact route/query/session context
    A->>A: CORS, rate, verified personal session and rewrite
    A-->>E: remove spoofed identity; inject trusted user and Zone
    E->>API: GET internal personal estimate
```

## Phase 2 — Resolve Global + Mail Zone

Transport accepts `1..1000000000`. Service resolves the active Global
`mail.delivery.accepted_recipient` schedule and the trusted Zone's Mail
multiplier, applies progressive rational brackets, then rounds once at the
micro-unit boundary. All BIGINT JSON fields are decimal strings. The flat
response includes quantity, amount, currency and immutable pricing/adjustment
lineage. Missing price, checksum mismatch or overflow returns 503.

Cloud Console requests exactly 1,000 recipients for display. The amount is
informational; runtime resume remains guarded independently by wallet admission
and readiness.

## Code map

- `cost-manager/api/internal/transport/http/handler/mail_pricing_handler.go`
- `cost-manager/api/internal/service/mail_estimate_service.go`
- `cloud-console/src/features/billing/api.ts`
- `cloud-console/src/app/(console)/mail/components/ConsumersTab.tsx`

