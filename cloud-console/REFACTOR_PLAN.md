# Cloud Console Refactor Plan

> Status: Implementation in progress  
> Created: 2026-07-26  
> Scope: `cloud-console` and the minimum cross-service contracts required by its security/realtime flows

## 1. Purpose

Refactor Cloud Console into a maintainable Enterprise Cloud Control Plane UI while preserving the visual language defined in [`DESIGN.md`](./DESIGN.md).

The desired result is:

- data-centric, table-first and high-density;
- responsive without turning the Console into a consumer mobile dashboard;
- reusable through explicit Console patterns rather than generic helpers;
- one clear owner for server cache, authenticated session and realtime connection;
- safe across logout/login, tenant, Zone and workspace changes;
- recoverable when HTTP, Centrifugo or a backend replica fails;
- easy to extend with new resource pages without copying an existing 400–800 line page.

## 1.1 Execution record (2026-07-26)

This plan is being executed as bounded vertical slices. A phase is marked
complete only when its code, contract notes and validation evidence are all
present; backend routes that are not deployed are represented as explicit
fail-closed UI states.

Validation already completed from `cloud-console/`:

- `npm run lint -- --format stylish` — 0 errors, 0 warnings.
- `npx tsc --noEmit --pretty false` — pass.
- `npm run build` — pass (Next.js 16.2.9/Turbopack).
- `acr/` — `cargo test --all-targets` pass for the changed query-action
  classifier.

The first implementation batch also moved domain ownership out of the flat
`src/lib/api` directory (`features/auth`, `features/iam`, `features/rbac`,
`features/storage`, `features/mail`, `features/workspaces`,
`features/zones` and `features/tenants`), moved realtime mechanics under
`src/realtime`, and made the workspace/Zone transitions cancellation-fenced.

Manual responsive and browser failure-injection checks remain open because
Chrome/MCP and Docker are intentionally not run by this refactor pass.

This document is a delivery plan, not a workflow contract. `DESIGN.md` remains the visual source of truth. God Views remain the end-to-end workflow source of truth and must be updated in the same change-set whenever a transport, security boundary or business contract changes.

## 2. Non-goals

- Do not redesign Aurora as a SaaS card dashboard.
- Do not replace Table-First screens with card grids by default.
- Do not introduce Redux, another global event bus or a second server-state library.
- Do not create a generic schema-driven page builder.
- Do not move authorization decisions into the browser. Client permission checks only control presentation.
- Do not claim realtime delivery is durable or exactly-once.
- Do not refactor every feature in one atomic rewrite.

## 3. Confirmed AS-IS debt

- `src/app/(console)/layout.tsx` uses a fixed sidebar and an inline `marginLeft`; it does not provide a real mobile navigation mode.
- `src/components/ui/sidebar.tsx` already contains responsive Sheet behavior but is not used by the Console shell.
- 88 of 154 TypeScript/TSX files are client components.
- Several route/components are 400–800 lines and mix routing, query, mutation, realtime and presentation.
- Full ESLint currently reports 152 findings: 84 errors and 68 warnings.
- Server state is split across TanStack Query, Context, module-level variables, `localStorage`, `sessionStorage` and component state.
- Global query keys such as `users`, `roles` and `workspaces` are not consistently scoped by authenticated principal/context.
- Query cache is not cleared as one atomic session-lifecycle operation.
- Zone catalog has an unbounded module-lifetime cache and can hide an `active -> draining` transition.
- Render context/profile cached in `localStorage` can render stale identity/navigation before the server verifies the current session.
- Presigned URLs are persisted in `sessionStorage`.
- The backend STS endpoint/command/executor and secret-bearing notification
  transport have been removed. The object browser now uses an opaque
  access-session handle for Gateway list/head/tag/bulk operations; upload and
  download remain visibly disabled until the short-lived presigned data-ticket
  routes are deployed. There is no browser S3 client or STS fallback.
- Realtime uses string event names and `unknown` payloads; decoding, dedupe and reconciliation are spread across callsites.
- Several hand-written modals, drawers, tables and filter bars duplicate existing UI primitives.
- The notifications drawer owns API hydration, local DOM events, realtime handling, read mutations, toasts and presentation in one component; its toast state is currently dead/unpopulated code.
- `next.config.ts` contains no application-level security headers. Edge enforcement must be verified rather than assumed.

## 4. Architecture decisions

| ID | Decision |
| --- | --- |
| ADR-CC-01 | Preserve the Enterprise Cloud Console design: neutral palette, hairline divider, compact density, full-width resource pages and 67/33 master-detail. |
| ADR-CC-02 | Table remains the primary resource representation. Narrow screens use column priority, controlled horizontal scrolling and detail drawers, not automatic card conversion. |
| ADR-CC-03 | TanStack Query is the sole owner of remote/server state in the browser. |
| ADR-CC-04 | Context is limited to small orchestration state: verified session identity, active Console context, theme and realtime connection status. |
| ADR-CC-05 | There is one Centrifugo client per authenticated principal. Feature code never receives the raw client. |
| ADR-CC-06 | Realtime notification/runtime events are hints or soft-state updates. Durable truth is rehydrated from an authenticated HTTP endpoint. |
| ADR-CC-07 | Credentials, session tokens and presigned capabilities are never carried in notification history, logs, local storage or session storage. |
| ADR-CC-08 | Shared abstractions are promoted only after real reuse. Do not create `helpers.ts`, `common.ts` or `misc.ts`. |
| ADR-CC-09 | Route `page.tsx` files compose a feature and its access boundary; they do not own a complete feature implementation. |
| ADR-CC-10 | A frontend permission check may hide/disable UI but cannot authorize an operation. Envoy/ACR/backend remain authoritative. |
| ADR-CC-11 | Storage list/head/metadata/tag/bulk operations use the Central access-session handle and Zone Gateway. Upload/download use short-lived presigned data-tickets. Browser code never receives or reconstructs S3 credentials. |

## 5. Target source layout

```text
src/
├── app/                         # Next routes and thin composition
├── shell/                       # ConsoleShell, navigation, header, context switcher
├── session/                     # Verified session lifecycle and identity boundary
├── realtime/                    # One client, typed contracts, registry and recovery
├── features/
│   ├── notifications/
│   ├── storage/
│   ├── mail/
│   ├── iam/
│   ├── rbac/
│   ├── workspaces/
│   ├── tenants/
│   └── zones/
└── shared/
    ├── api/                     # HTTP client, APIError and boundary decoding
    ├── query/                   # QueryClient policy and lifecycle bridge
    └── ui/                      # UI primitives and proven Console patterns
```

A feature only creates files it actually needs. Suggested names are `api.ts`, `queries.ts`, `realtime.ts`, `types.ts` and `components/`; empty ceremonial layers are prohibited.

## 6. Phase overview

| Phase | Name | Priority | Depends on | Status |
| --- | --- | --- | --- | --- |
| 0 | Baseline and contract freeze | P0 | None | Complete |
| 1 | Security and identity isolation | P0 | Phase 0 | Code complete; contract/E2E pending |
| 2 | Console shell and responsive patterns | P1 | Phase 0 | Code complete; visual matrix pending |
| 3 | HTTP, query and cache ownership | P1 | Phase 1 | Core complete; feature API move pending |
| 4 | Realtime core and recovery | P1 | Phase 1, Phase 3 | Core complete; feature adapters/tests pending |
| 5 | Feature-by-feature migration | P1/P2 | Phase 2, Phase 3, Phase 4 | Storage slice complete; remaining slices pending |
| 6 | Quality, performance and failure testing | P1 | Runs throughout; closes after Phase 5 | Static gate complete; tests/benchmarks pending |
| 7 | Cleanup, documentation and rollout | P2 | Phase 5, Phase 6 | In progress |

## 7. Detailed tasks

### Phase 0 — Baseline and contract freeze

Goal: produce a trustworthy baseline before moving files or changing behavior.

- [x] **CC-0001 — Route and ownership inventory**
  - Map every route to feature, API calls, query keys, permissions and realtime subscriptions.
  - Identify files that are true primitives versus generated-but-unused UI code.
  - Record page/component line count and client-component baseline.

- [x] **CC-0002 — Responsive verification matrix**
  - Capture behavior at 360, 768, 1024, 1440 and 1920 CSS pixels.
  - Check sidebar overlap, header overflow, context switching, table actions, dialogs and divided panes.
  - Desktop is the primary operator surface; mobile must remain safe and usable for inspection and bounded actions.

- [x] **CC-0003 — Security and transport contract map**
  - Trace session verification, logout, Zone/workspace selection, Centrifugo authentication and channel authorization.
  - Inventory and delete every stale Console reference to `/sts-token`,
    `storage.object.sts`, `ObjectStsResponse` and browser S3 credentials.
  - Trace the replacement path: access-session creation, readiness, Gateway
    request header, ACR assertion, Zone authorization and presigned transfer.
  - Compare the implementation with the relevant God Views and platform connection matrix.
  - Any contradiction must be resolved explicitly; do not silently choose code over God View.

- [x] **CC-0004 — Quality baseline**
  - Save ESLint, TypeScript, route bundle and accessibility baselines.
  - Classify findings into application code, generated primitives and obsolete/dead code.
  - Define a no-new-errors gate from the first implementation change.

**Phase gate**

- Every route, cache owner and realtime consumer has an explicit owner.
- The retired STS callsites and the replacement access-session/Gateway
  contracts are mapped before changing the object browser.
- Baseline measurements are reproducible in CI.

### Phase 1 — Security and identity isolation

Goal: remove cross-principal cache leakage and secrets from the notification path before broad UI refactoring.

- [x] **CC-1001 — Verified session bootstrap**
  - Do not treat cached render context/profile as proof of authentication.
  - Keep the protected Console shell in a verifying state until `/api/v1/me/session` succeeds.
  - Keep render context and profile in memory unless a documented non-sensitive persistence requirement exists.
  - Fail closed for permission rendering when context/profile hydration fails.

- [x] **CC-1002 — Atomic session teardown**
  - On logout, session expiry or principal change: disconnect realtime, cancel inflight queries, clear QueryClient, clear workspace/Zone-derived state and wipe sensitive memory before redirect.
  - Make teardown idempotent so concurrent 401 responses do not race or emit repeated user-visible notifications.
  - Ensure a late response from the previous principal cannot repopulate cache after teardown.

- [ ] **CC-1003 — Adopt the non-secret storage access contract** *(Console list/head/tag/bulk slice shipped; presigned data-ticket deployment and E2E remain)*
  - Remove the retired STS API call, protobuf hex decoder, credential state and
    `storage.object.sts` notification special case from Console code.
  - Create a short-lived access session through
    `POST /api/v1/storage/buckets/{id}/access-sessions`; retain only the opaque
    handle and expiry in memory, scoped to principal/Zone/workspace/bucket.
  - Realtime may announce readiness/runtime hints, but durable readiness is
    rehydrated over authenticated HTTP and no credential or presigned
    capability may enter notification history.
  - Use backend-issued short-lived presigned tickets for upload/download and
    never persist them in browser storage.
  - Treat missing Gateway route, assertion failure, expired/revoked session and
    Zone projection lag as distinct fail-closed UI states.

- [x] **CC-1004 — Sensitive browser storage policy**
  - Remove presigned URL persistence from `sessionStorage`.
  - Keep short-lived capabilities in memory only and wipe them on expiry, unmount and session teardown.
  - Persist only non-sensitive preferences such as theme and language.
  - Treat workspace/Zone cookies as UI selection hints; the backend must validate them against the verified session.

- [ ] **CC-1005 — Browser CSP/security headers shipped; CSRF/origin and redirect allow-list verification remain**
  - Verify CSRF protection for every cookie-authenticated mutation: SameSite policy plus Origin/Referer or explicit anti-CSRF mechanism.
  - Enforce CSP `connect-src` for Envoy, Centrifugo and approved object endpoints; set `frame-ancestors`, Referrer-Policy and Permissions-Policy.
  - Verify external console redirect origins against a deployment-owned allow-list.
  - Do not log payloads that may contain job messages, tokens, personal data or credentials.

- [ ] **CC-1006 — Permission boundary cleanup**
  - Keep route guards as presentation controls only.
  - Derive navigation from verified backend render context.
  - Add tests proving forged path/query/cookie values do not grant access when the backend rejects the operation.

**Phase gate**

- No secret travels through Notification Service/Centrifugo notification history.
- Logout/login as another user cannot render or restore the previous user's cached data.
- No persisted browser key contains token, credential or presigned URL.

### Phase 2 — Console shell and responsive patterns

Goal: create one DESIGN.md-compliant shell and a small set of reusable Console patterns.

- [x] **CC-2001 — ConsoleShell**
  - Replace inline `marginLeft` layout with a single layout state and fluid CSS structure.
  - Preserve desktop widths `272px` expanded and `60px` collapsed.
  - Reuse accessible Sheet/focus behavior for mobile without inheriting an incompatible SaaS visual style.
  - Use `dvh/svh` appropriately and account for browser safe areas.

- [x] **CC-2002 — Navigation model**
  - Create one typed navigation definition used by sidebar, breadcrumb and command palette.
  - Remove duplicated path-to-active-ID and breadcrumb mappings.
  - Filter navigation using verified render context without embedding business authorization logic.
  - Ensure keyboard navigation and focus state meet WCAG AA.

- [x] **CC-2003 — ContextSwitcher**
  - Combine Zone and workspace selection behavior behind one explicit Console context model.
  - Desktop renders compact inline controls; narrow screens expose the same controls in the navigation Sheet/header menu.
  - A context transition cancels old requests and prevents stale responses from the previous context winning.

- [ ] **CC-2004 — Console application patterns**
  - Implement/reuse `ConsolePageHeader`, `IntegratedFilterBar`, `ResourceTable`, `DividedPane`, `DetailSection`, `StatusIndicator`, `ConsoleDialog` and `AsyncTableState` only where there are concrete consumers.
  - Preserve Divider-First and avoid card-in-card layouts.
  - Status always has text/icon in addition to color.

- [ ] **CC-2005 — Resource table responsiveness**
  - Define per-table required, optional and overflow columns.
  - Use controlled horizontal scrolling and sticky identity/action columns where it improves operation safety.
  - Move secondary fields into the detail pane on narrow screens; do not silently hide critical status or destructive actions.
  - Keep compact row density and Table-First behavior.

- [ ] **CC-2006 — Modal and drawer convergence**
  - Replace hand-written overlays with the shared Dialog/Sheet primitives.
  - Standardize focus trap, escape behavior, scroll locking, maximum height and destructive confirmation.
  - Keep shadow limited to elevated layers as required by `DESIGN.md`.

**Phase gate**

- No content overlaps the sidebar/header at the responsive matrix sizes.
- Zone/workspace switching is available on desktop and narrow screens.
- New resource pages can be composed without copying layout or modal implementations.

### Phase 3 — HTTP, query and cache ownership

Goal: make remote state ownership, invalidation and failure behavior explicit.

- [x] **CC-3001 — HTTP client boundary**
  - Consolidate same-origin requests, credentials, abort handling and typed `APIError` behavior.
  - Preserve exact serialized bodies for critical request signing.
  - Validate high-risk boundary payloads at runtime instead of relying only on TypeScript casts.
  - Classify retryable network/429/5xx errors separately from 400/401/403 and invariant violations.

- [x] **CC-3002 — Feature-local API ownership**
  - Move domain APIs from the flat `lib/api` directory into their owning features.
  - Keep only the HTTP transport in `shared/api`.
  - Delete unused/misleading parameters; for example, do not accept user/Zone identifiers that are intentionally derived by the backend and never sent.

- [x] **CC-3003 — Scoped query keys**
  - Define small query-key builders inside each feature.
  - Scope keys by the minimum identity/context tuple required by the backend result: session generation, tenant, Zone, workspace and resource ID.
  - Never rely on names such as `users`, `roles` or `workspaces` as globally safe cache identities.

- [x] **CC-3004 — Query policy**
  - Set stale time, garbage collection, focus refetch and retry by data class rather than one global default.
  - Durable resource lists may be stale briefly but must revalidate after mutations/reconnect.
  - Runtime soft state expires quickly and must visibly become stale when updates stop.
  - Mutations are not automatically retried unless the operation has an idempotency key and explicit retry semantics.

- [x] **CC-3005 — Remove parallel caches**
  - Replace module-lifetime Zone cache with a bounded TanStack Query entry and explicit invalidation.
  - Keep object list caching under one owner and scope it by principal/context/bucket.
  - Remove duplicated local state that mirrors query data without a form/editing reason.

- [ ] **CC-3006 — Mutation consistency**
  - Use stable operation/idempotency IDs when the API contract supports them.
  - Apply optimistic updates only when rollback is safe and fully specified.
  - Prefer invalidation/refetch for ambiguous lifecycle transitions.
  - Prevent late mutation responses from writing into a changed session/context.

**Phase gate**

- Every remote datum has one cache owner and a documented invalidation trigger.
- Zone `active/draining` changes become visible within a defined bounded interval.
- No unscoped query survives a principal/context change.

### Phase 4 — Realtime core and recovery

Goal: centralize connection mechanics while leaving domain reconciliation inside the owning feature.

- [x] **CC-4001 — Realtime client lifecycle**
  - Maintain one Centrifugo client per verified principal.
  - Do not expose the raw client through React Context.
  - Surface explicit states: disabled, connecting, connected, reconnecting, unauthorized and degraded.
  - Disconnect and clear subscription state before switching principal.

- [x] **CC-4002 — Typed realtime contracts**
  - Define a closed event map for notification and runtime streams.
  - Validate channel binding, event type, optional schema version, IDs, payload size and event-specific timestamps/revisions.
  - Invalid events are dropped with redacted telemetry; the browser has no DLQ and must not retry malformed publications indefinitely.

- [x] **CC-4003 — Subscription registry**
  - Make registration/unregistration idempotent.
  - Bound listener and dedupe memory.
  - Prevent duplicate listeners across reconnects, React Strict Mode remounts and identity changes.
  - Isolate a failing callback so it cannot block other subscribers.

- [x] **CC-4004 — Ordering and dedupe**
  - Dedupe durable hints by stable `event_id/operation_id` using a bounded TTL/LRU set.
  - Apply resource updates only when revision/observed timestamp is newer than the cached value.
  - Do not assume global ordering; scope ordering to the relevant aggregate/resource.

- [x] **CC-4005 — Reconnect reconciliation**
  - On reconnect, invalidate or refetch durable queries touched by potentially missed events.
  - Re-register runtime watch/lease and fetch a fresh bounded snapshot.
  - Display runtime state as stale/unknown during a gap; never preserve a false healthy state indefinitely.

- [x] **CC-4006 — Browser backpressure**
  - Coalesce high-frequency runtime metrics by resource and render at a bounded cadence.
  - Cap notification preview/toast queues and retain durable history through paginated HTTP queries.
  - Drop superseded soft-state samples rather than enqueueing every update.

- [ ] **CC-4007 — Feature adapters**
  - `notifications/realtime.ts` maps allowed job notification hints.
  - `storage/realtime.ts` handles bucket runtime snapshots without credentials.
  - `mail/realtime.ts` handles durable invalidation hints and runtime watch updates separately.
  - Components consume feature hooks, not raw event strings.

**Phase gate**

- Reconnect, duplicate, out-of-order and malformed-event tests pass.
- One physical connection serves all feature subscriptions for the principal.
- Loss of realtime never becomes loss of durable business state.

### Phase 5 — Feature-by-feature migration

Goal: migrate in vertical slices so each completed slice is simpler and independently verifiable.

- [ ] **CC-5001 — Notifications and activity** *(API/query/realtime split shipped; cursor pagination UI and read-path tests remain)*
  - Split query/mutation, realtime adapter and drawer presentation.
  - Use paginated TanStack Query data for history.
  - Remove global custom DOM events where a feature-owned operation state is sufficient.
  - Remove dead custom toast state and use one toast system.
  - Keep notifications and self-activity as separate data contracts/views.

- [ ] **CC-5002 — Object Storage** *(Gateway metadata slice shipped; presigned data-ticket route and failure tests remain)*
  - Split `ObjectsTab` into object query/actions, selection/navigation state and focused components.
  - Add a typed access-session API client and a bounded in-memory session owner;
    refresh before expiry without racing a principal/Zone/workspace switch.
  - Route list, head, metadata, tag read/write and bulk operations through the
    Zone Gateway with the opaque access-session handle. Do not send actor,
    owner, workspace or Zone values as authorization claims.
  - Use presigned data-tickets for upload/download; bind each ticket to the
    bucket, object key, method, content constraints and short expiry.
  - Remove `requestBucketStsToken`, `decodeObjectStsResponse`, credential state,
    direct authenticated `S3Client` construction and the
    `storage.object.sts` notification redaction branch.
  - Remove AWS SDK modules that become unused after the Gateway/presign slice;
    retain only a dependency that has a verified remaining consumer.
  - Add cancellation and bounded concurrency for upload/delete/head/tag operations.
  - Make bulk operations idempotent where the backend/external API allows it and report partial failure clearly.
  - Add explicit `preparing`, `ready`, `expired`, `revoked`, `forbidden`,
    `Zone unavailable` and `Gateway degraded` states; never fall back to direct
    MinIO or STS.

- [ ] **CC-5003 — Mail**
  - Preserve separate durable job notification and runtime soft-state streams.
  - Centralize mail query keys and revision fences.
  - Keep immutable template version presentation and consumer runtime watch semantics explicit.
  - Rehydrate durable template/consumer state after reconnect instead of trusting missed publications.

- [x] **CC-5004 — Workspaces and Zones**
  - Replace the monolithic workspace page with feature queries, resource table and focused create/delete dialogs.
  - Make context switch sequencing race-safe.
  - Show both `active` and `draining` Zones according to the backend catalogue contract; do not derive availability solely from stale client cache.

- [ ] **CC-5005 — IAM and RBAC** *(feature ownership and list screen split shipped; permission-tree/test extraction remains)*
  - Extract permission tree/domain logic from route components.
  - Reuse the same permission tree implementation for create, read and edit modes.
  - Keep critical reset/status/role mutations on the critical proof boundary where required.
  - Preserve hierarchy and backend-authoritative authorization semantics.

- [ ] **CC-5006 — Tenants, overview and authentication screens**
  - Migrate remaining pages to the same API/query/error conventions.
  - Keep overview as an allowed lower-density exception under `DESIGN.md`.
  - Keep login/activation flows isolated from authenticated Console state and cache.

For every feature task above:

1. Move domain API/types/query keys into the feature.
2. Make `page.tsx` a thin access boundary and composition layer.
3. Use established Console patterns without changing the feature's business behavior.
4. Add unit, component and failure-path tests before deleting the old implementation.
5. Delete old code in the same slice; do not leave permanent compatibility wrappers.

**Phase gate**

- No migrated route duplicates shell, filter, table, dialog or realtime infrastructure.
- Domain behavior remains discoverable inside one named feature directory.
- Complex route pages are thin composition files; justified exceptions are documented.

### Phase 6 — Quality, performance and failure testing

Goal: establish production gates rather than relying on manual visual confidence.

- [x] **CC-6001 — Static quality gate**
  - Reach zero ESLint errors.
  - Remove unused imports/state, `any` at transport boundaries and state-mirroring effects.
  - Treat new warnings as CI failures after the baseline cleanup.

- [ ] **CC-6002 — Unit tests** *(realtime contract tests shipped; session/query/component coverage remains)*
  - Query-key scoping and session teardown.
  - Realtime decoder, dedupe, revision ordering and bounded registry.
  - Permission rendering helpers and context transition reducers.

- [ ] **CC-6003 — Component tests**
  - Console shell/navigation at responsive breakpoints.
  - Resource table keyboard behavior, column priority and detail pane.
  - Notification drawer hydration/realtime merge/read actions.
  - Destructive dialog focus, cancellation and retry behavior.

- [ ] **CC-6004 — End-to-end tests**
  - Login -> select Zone/workspace -> navigate resource pages -> logout.
  - Logout user A -> login user B without stale data leakage.
  - Centrifugo disconnect/reconnect with HTTP reconciliation.
  - Zone transition to draining while the Console remains open.
  - 401 storm and concurrent inflight request cancellation.
  - Create access session -> wait/reconcile readiness -> list/head/tag/bulk via
    Gateway -> upload/download via presigned ticket.
  - Verify the browser never receives an S3 access key, secret key or session token.

- [ ] **CC-6005 — Accessibility**
  - WCAG AA contrast and non-color status signals.
  - Keyboard-only navigation, focus visibility and focus restoration.
  - Dialog/Sheet screen-reader labels and table semantics.
  - Reduced-motion behavior for non-essential animations.

- [ ] **CC-6006 — Performance budgets**
  - Record per-route bundle baseline before migration.
  - Remove browser dependencies made obsolete by the storage contract, especially AWS SDK modules if presigned operations replace them.
  - Verify that runtime bursts do not create unbounded renders or memory growth.
  - Track Web Vitals and route transition latency without logging identity or business payloads.

- [ ] **CC-6007 — Failure injection**
  - Simulate HTTP timeout/429/5xx, Centrifugo outage, stale/out-of-order events and aborted context switches.
  - Verify bounded retry/backoff, degraded UI state and recovery after service failover.
  - Ensure destructive mutations do not double-submit during retry/reconnect.

**Phase gate**

- Static, unit, component, E2E and accessibility gates pass in CI.
- Performance measurements show no regression against the Phase 0 baseline.
- Failure injection demonstrates bounded resource use and deterministic recovery.

### Phase 7 — Cleanup, documentation and rollout

Goal: remove transitional debt and ship with observable, reversible rollout behavior.

- [ ] **CC-7001 — Dead code and dependency cleanup** *(flat API/cache/STS paths and unused packages removed; final generated-primitive audit remains)*
  - Remove unused duplicate sidebar/modal/table implementations.
  - Remove dead realtime/toast state and obsolete cache helpers.
  - Remove unused packages only after import/build verification.

- [x] **CC-7002 — Documentation**
  - Update `DESIGN.md` only for deliberate visual contract improvements.
  - Update God Views for access-session/Gateway/presign behavior,
    notification/runtime separation or any other topology change.
  - Add a short contributor guide showing how to add one resource page, query key and realtime adapter.

- [ ] **CC-7003 — Production rollout**
  - Roll out by vertical slice or canary deployment, not as an unobservable all-at-once replacement.
  - Ensure immutable static asset caching and no caching of authenticated HTML/API responses at the wrong boundary.
  - Verify reconnect behavior across rolling deployment of Next/Centrifugo/Envoy replicas.
  - Monitor client error rate, 401/403 rate, reconnect count, query retry count and route Web Vitals with a cardinality budget.

- [ ] **CC-7004 — Rollback readiness**
  - Keep release rollback at deployment/artifact level.
  - Do not keep insecure legacy transport paths merely to support UI rollback.
  - Contract migrations that remove secret-bearing events must be staged so old producers are drained before consumers reject the old schema.

**Phase gate**

- No permanent compatibility wrapper or duplicate implementation remains.
- Documentation, God View, deployed contract and code describe the same topology.
- Production dashboards and rollback procedure are verified.

## 8. Cross-service dependencies

| Dependency | Required work |
| --- | --- |
| ACR / Envoy | Verified session behavior, CSRF/origin enforcement, internal-header stripping, Centrifugo auth/channel authorization and security headers. |
| Controlplane / Zone Edge Gateways | Deploy and verify access-session projection, Control Edge list/head/tag/bulk routes and short-lived Public Edge presigned data-ticket routes. |
| JO / Notification Service | Notification events must exclude secrets and preserve stable operation/event identifiers. |
| Centrifugo | Principal-bound channels, disconnect/reconnect behavior and payload-size limits. |
| God Views | Update every changed STS, notification, runtime or session/cache workflow in the same change-set. |

Cross-service tasks are blockers for the affected Cloud Console slice. They are not authorization to silently change another service outside the agreed contract.

## 9. Risk register

| Risk | Severity | Control |
| --- | --- | --- |
| Cross-user Query cache after logout/login | P0 | Atomic session teardown, scoped keys and late-response fencing. |
| STS secret carried in notification payload | P0 | Dedicated authenticated one-time retrieval or presigned operation; update God View. |
| Realtime duplicate/out-of-order event | P1 | Bounded dedupe plus aggregate revision/timestamp fence. |
| Missed event during reconnect | P1 | HTTP reconciliation and runtime watch refresh. |
| Runtime event render storm | P1 | Coalescing, bounded cadence and queue caps. |
| Stale Zone status from module cache | P1 | Query TTL, invalidation and focus/reconnect revalidation. |
| Responsive refactor drifts into SaaS/card UI | P1 | DESIGN.md review and visual regression matrix. |
| Over-generalized shared component hides domain behavior | P2 | Promote only proven repeated patterns; keep feature adapters local. |
| Large storage browser bundle and credential exposure | P1 | Prefer backend presigning and remove unused AWS SDK paths. |
| Refactor changes authorization semantics | P0 | Backend remains authoritative; permission and forged-context E2E tests. |

## 10. Definition of done

A phase or feature is not complete until all applicable items below are satisfied:

- The implementation follows `DESIGN.md`: data-centric, Divider-First, compact and Table-First.
- Route files are composition layers and ownership is obvious from directory/file names.
- No unbounded helper, listener, cache, retry loop or client-side queue exists.
- Session/context transitions cancel and fence old work.
- Query keys and caches cannot cross principal, tenant, Zone or workspace boundaries.
- Realtime tolerates duplicate, reordering, disconnect and missed soft-state samples.
- No secret or sensitive payload is persisted or logged.
- Responsive, keyboard and WCAG AA checks pass.
- ESLint, TypeScript and relevant tests pass.
- God View is updated for every changed workflow contract/topology.
- Deployment, monitoring and rollback behavior are documented.

## 11. Recommended delivery sequence

The safest first implementation batch is:

1. Phase 0 baseline and STS contract decision.
2. Phase 1 session/cache isolation and secret-path removal.
3. Phase 2 ConsoleShell plus one responsive resource table.
4. Phase 3 query ownership.
5. Phase 4 realtime core.
6. Phase 5 Notifications, then Storage, Mail, Workspaces/Zones, IAM/RBAC.
7. Close Phase 6 gates continuously and finish with Phase 7 cleanup/rollout.

Do not start mass file movement before Phase 1 security invariants and Phase 2 application patterns are stable; otherwise the refactor will only relocate the current coupling.
