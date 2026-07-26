# Cloud Console Refactor Baseline

Recorded on 2026-07-26 from `cloud-console` before the Phase 1 implementation.
This file freezes the observable starting point and the ownership rules used by
the refactor. It is intentionally separate from `DESIGN.md`, which remains the
visual contract.

## Reproducible quality baseline

| Check | Result |
| --- | --- |
| TypeScript/TSX source files | 154 |
| Files with a client boundary | 88 |
| ESLint | 84 errors, 68 warnings |
| `tsc --noEmit` | Pass |
| Next production build | Pass, Next.js 16.2.9 |

Commands are run from `cloud-console`:

```bash
npm run lint
npx tsc --noEmit --pretty false
npm run build
```

The largest coupling hotspots at baseline are the RBAC create route (794
lines), Storage `ObjectsTab` (748), the generated sidebar primitive (724), the
workspace route (700), `CreateBucketForm` (531), signup (495), RBAC details
(487), RBAC list (464), notifications drawer (437), RBAC edit (433), and the
Storage object detail panel (427).

## Route and owner inventory

| Routes | Owner | Remote state / realtime | Presentation permission |
| --- | --- | --- | --- |
| `/signin`, `/activate` | Authentication | session/login/activation HTTP | Public boundary |
| `/billing/authorize` | Billing handoff | verified IAM session and deployment-owned redirect | Verified session |
| `/` | Overview | aggregate HTTP queries | verified render context |
| `/storage`, `/storage/new`, `/storage/[id]` | Object Storage | bucket HTTP, access session, Zone Gateway; bucket-size realtime hint | `storage:*` render entries |
| `/mail`, `/mail/templates*`, `/mail/consumers*` | Mail | durable HTTP state plus distinct notification/runtime streams | `mail:*` render entries |
| `/users` | IAM | user list/mutations | `iam:users` |
| `/rbac*` | RBAC | roles and permission tree | RBAC render entries |
| `/workspaces*` | Workspaces | Zone-bound workspace catalog/mutations | personal/tenant workspace entry |
| `/tenants*` | Tenants | tenant list/mutations | tenant render entries |

At baseline, `users`, `roles`, `permissions`, and `workspaces` use query keys
that are not principal/context scoped. Mail keys are partially scoped. Zone
catalog and object data also have cache owners outside TanStack Query.

## Cache ownership inventory

| State | Baseline owner | Target owner / policy |
| --- | --- | --- |
| Verified session, render context, profile | module state plus `localStorage` | in-memory session provider after server verification |
| Remote resource data | TanStack Query plus component mirrors | TanStack Query, scoped by session/context |
| Zone catalog | unbounded module cache | bounded TanStack Query with focus/reconnect invalidation |
| Workspace selection | Context plus cookie | context; cookie is an untrusted selection hint |
| Presigned URL / object capability | `sessionStorage` in Storage | bounded memory only, erased on expiry/teardown |
| Realtime connection/listeners | global Context exposing Centrifuge | one principal-scoped client and bounded typed registry |

## Security and transport freeze

- Protected routes remain in a verifying state until `/api/v1/me/session`
  succeeds. Cached display data is never authentication proof.
- Browser permission checks only decide what to render. Envoy, ACR and the
  backend authorize every operation.
- A principal/context transition cancels old requests before clearing cached
  data. A generation fence prevents late responses from restoring old data.
- The retired `/sts-token`, `storage.object.sts`, `ObjectStsResponse`, and
  browser S3 credential flow must be deleted. There is no compatibility
  fallback.
- Object control operations use an opaque access-session handle and the Zone
  Gateway. Upload/download use method- and object-bound short-lived transfer
  tickets. Handles and tickets live in memory only.
- Realtime publications are hints or soft state. Reconnect always reconciles
  durable state over authenticated HTTP.
- Invalid, oversized, cross-channel, duplicate or stale realtime publications
  are dropped with redacted diagnostics. The browser has no DLQ.
- Browser/SDK traffic remains limited to Envoy and Centrifugo public endpoints.

## Responsive verification matrix

The implementation is verified against these layout contracts. Automated
static/component coverage is the CI gate; manual browser capture remains a
release check because repository rules prohibit Codex from opening Chrome.

| Width | Navigation | Context controls | Resource data |
| --- | --- | --- | --- |
| 360 | modal Sheet, safe-area aware | inside Sheet/header menu | required columns plus bounded horizontal scroll/detail Sheet |
| 768 | modal Sheet | reachable without hover | compact table; destructive action remains explicit |
| 1024 | 60/272 px desktop rail | compact header controls | table-first, optional columns may move to detail |
| 1440 | 272 px expanded rail | inline | full table and 67/33 divided pane where applicable |
| 1920 | 272 px expanded rail | inline | full-width resource surface; no card-grid expansion |

Every size checks navigation focus, header overflow, context switching, sticky
table identity/actions, Dialog/Sheet focus restoration, and divided-pane
overflow. Status is never communicated by color alone.

## Phase 0 gate

The route, state, realtime and security owners above are frozen. A change to
transport or trust boundaries requires a matching God View change; ordinary
component/file movement does not.
