# Personal Hypervisor Estimate — God View

This self-user read quotes one hour of selected VM allocation and a 730-hour
monthly display estimate. It does not reserve capacity, debit a wallet, admit a
create request or promise that a future settlement run will pin the same rate.

## API-scope contract

The browser calls neutral
`GET /api/v1/billing/wallet/estimate/hypervisor?cpu_cores=...&memory_mib=...&disk_gib=...`.
ACR verifies the Cloud Trinity or Billing Alias/source session, removes spoofed
identity headers, derives the platform personal owner, and rewrites the request
to `/api/v1/personal/billing/wallet/estimate/hypervisor` with the original
bounded query. Direct client input to the internal `/personal` path is denied.

The trusted `x-zone-id` selects only the Hypervisor-owned Zone multiplier. The
client cannot put a Zone, owner, wallet, schedule code or charge kind in the
query. This self read has no permission/level authorization middleware and no
session proof.

## Phase 1 — Client → Envoy → ACR

```mermaid
sequenceDiagram
    participant C as Cloud Console
    participant E as Envoy
    participant A as ACR
    participant API as Cost API
    C->>E: GET neutral Hypervisor estimate plus limits
    E->>A: CheckRequest method/path/origin/cookies/query
    A->>A: CORS, rate, verified self session and personal rewrite
    A-->>E: remove spoofed context; inject trusted user/Zone
    E->>API: GET internal personal estimate with unchanged limits
```

## Phase 2 — Resolve component schedules and Zone adjustment

The handler accepts CPU `1..1024`, memory MiB `1..4194304` and disk GiB
`1..1048576`. GPU is rejected until the provisioning workflow enforces a GPU
SKU/count. The Hypervisor estimate workflow independently resolves the current
Global schedule for vCPU, memory and disk plus one Hypervisor Zone adjustment.
Pricing caches are performance paths; Billing PostgreSQL remains authority.

Each hourly quantity is `selected_limit * 3600`. The workflow applies exact
progressive rational brackets and the Zone rational, then rounds once per
component at the micro-unit boundary. Checked integer addition produces the
hourly total; checked multiplication by 730 produces the monthly display
estimate. Every BIGINT JSON field is a base-10 string.

```mermaid
sequenceDiagram
    participant H as Hypervisor estimate handler
    participant S as Hypervisor estimate workflow
    participant P as Global pricing snapshots
    participant Z as Hypervisor Zone adjustment
    H->>S: validated limits plus trusted Zone
    S->>P: resolve vCPU, memory and disk schedules at UTC now
    S->>Z: resolve immutable Zone multiplier or 1/1
    S->>S: limit x 3600; exact price; checked totals
    S-->>H: flat hourly components, total, monthly estimate and lineage
```

Missing schedules, checksum mismatch or overflow returns `503` and no partial
quote. Invalid limits return `400`. The workflow never reads runtime telemetry.

## Cloud Console plan presets

The VM create screen renders Basic, Standard and Performance as fixed resource
profiles. There is no custom CPU, memory or boot disk. The only user-defined
allocation is a bounded repeated list of additional data disks. Profiles contain
no currency or money. Each
card independently calls this neutral estimate workflow and displays the
server-calculated 730-hour amount; selecting a card only copies its limits into
the create request. The estimate uses the profile boot disk plus the sum of all
additional disks. Network remains
explicitly usage-priced and separate. If pricing is unavailable, the create
button remains disabled and no estimated amount is submitted as authority.

## Code map

- `cost-manager/api/internal/transport/http/handler/hypervisor_pricing_handler.go`
- `cost-manager/api/internal/service/hypervisor_estimate_service.go`
- `cost-manager/api/internal/repository/hypervisor_pricing_repo.go`
- `cost-manager/api/internal/app/route.go`
- `acr/src/gateway/ext_authz.rs`
- `cloud-console/src/features/compute/create-screen.tsx`
