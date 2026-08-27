# Billing Storage Zone Price Adjustment List — God View

This operator read lists immutable Storage multiplier versions for exactly the
Zone verified by ACR. It does not publish, cancel or mutate pricing.

## API-scope contract

Cost Console sends
`GET /api/v1/billing/storage/zone-price-adjustments?limit=100` with Billing
Alias cookies. Envoy supplies the exact method, path, query, headers and origin
in the ACR `CheckRequest`. ACR enforces CORS/rate/session, removes
caller-provided proof and identity headers, and overwrites them with the
verified Billing Alias user and `x-zone-id`. It forwards the unchanged
method/path/query; there is no owner rewrite and the query contains no
`zone_id`. Cost API requires `billing:pricing_schedule:read`; session proof is
not required for this read.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant C as Cost Console
    participant E as Envoy
    participant A as ACR
    participant API as Cost API
    C->>E: GET Storage Zone adjustment history
    E->>A: CheckRequest exact method/path/query/headers
    A->>A: CORS, rate and Billing Alias verification
    A-->>E: overwrite identity context with trusted user and Zone
    E->>API: unchanged GET plus trusted x-zone-id
```

## Phase 2 — Flat CTE read projection

Transport validates `limit` in `1..100` and obtains the trusted Zone. The
Storage list service passes its flat query to its own repository port. One CTE
orders only that Zone's versions, marks the latest row and computes which
half-open interval is effective at PostgreSQL `NOW()`. The repository reads at
most `limit+1` rows to return bounded history and `has_more` without a second
count query.

The response contains trusted `zone_id`, UTC observation time and repeated flat
Storage list items. Multiplier numerator/denominator are decimal strings at
JSON; timestamps are RFC3339Nano UTC. PostgreSQL retains BIGINT integers. Empty
history means Global inheritance `1/1` and expected first version `0`.

## Failure and security rules

- Missing trusted Zone is denied before repository access.
- Invalid limit returns 400.
- Database failure returns 500 without a fabricated cache fallback.
- Caller path/query/body cannot select another Zone.
- Settlement independently resolves its immutable effective version from
  Billing PostgreSQL at report time.

## Code map

- `cost-manager/api/internal/transport/http/handler/storage_pricing_handler.go`
- `cost-manager/api/internal/service/storage_zone_adjustment_list_service.go`
- `cost-manager/api/internal/repository/storage_zone_adjustment_list_repo.go`
- `cost-console/src/page/pricing-schedules/StorageZoneAdjustmentPanel.tsx`
