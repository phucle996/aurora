# Vault Connection and Cryptographic Bootstrap

Vault is the source of truth for sensitive connection configuration and
purpose-scoped cryptographic operations. Application configuration contains only
Vault bootstrap/authentication material and non-secret pool policy.

```text
process start
  -> read application environment and Vault bootstrap identity
  -> authenticate as the workload-specific Vault role
  -> read fixed capability record
  -> validate endpoint, TLS, credentials, and schema
  -> construct and ping the scoped client/pool
  -> report readiness
```

Each process has one Vault client. A connector never accepts a secret path from
HTTP input, a ConfigMap, or user data. In production, Kubernetes workload auth,
Vault policy, database roles, Redis ACLs, and NetworkPolicy enforce the same
least-privilege boundary.

## Capability rules

- KV records are consumer-neutral; access is constrained by authenticated
  workload policy, not a consumer field inside the record.
- Central applications use dedicated roles for Controlplane, ACR, Cost API,
  Cost Engine, Job Orchestrator, and Notification Service. JO separates CDC
  read, dispatch-marker write, and result-settlement write capabilities.
- Dataplane does not receive Central PostgreSQL, Redis, or Vault capability.
  Zone Kafka/NATS/JetStream KV configuration stays Zone-local.
- Transit keys remain in Vault. New writes use current versions; old versions
  survive only while decrypt/verify compatibility requires them.
- Secret body, DSN, password, OAuth credential, and private key are never in
  Kubernetes ConfigMap, business storage, logs, Kafka, or telemetry.

Malformed/missing records fail startup closed. Vault outage after a healthy
startup does not tear down existing pools. Credential rotation writes a new
record version, rolls/drains consumers, then revokes the old credential.

Local Compose uses one app-owned ignored `.env` file per first-party process;
the tracked `.env.example` documents it. Deployable images are pulled from GHCR
and never built by Compose.

