# Vault-backed central connection and crypto bootstrap

## Contract

Vault is the source of truth for sensitive connection configuration and
purpose-scoped cryptographic operations. Applications call Vault directly
during startup from their infrastructure connector. The application config
contains only Vault bootstrap/auth material and non-secret pool policy.

KV records are consumer-neutral. They do not contain `owner_service`,
`consumer`, or an allow-list of workloads. Isolation is enforced by the
Vault-authenticated workload identity, policy, database role, Redis ACL and
NetworkPolicy.

Kubernetes `ConfigMap` and `Secret` objects do not contain database passwords,
Redis passwords, DSNs or OAuth secrets. A Kubernetes Secret is only a
bootstrap carrier when a workload identity cannot be used directly.

## Startup flow

```text
Pod starts
  |
  +--> read Vault bootstrap only
  |      VAULT_ADDR, workload identity/token, TLS trust
  |
  +--> authenticate as the app-specific Vault role
  |
  +--> connector GETs its fixed capability record
  |      secret/data/connections/{resource}/{capability}
  |
  +--> validate schema, endpoint, credentials and TLS contract
  |
  +--> construct client/pool and ping
  |
  `--> readiness=true
```

There is one process-scoped Vault client per application process. A connector
does not accept a path from a request, ConfigMap value or user input. Static
connection credentials are read at startup; rotation is completed by creating
the new credential, writing a KV version, rolling the workload, draining old
pools and then revoking the old credential.

## Consumer-neutral connection records

| Capability | Vault path |
|---|---|
| Central business PostgreSQL | `secret/data/connections/postgres/pg-central/role-business-rw` |
| Central CDC PostgreSQL | `secret/data/connections/postgres/pg-central/role-cdc-read` |
| Billing PostgreSQL API | `secret/data/connections/postgres/pg-billing/role-ledger-rw` |
| Billing PostgreSQL Engine | `secret/data/connections/postgres/pg-billing/role-engine-read` |
| Shared L2 request/reply | `secret/data/connections/redis/shared-l2/role-request-reply-rw` |
| Shared L2 ACR auth request | `secret/data/connections/redis/shared-l2/role-auth-request-rw` |
| Shared L2 wallet command | `secret/data/connections/redis/shared-l2/role-wallet-command-rw` |
| Shared L2 runtime bridge | `secret/data/connections/redis/shared-l2/role-runtime-bridge-rw` |
| Shared L2 notification consume | `secret/data/connections/redis/shared-l2/role-notification-consume` |
| Auth-State session | `secret/data/connections/redis/auth-state/role-session-rw` |
| Auth-State authz projection | `secret/data/connections/redis/auth-state/role-authz-projection-rw` |
| Auth-State proof | `secret/data/connections/redis/auth-state/role-proof-rw` |
| Engine checkpoint/lock | `secret/data/connections/redis/engine/role-checkpoint-lock-rw` |
| Cost Manager payment signing material | `secret/data/integrations/payment/cost-manager-api` |

All records use `schema_version: 1`. PostgreSQL records contain typed endpoint,
database and credential fields. Redis records contain typed endpoint, ACL
username/password and database index, or a URL for Rust connectors. Pool size,
timeout, retry budget, AOF durability and worker backpressure remain deployment
policy.

## App identities and policy

The bootstrap creates a separate policy and AppRole for each Central workload:

```text
controlplane-connections-read
acr-connections-read
job-orchestrator-connections-read
cost-manager-api-connections-read
cost-manager-engine-connections-read
notification-service-connections-read
```

Each policy grants `read` only on the exact capability records required by that
workload. Transit/TOTP operation paths are separately granted to the workloads
that need them. Runtime identities cannot write, delete or list unrelated
records. Production uses Kubernetes auth; local Compose may use one externally
issued token per app or AppRole credentials.

## Crypto boundary

MFA encryption and platform signing keys stay inside Transit. Purpose-scoped keys
include:

```text
transit/keys/jwt-signer
transit/keys/zone-control-assertion
transit/keys/iam-mfa-secret
```

New writes use the current key version. Older versions remain available for
decrypt/verify until data migration is complete. A KV version retaining an old
database password does not keep that password valid in PostgreSQL or Redis.

The external payment gateway currently requires the raw HMAC key for request
signing, so Cost Manager reads the two payment keys from its app-scoped KV
record into bounded process memory. They are never supplied through an
environment variable or written to business storage; rotate them by writing a
new KV version and rolling the Cost Manager replicas.

OAuth provider records remain KV because the provider SDK requires plaintext
client configuration in the bounded ACR process memory:

```text
secret/data/acr/oauth/google
secret/data/acr/oauth/github
```

## Failure and HA semantics

- Vault authentication/read retries are bounded with backoff and no secret
  body logging.
- Malformed or incomplete records fail startup closed.
- Each replica authenticates independently; a Vault read is idempotent and
  never becomes business state.
- Vault outage after startup does not tear down healthy DB/Redis pools.
- Transit operation failure is fail-closed for the dependent crypto workflow.
- Stable service endpoints are stored, never pod IPs.
- Graceful shutdown drains workers before closing pools.
- Vault ACL, DB role, Redis ACL and NetworkPolicy enforce the same boundary.

## Allowed topology update

This workflow explicitly adds direct Vault access to Controlplane, Cost Manager
API/Engine, Job Orchestrator and Notification Service. ACR remains a Vault
consumer. Dataplane services do not gain access to Central PostgreSQL, Redis or
Vault. Deployment manifests and the conceptual connection matrix must enforce
this allow-list; no generic Central Vault credential may be shared across apps.
