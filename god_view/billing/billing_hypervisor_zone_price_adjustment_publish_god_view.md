# Billing Hypervisor Zone Price Adjustment Publish — God View

This critical operator workflow appends one immutable Hypervisor multiplier for
the verified operator Zone. It never publishes a Global base schedule and never
uses the Storage adjustment table.

## API-scope contract

The operator sends
`POST /api/v1/billing/critical/hypervisor/zone-price-adjustments/versions?zone_code={code}` with
Billing Alias cookies, CSRF/session proof and a bounded JSON body containing
`expected_latest_version`, UTC `effective_from`, `change_reason` and decimal
string numerator/denominator. The body has no Zone. ACR resolves exactly one
active/draining `zone_code`, removes caller Zone headers, consumes proof bound
to the exact request and injects the target Zone. Cost API requires `billing:pricing_schedule:publish` at the
critical role level.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant C as Cost Console
    participant E as Envoy
    participant A as ACR
    participant API as Cost API
    C->>E: POST Hypervisor multiplier version plus proof
    E->>A: exact CheckRequest headers/path/body
    A->>A: CORS, session, CSRF, rate, target zone_code and one-time proof
    A-->>E: verified operator user and target Zone; remove spoofed authority
    E->>API: unchanged bounded body plus trusted context
```

## Phase 2 — Immutable append transaction

The workflow parses multiplier fields as int64, normalizes time to UTC
microseconds, validates OCC/effective windows and computes the canonical
checksum. The repository takes a Zone advisory lock, closes the previous
effective interval and inserts the next immutable version atomically. `105/100`
means 105% of Global; absence of a Hypervisor adjustment means `1/1`.

Settlement resolves the version effective at the closed allocation/network
window and passes only its opaque rational lineage to the generic PAYG kernel.

## Failure rules

- Invalid proof, permission or trusted Zone: deny before PostgreSQL.
- Malformed/overflowing decimal string: `400`.
- OCC or overlapping effective window: `409`.
- PostgreSQL failure: rollback; no partial window close.
- Adjustment rows never cross module ownership boundaries.

## Code map

- `cost-manager/api/internal/transport/http/handler/hypervisor_pricing_handler.go`
- `cost-manager/api/internal/service/hypervisor_zone_adjustment_service.go`
- `cost-manager/api/internal/repository/hypervisor_pricing_repo.go`
- `cost-manager/api/migrations/000003_tables_pricing.up.sql`
- `cost-manager/engine/src/service/hypervisor/hourly_allocation_settlement.rs`
