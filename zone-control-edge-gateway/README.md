# Zone Control Edge Gateway

The Zone Control Edge Gateway is a private Envoy boundary. Only Central Envoy
workload identities may connect over mTLS. It accepts bounded control requests,
invokes `zone-control-authorizer` through mTLS ExtAuthz and then routes an
allow-listed capability to the corresponding private Zone service.

The Rust authorizer lives under `authorizer/`. It verifies the generic
`zone-control-edge-gateway` assertion envelope and dispatches capability policy;
it is not itself a gateway or an ownership database.

## Provisioning

1. Create the dedicated asymmetric Vault Transit key
   `transit/keys/zone-control-assertion`.
2. Configure ACR with `VAULT_ZONE_CONTROL_ASSERTION_KEY_PATH` and the exact
   versioned `VAULT_ZONE_CONTROL_ASSERTION_KEY_ID`.
3. Export only overlapping versioned public keys into the Zone Secret consumed
   as `ZONE_CONTROL_ASSERTION_PUBLIC_KEYS`. Never mount a Vault token or private
   key into the Zone.
4. Issue separate mTLS identities for Central Envoy → Control Gateway and
   Control Gateway → Control Authorizer. Certificates must carry the exact DNS
   SANs `central-envoy`, `zone-control-edge-gateway` and
   `zone-control-authorizer` required by the Envoy validation contexts.
5. Give the authorizer read-only NATS credentials limited to
   `AURORA_ZONE_ACCESS`.
6. Create the `zone-control-minio-signer` Secret from a dedicated MinIO service
   account. Its policy may cover only the object/list/tag/delete operations
   exposed by reviewed Control Gateway routes; never use MinIO root credentials.
7. Apply the Control Gateway, Control Authorizer and Public Gateway manifests
   as separate Deployments and NetworkPolicy subjects.

## Failure semantics

- Missing assertion key, mTLS identity, Zone KV or authorizer capacity fails
  closed. Dependency/not-ready/overload failures surface as retryable 503;
  forged or scope-mismatched requests remain 403.
- The control body limit is 64 KiB. Large bytes go through a presigned request
  on the Zone Public Edge Gateway.
- Envoy does not retry a routed control mutation. A retry arrives with a new
  assertion and must be idempotent at the downstream `operation_id` boundary.
- S3-compatible signing is an upstream SigV4 filter, after route/path rewrite.
  The signer credential is Zone-local and is never returned to Central or the
  client. Envoy's provider chain is explicitly restricted to the injected
  environment credentials; it must not query AWS instance metadata.
- A non-empty `key_prefix` requires the ListBucket query to contain exactly one
  decoded `prefix` value within that scope. List routes accept only the
  reviewed ListObjectsV2 query fields; S3 subresource queries fail closed.
- Object-version queries and `VersionId` bulk-delete elements are denied until
  separately authorized version capabilities exist.
- The authorizer replay cache is per-process defense in depth, not distributed
  exactly-once.
- Existing presigned transfers continue until their TTL if the Control Edge is
  unavailable; no new tickets/control operations can be issued.
