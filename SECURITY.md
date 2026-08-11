# Security Policy

Aurora handles identity, session state, billing state, infrastructure commands,
and Zone-local credentials. Any change to authentication, authorization,
cryptography, transport contracts, payment, edge gateways, or secret
boundaries must be treated as security-sensitive.

## Supported versions

| Version | Security support |
| --- | --- |
| `main` / latest release | Supported |
| Older commit or release | Only when confirmed by a maintainer |
| Fork not maintained by Aurora | Not supported |

The repository follows a mainline development model. Security fixes are
applied to the supported branch; backporting to every older commit is not
assumed.

## Reporting a vulnerability

Do not create a public issue, discussion, pull request, or log paste containing
exploit details, tokens, cookies, private keys, or user data.

Use GitHub Private Vulnerability Reporting when possible:

[Report a vulnerability privately](https://github.com/phucle996/aurora/security/advisories/new)

If private reporting is not enabled, contact a maintainer through a private
channel published on the GitHub profile or repository. A public issue may be
opened only to request a private contact channel; do not include vulnerability
details in it.

A report should include:

- The affected component, commit/release, and environment.
- The impact and the trust boundary that is crossed.
- Preconditions and minimal reproduction steps.
- A sanitized proof of concept with no production secret or data.
- A severity assessment, when available.
- Suggested remediation or defense-in-depth measures.
- A contact channel and attribution preference.

Do not test against production or another person's data. Do not perform
destructive actions, persistence, lateral movement, social engineering,
denial-of-service, or exfiltration beyond what is necessary to demonstrate the
impact.

## Response process

Target handling timeframes are:

1. Acknowledge receipt within three business days.
2. Triage severity and affected surfaces within seven business days.
3. Agree with the reporter on coordination, embargo, and disclosure.
4. Release a fix or mitigation according to risk.
5. Publish an advisory after supported users have had reasonable time to
   update.

These are best-effort targets, not an SLA or bug-bounty commitment. A reward is
not implied unless a separate program has been published.

## Security boundaries

### Central edge

- Browsers enter the Central API only through Envoy.
- Envoy terminates TLS, bounds body size and timeouts, and calls ACR through
  gRPC ExtAuthz.
- ACR must remove or overwrite every trusted identity/proof header supplied by
  the client.
- Static asset routes may disable ExtAuthz; API routes must not inherit that
  bypass.
- Backends still enforce domain authorization; UI hiding or ACR authentication
  never replaces backend permission enforcement.

### Identity and session

- IAM PostgreSQL is the durable identity, role, and membership Source of Truth.
- Auth-State Redis stores runtime sessions, nonces, replay fences, and rate
  limits; dependency failure must fail closed.
- Shared L2 Redis stores request/reply, cache, Pub/Sub, bounded Streams, and
  workflow locks/checkpoints only.
- Auth-State Redis and Shared Redis must use separate deployments,
  credentials/ACLs, and namespaces.
- Session/alias cookies are host-only, `Secure`, and `HttpOnly`, with the
  applicable CSRF/session-proof contract.
- Critical routes require a fresh proof; privileged mutations must not rely on
  stale authorization cache state.

### Central and Zone isolation

- Kafka is the durable Central↔Zone transport; NATS Core carries soft state
  only.
- NATS JetStream KV is a database belonging to one Zone, not a Central event
  bus.
- A Zone A Dataplane must not subscribe to Zone B commands or metadata.
- JO has no Zone KV credential or Zone HPKE private key.
- Dataplane has no Controlplane/Billing PostgreSQL, Auth Redis, Shared Redis, or
  Vault credential.
- Notification Service and Cost Engine must not receive NATS/Zone KV
  credentials unless a workflow explicitly requires them.

### Protected Zone payload

- Controlplane serializes the complete domain command and seals it with HPKE
  using the active public key for the target Zone.
- JO validates public envelope metadata and relays byte-identical ciphertext; it
  does not decrypt the payload.
- Dataplane loads the private key from a read-only Zone-local file mount. The
  key must not be placed in environment variables, images, PostgreSQL, Redis,
  Kafka, Zone KV, logs, or traces.
- AAD binds the key, Zone, source domain, job topic, resource, job version, and
  schema version.
- At-least-once retries reuse the committed ciphertext; plaintext must not be
  rebuilt from a non-authoritative projection.
- A DLQ contains only sanitized metadata, byte length, and a fingerprint when
  needed; it must not copy the raw protected payload.

### Zone edge separation

- Zone Public Edge does not receive Central cookies, ACR assertions, Zone KV
  credentials, or private control identity.
- Public Edge routes only allow-listed data/read capabilities. Public Authorizer
  CAS-consumes the ticket before MinIO/SigV4 upstream access.
- Zone Control Edge accepts only the Central Envoy workload identity over mTLS.
- Zone Control Authorizer verifies the signed assertion, replay/request binding,
  and Zone access; it does not infer ownership by itself.
- Runtime Stream queries a fixed allow-list from Zone Victoria; a browser may
  not supply arbitrary PromQL/LogsQL.

### Billing

- Billing PostgreSQL is separate from Controlplane PostgreSQL.
- Cost Manager does not query the Controlplane database directly.
- Owner/user/tenant context is derived from trusted identity; a client-provided
  owner ID is not billing authority.
- Wallet mutation and the immutable ledger insert must commit in one
  transaction.
- Payment webhooks verify the signature over the exact raw body, enforce a
  timestamp window, and use an idempotency key.
- Money uses fixed integer micro-units; floating point is not used for
  settlement.
- Storage metering is Zone-local: Public Edge emits only the bounded
  `log_type=metering`/`module=storage` envelope after upstream completion.
  The event contains no ticket, cookie, access secret, Authorization header, or
  object key.
- Zone ClickHouse is a journal/aggregation store, not a payer authority. The
  only charge-producing path is Zone report outbox -> Kafka -> Job Orchestrator
  -> Shared Redis -> Cost Engine -> Billing PostgreSQL.
- A report is bound to one Zone and one closed UTC hour. Cost Engine resolves
  ownership and pins pricing in Billing PostgreSQL, then mutates wallet and
  immutable ledger atomically. Duplicate delivery is replay-safe; unsigned
  corrections are quarantined and never rewrite settled history.

### Telemetry and projection

- Victoria, the Scylla timeline, and realtime channels are not business
  aggregate Sources of Truth.
- Logs, metrics, and traces must not contain raw tokens, cookies, passwords,
  private keys, customer credentials, protected payloads, rendered Secrets, or
  presigned queries.
- User/resource/workspace/Zone UUIDs, raw paths, Redis keys, SQL text, and error
  strings must not become unbounded metric labels.
- Telemetry failure must not change the durable business outcome.

## Secret handling

- Never commit `.env` files, private keys, access tokens, refresh tokens, Vault
  responses, TLS private keys, or Dataplane keyrings.
- Commit only `.env.example` files with non-sensitive placeholders.
- Production uses workload identity/AppRole/Kubernetes auth and least-privilege
  capability records; it does not use local static Vault tokens.
- Each service receives only the secrets for the downstream systems it owns.
- Redact secrets from `Debug`, error responses, structured logs, and test
  fixtures.
- Rotation must preserve the required overlap/fencing window; do not retire a
  key while retained outbox/projection records still reference it.

If a real secret is committed:

1. Revoke or rotate it immediately; removing it from Git history does not make
   the old secret safe again.
2. Identify where it was used and audit access.
3. Replace the credential in every deployment and consumer.
4. Sanitize the repository/history only after the credential has been rotated.
5. Record the incident or advisory through a private channel.

Fixed values in Docker Compose and Vault bootstrap are development-only and
must not be reused in staging or production.

## Secure development requirements

### Input and authority

- Validate size, schema, enum, UUID, timestamp, and Zone/tenant/workspace scope
  at the boundary.
- Never derive authority from a path, body, query, or header controlled by the
  client.
- Normalize before signing, hashing, or comparing; signed messages must use a
  versioned canonical format.
- Unsupported routes, schemas, and capabilities must fail closed.

### Durable workflows

- Commit aggregate mutation and its outbox record in one transaction.
- A Kafka/Redis Stream consumer ACKs or commits only after a durable side effect
  or durable sanitized quarantine.
- At-least-once handlers must be idempotent and use a stable identity,
  version/generation fence.
- An external mutation must not be automatically retried unless idempotency is
  proven by contract.
- A lock reduces concurrency; version/generation/fencing tokens are the
  correctness boundary.

### Cryptography

- Do not design primitives or downgrade algorithms/TLS.
- Use the canonical shared contract and cross-language fixtures in
  [`proto/`](./proto/).
- Version key IDs, algorithm suites, AAD, and rotation state.
- Prefer constant-time or verified library APIs for secret comparison and
  signature validation.

### Dependencies and infrastructure

- Pin image and dependency versions according to release policy; avoid `latest`
  in production.
- A security-sensitive dependency update must run the related
  unit/integration/contract tests.
- NetworkPolicy and ACLs must deny by default and open only the exact required
  ingress/egress.
- Production transport must enable TLS/mTLS/SASL according to the contract; do
  not silently downgrade to plaintext.
- Dev-only mode, root credentials, automatic schema creation, and open CORS
  must not enter production manifests.

## Review checklist

A security review is mandatory when changing:

- ACR, IAM, sessions/cookies, MFA, OAuth, RBAC, or trusted headers.
- Vault policy/path, Redis ACL/namespace, or workload identity.
- Protobuf fields/schemas, Kafka topics, NATS subjects, or DLQ payloads.
- HPKE/AAD/key lifecycle or the Dataplane key mount.
- Envoy routes/filters, Zone Edge, NetworkPolicy, or CORS.
- Wallet/ledger/payment/ownership logic.
- Logging, telemetry attributes, or data retention.

A security-sensitive pull request should state the threat model, authority
source, failure semantics, replay/race behavior, migration/cutover plan, and
verification evidence.

## Disclosure

Aurora coordinates responsible disclosure. Reporters are credited when they
want attribution and doing so does not add risk. Do not publish exploit details
until a fix or mitigation is available to supported users.

Detailed security designs by workflow live in [`god_view/`](./god_view/); the
system architecture is documented in [`ARCHITECTURE.md`](./ARCHITECTURE.md).
