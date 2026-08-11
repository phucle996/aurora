# Zone Edge Architecture

Aurora has two independent Envoy deployments in each Zone: Zone Public Edge
Gateway and Zone Control Edge Gateway. Zone Control Authorizer and Zone Runtime
Stream are Rust services, not gateways.

```text
Browser -- presigned object request --> Zone Public Edge --> MinIO
Browser -- runtime.read ticket ------> Zone Public Edge --> Authorizer --> Runtime Stream --> Victoria

Central Envoy + ACR -- mTLS assertion --> Zone Control Edge --> Authorizer --> private S3/API
```

## Boundaries

| Component | Owns | Does not own |
|---|---|---|
| Zone Public Edge | Public TLS, connection/body limits, named public routes | Business authorization, Zone KV, assertion keys |
| Zone Control Edge | Private Central mTLS ingress, allow-listed control routes, bounded ExtAuthz | Business ownership, Kafka execution, dynamic proxying |
| Zone Control Authorizer | Assertion verification, capability policy, Zone access-record match | Gateway routing, Central business data, provider credentials |
| Zone Runtime Stream | Fixed Victoria query shaping and bounded SSE fan-out | Business authorization, Zone KV, Kafka/NATS/PostgreSQL/Vault |

ACR creates a request-bound signed assertion only after Central authentication
and authorization. Zone Control Authorizer verifies issuer, audience, schema,
key, expiry, exact Zone/capability/path/body binding, and the matching Zone KV
access projection. It then removes assertion material before the upstream call.
In-process `jti` caching only limits local replay; mutations require stable
`operation_id` idempotency and make no distributed exactly-once claim.

Public runtime tickets use a separate `zone-public-edge-gateway` audience and
`runtime.read` capability. Public Edge strips browser scope headers/ticket
material, calls Authorizer once on open, and injects only verified bounded scope
to Runtime Stream. It never accepts a control assertion as a public ticket.

All edge deployments have bounded in-flight work, connections, bodies, and
stream lifetimes. Dependency/projection/overload failures are retryable; forged
or scope-mismatched input is denied. Public Edge cannot reach Dataplane, Zone
KV, Central transport, or private control services. Control Edge has no public
DNS and accepts only Central Envoy mTLS identity.

Exact storage-control and runtime-stream workflows live in their owning God
Views; this file defines the component topology and trust boundaries.

