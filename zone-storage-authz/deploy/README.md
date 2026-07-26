# Zone Storage Authz deployment notes

1. Create `transit/keys/storage-assertion` as an Ed25519 key in Vault and
   configure ACR with `VAULT_STORAGE_ASSERTION_KEY_PATH` and the exact versioned
   `VAULT_STORAGE_ASSERTION_KEY_ID`.
2. Export only the public key for that version into the Zone Secret
   `storage-assertion-public-keys` as JSON (`{ "storage-assertion:v1": "..." }`).
   Never mount a Vault token or private key into this service.
3. Create the mTLS identities for Central Envoy, Zone Envoy and this service.
   `ZONE_STORAGE_AUTHZ_TLS_CLIENT_CA` must trust only Zone Envoy's client CA;
   NATS credentials must be read-only for `AURORA_ZONE_ACCESS`.
4. Render `envoy-zone-storage.yaml` into the Zone Envoy ConfigMap and apply the
   two manifests under `k8s/`. Keep MinIO/S3 ports private.

The Envoy AWS signing extension is deliberately behind the ExtAuthz boundary.
Validate the exact Envoy/MinIO image pair for body hashing, buffering and retry
semantics before enabling chargeable production traffic.

