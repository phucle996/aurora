# Hypervisor global base-price version publish

`POST /api/v1/billing/critical/hypervisor/pricing-schedules/:code/versions`

The transport owns JSON validation: every BIGINT bracket field is a decimal string and
`effective_from` is normalized to UTC. The Hypervisor pricing workflow owns the
business checks, obtains the Hypervisor-only schedule through a CTE fenced by
`module_code='hypervisor'`, writes the immutable version, scalar brackets and durable
outbox row in one transaction, then emits its immediate Hypervisor invalidation
hint. Hypervisor's own outbox loop republishes the Engine fact and its own cache
hint; it never claims another module's rows or routes another module's channel.

The request is flat: version metadata is one command and `brackets[]` is a separate
flat collection. No nested pricing aggregate is persisted or passed across layers.

Hypervisor owns its binary L2 snapshots (one-hour TTL), one-minute L1, and
module-only invalidation channel. The warm-up loop runs every 15 seconds, not
every hour: an invalidation can delete L2 long before TTL expiry. Controlplane
reads and validates these Protobuf snapshots directly; the old JSON readiness
stream and separate readiness projection no longer exist.
