# Adding a Cloud Console resource page

Use this checklist after reading `DESIGN.md` and `REFACTOR_PLAN.md`.

## Ownership

- Keep `app/**/page.tsx` as an access boundary and composition layer.
- Put domain API, runtime decoders, query keys, realtime adaptation and focused
  components under `src/features/<domain>/`.
- Keep only transport mechanics in `src/shared/api`, server-cache policy in
  `src/shared/query`, session lifecycle in `src/session` and connection
  lifecycle in `src/realtime`.
- Do not add `helpers.ts`, a second server-state cache or a raw broker client.

## Query and mutation rules

Start every authenticated query key with `useConsoleQueryScope()`. The scope is
a browser cache fence; actor, workspace and Zone authorization still comes from
the verified backend session.

Use `fetchJSON` for same-origin Envoy routes. Decode high-risk responses at the
feature boundary. Abort queries on unmount/context transition and do not retry
mutations unless the backend contract supplies an idempotency key and defines
safe retry semantics.

After a mutation, update cache optimistically only when rollback is complete and
unambiguous. Otherwise invalidate the smallest durable query prefix and refetch.

## Realtime rules

Add a closed event contract in `src/realtime/contracts.ts`, then expose a
feature-owned hook that subscribes through `useRealtime()`. Components must not
receive the Centrifugo client or raw channel.

Treat publications as hints. Dedupe and revision-fence them, bound memory and
render cadence, and rehydrate durable truth through HTTP after reconnect.

## UI and validation

Preserve the Console's compact, divider-first and table-first design. Define
required/optional columns, keep identity/status/destructive actions reachable on
narrow screens, and use existing Dialog/Sheet primitives for elevated layers.

Before handoff run:

```bash
npm run lint -- --format stylish
npx tsc --noEmit --pretty false
npm run build
```

Also run the feature's unit/component/failure tests. If a transport, trust
boundary or business contract changed, update the corresponding God View in the
same change-set.
