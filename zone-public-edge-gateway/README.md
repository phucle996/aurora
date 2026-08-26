# Zone Public Edge Gateway

This Envoy deployment is the public data boundary for a Zone. It streams
presigned object/image transfers and the scoped Zone runtime SSE read plane.

Security invariants:

- Chỉ có một `zone-public-edge-authorizer` process/deployment. Bên trong binary,
  Storage access và generic runtime read là hai workflow tách biệt; `main.rs` chỉ
  wire dependency và dispatch route. Mỗi workflow có semaphore budget riêng nên
  một đợt runtime reconnect không làm cạn concurrency của Storage authorization.
- Envoy never receives Zone NATS credentials or Central cookies. Its authorizer
  receives a short-lived ACR runtime assertion, verifies the signature and
  exact request binding, checks the Zone-local resource registration, then CAS
  claims the assertion `jti` in the 30-second `AURORA_ZONE_RUNTIME_REPLAY` KV.
- The authorizer NATS credential may read `AURORA_ZONE_CONFIG` and may create
  keys only in `AURORA_ZONE_RUNTIME_REPLAY`; it does not mutate resource heads.
- It strips every caller-controlled `x-aurora-*` header. Only the reviewed
  runtime assertion reaches the authorizer; only authorizer-injected scope
  reaches `zone-runtime-stream`.
- MinIO is the S3-compatible object store. Envoy applies an internal SigV4
  signature after route/path rewrite, using only the Zone-local signer
  credentials injected through `AWS_ACCESS_KEY_ID` and
  `AWS_SECRET_ACCESS_KEY`; it never queries AWS IAM or instance metadata.
- It has no network path to Dataplane, Proxmox or Zone Control Authorizer.
  The runtime route can reach only `zone-runtime-stream`.
- Access logs intentionally omit the request path/query because presigned URLs
  carry credentials in their query string.
- Request retries are disabled. Clients retry uploads with multipart semantics.

Runtime routes use the generic contract
`/zone-public/v1/runtime/{module}/{resource_type}/{resource_id}/{panel}[/{component_id}]`.
The authorizer resolves `{module}.{resource_type}.head.{resource_id}` in
`AURORA_ZONE_CONFIG` and requires a flat schema-v1 registration with
`runtime_read_enabled=true`, matching module/resource/owner/workspace/Zone scope,
a positive version and no tombstone. Adding a conforming module therefore does
not add an Edge route branch or a new authorizer service.
