# Zone Public Edge Gateway

This Envoy deployment is the public data boundary for a Zone. It streams
presigned object/image transfers and can host explicitly approved public
WebSocket/data-plane routes later.

Security invariants:

- It never receives Zone NATS credentials, Central cookies or ACR assertions.
- It strips every `x-aurora-*` header and browser-only security headers before
  forwarding.
- MinIO is the S3-compatible object store. Envoy applies an internal SigV4
  signature after route/path rewrite, using only the Zone-local signer
  credentials injected through `AWS_ACCESS_KEY_ID` and
  `AWS_SECRET_ACCESS_KEY`; it never queries AWS IAM or instance metadata.
- It has no network path to Dataplane, Proxmox or Zone Control Authorizer.
- Access logs intentionally omit the request path/query because presigned URLs
  carry credentials in their query string.
- Request retries are disabled. Clients retry uploads with multipart semantics.
