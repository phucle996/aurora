# Billing Storage Zone Price Adjustment Publish — God View

This critical Storage pricing workflow appends one immutable multiplier for the
verified operator Zone. It never publishes a PAYG base schedule and never
mutates a wallet. Storage owns Global base-price publication in the separate
Storage base-price workflow and owns how its trusted Zone selects an adjustment.
The generic pricing-schedule service is read-only catalog infrastructure.

## API-scope contract

The operator calls
`POST /api/v1/billing/critical/storage/zone-price-adjustments/versions` with a
Billing Alias session and one-time proof. The body contains
`expected_latest_version`, UTC `effective_from`, `change_reason`,
`multiplier_numerator` and `multiplier_denominator`. Multiplier fields are
base-10 integer strings. The body cannot contain a Zone: ACR removes caller
authority headers and injects the verified operator Zone; Cost API requires
fresh `billing:pricing_schedule:publish` permission.

`105/100` means 105% of the Global base; `80/100` means 80%. The multiplier is
applied to the unrounded exact base rational and the PAYG kernel rounds only
once after multiplication.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant UI as Cost Console
    participant E as Central Envoy
    participant A as ACR ExtAuthz
    participant V as Proof verifier
    participant API as Cost API
    UI->>E: POST Storage adjustment version with proof and bounded JSON
    E->>A: CheckRequest exact method/path/headers/body
    A->>A: CORS, session, CSRF and rate limits
    A->>V: Verify exact request and consume one-time proof
    alt denied
        A-->>E: local 401/403/429/503
    else verified operator
        A->>A: remove caller Zone; inject verified user and Zone
        A-->>E: allow unchanged path/body
        E->>API: trusted request
    end
```

## Phase 2 — Cost API immutable adjustment transaction

The handler reads the verified Zone from request context, parses the two
integer strings and rejects malformed transport input. The service requires
numerator nonnegative and denominator positive, normalizes time to UTC
microseconds and computes a checksum over Zone, version, effective time and
rational multiplier. The repository locks
the latest adjustment for that Zone, performs OCC/effective-window checks,
closes the previous window and inserts the new version atomically.

```mermaid
sequenceDiagram
    participant H as StorageAdjustmentHandler
    participant S as StorageAdjustmentService
    participant R as StorageAdjustmentRepository
    participant DB as Billing PostgreSQL
    H->>S: verified Zone plus named publish command
    S->>S: validate rational, UTC time and checksum
    S->>R: append immutable Zone adjustment
    R->>DB: BEGIN; advisory lock Zone; OCC/window checks
    R->>DB: close prior interval; insert new version
    DB-->>R: COMMIT
    R-->>H: 201 adjustment identity/version/checksum
```

## Phase 3 — Storage settlement consumption

At a closed report boundary the Storage adapter resolves the exact Zone
adjustment version from Billing PostgreSQL and verifies its checksum. Absence
means explicit Global inheritance (`1/1`) until the first Zone override is
published. The adapter passes only immutable rational adjustment lineage to the
PAYG kernel. Other modules cannot reuse this table or infer their pricing scope
from Storage.

## Failure and durable rules

- Invalid proof/permission/Zone: deny before PostgreSQL.
- Invalid or overflowing integer string: `400`, no mutation.
- Stale version or overlapping effective window: `409`, no mutation.
- Adjustment rows are immutable except closing the preceding effective window.
- Storage settlement pins adjustment ID/version/checksum/numerator/denominator
  in its settlement run; each ledger entry references that run, so later
  publishes cannot reprice existing ledger lineage.
- PostgreSQL is the adjustment SoT; Redis is not required for correctness.

## Code map

- `cost-manager/api/internal/transport/http/handler/storage_pricing_handler.go`
- `cost-manager/api/internal/service/storage_pricing_service.go`
- `cost-manager/api/internal/repository/storage_pricing_repo.go`
- `cost-console/src/page/pricing-schedules/StorageZoneAdjustmentPanel.tsx`
- `cost-manager/api/migrations/000003_tables_pricing.up.sql`
- `cost-manager/engine/src/service/storage/usage_report_settlement.rs`
- `cost-manager/engine/src/engine/runtime.rs`
