# Billing Mail Zone Price Adjustment Publish — God View

This critical operator workflow appends one immutable Mail multiplier for the
verified operator Zone. Global schedules and other module adjustment tables are
outside this workflow.

## API-scope contract

Cost Console sends
`POST /api/v1/billing/critical/mail/zone-price-adjustments/versions` with Alias
cookies, CSRF/session proof and JSON containing `expected_latest_version`, UTC
`effective_from`, `change_reason` and decimal-string numerator/denominator. The
body contains no Zone. Envoy gives ACR the exact method, path, headers and body;
ACR applies origin/CORS, rate, session and one-time proof checks, removes raw
proof headers, overwrites identity context with the Billing Alias `x-user-id`,
`x-user-name`, `x-zone-id` and `x-tenant-id`, and injects the opaque verified
proof marker/challenge. It does not rewrite the path. Cost requires
`billing:pricing_schedule:publish` at critical level.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant C as Cost Console
    participant E as Envoy
    participant A as ACR
    participant API as Cost API
    C->>E: POST bounded multiplier body plus proof
    E->>A: CheckRequest exact method/path/headers/body
    A->>A: CORS, rate, session, CSRF and one-time proof
    A-->>E: consume raw proof; overwrite trusted Alias user and Zone context
    E->>API: exact body plus trusted context
```

## Phase 2 — Immutable append transaction

Transport parses BIGINT fields from decimal strings. Service normalizes the
timestamp to UTC microseconds, validates business effective/OCC constraints and
computes the canonical checksum. Repository takes a Mail+Zone advisory lock,
closes the previous effective interval and inserts the next version atomically.
`105/100` is 105% of Global; absence means `1/1`.

Malformed/overflow values return 400, OCC/overlap returns 409, and database
failure rolls back both interval close and insert. Settlement resolves the Mail
adjustment effective at evidence time and supplies only rational lineage to the
generic kernel.

## Code map

- `cost-manager/api/internal/transport/http/handler/mail_pricing_handler.go`
- `cost-manager/api/internal/service/mail_zone_adjustment_service.go`
- `cost-manager/api/internal/repository/mail_pricing_repo.go`
- `cost-manager/api/migrations/000003_tables_pricing.up.sql`
