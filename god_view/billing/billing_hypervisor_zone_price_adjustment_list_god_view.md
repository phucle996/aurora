# Billing Hypervisor Zone Price Adjustment List — God View

This read workflow lists immutable Hypervisor multipliers for one selected Zone.
It never changes a Global base price or Hypervisor resource plan.

## API-scope contract

Cost Console calls
`GET /api/v1/billing/hypervisor/zone-price-adjustments?limit=100&zone_code={code}`.
The browser sends only a catalog code, never a Zone UUID/header. Envoy provides
the exact query to ACR; ACR verifies the Billing Alias, requires one
active/draining code, resolves it through hierarchy and overwrites `x-zone-id`.
Cost requires `billing:pricing_schedule:read` and reads only that trusted header.

## Processing and failure boundary

The handler validates `limit` then calls the Hypervisor list repository with
the injected target Zone. Its bounded CTE returns immutable versions, the latest
row and the row effective at PostgreSQL `NOW()`. No rows means `1/1` Global
inheritance. Missing/repeated/malformed/inactive `zone_code` fails at ACR;
PostgreSQL remains the pricing SoT.

## Code map

- `acr/src/gateway/ext_authz.rs`
- `cost-manager/api/internal/transport/http/handler/hypervisor_pricing_handler.go`
- `cost-console/src/page/pricing-schedules/ZonePriceAdjustmentsTab.tsx`
