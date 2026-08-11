# Aurora

Aurora is a multi-Zone cloud platform monorepo. Central owns identity,
desired state, orchestration, notification, and billing. Each Zone executes
workloads for that Zone, keeps its runtime state, and emits its own telemetry.

## Documentation

| Document | Contents |
| --- | --- |
| [ARCHITECTURE.md](./ARCHITECTURE.md) | Central and Zone architecture, ownership, and runtime flows |
| [SECURITY.md](./SECURITY.md) | Security policy, trust boundaries, secret handling, and vulnerability reporting |
| [dev/README.md](./dev/README.md) | Docker Compose, networks, and volumes for local development |
| [proto/README.md](./proto/README.md) | Canonical Protobuf contracts and compatibility rules |
| [god_view/](./god_view/) | Workflow-level Source of Truth for each domain |

## Components

### Central

| Component | Technology | Role |
| --- | --- | --- |
| [Central Envoy](./dev/central/envoy/) | Envoy | TLS termination, virtual hosts, API routing, and ExtAuthz |
| [ACR](./acr/) | Rust/Tonic | Session, Trinity/Billing/SRE authentication, and trusted identity headers |
| [Controlplane](./controlplane/) | Go/Gin/gRPC | Core API and durable desired state |
| [Job Orchestrator](./job-orchestrator/) | Rust/Tokio | PostgreSQL changefeed, Kafka dispatch/result settlement, and reconciliation |
| [Notification Service](./notification-service/) | Rust/Axum | Activity/inbox projection, Redis consumers, and Centrifugo adapter |
| [Cost Manager API](./cost-manager/api/) | Go/Gin | Billing API, wallet, plan, tier, payment, and ownership |
| [Cost Manager Engine](./cost-manager/engine/) | Rust/Tokio | Usage rating, pricing runtime, and wallet-ledger debit |
| [Cloud Console](./cloud-console/) | Next.js/React | User console |
| [Admin UI](./admin-ui/) | Vite/React | SRE/platform console |
| [Cost Console](./cost-console/) | Vite/React | Billing console |

Central infrastructure consists of Controlplane PostgreSQL, Billing
PostgreSQL, Auth-State Redis, Shared L2 Redis, Kafka, NATS Core, Vault,
Scylla, ClickHouse, Centrifugo, and the OpenTelemetry/Victoria/Grafana stack.

### Zone

| Component | Technology | Role |
| --- | --- | --- |
| [Dataplane](./dataplane/) | Rust/Tokio | Per-Zone job admission, execution, fences, and result/report handling |
| [Zone Control](./zone-control/) | Rust/Axum/Tokio | Zone-wide orchestration and one-time transfer ticket issue/revoke workflows |
| [Zone Public Edge](./zone-public-edge-gateway/) | Envoy | One-time browser object transfer and runtime ingress |
| [Zone Public Authorizer](./zone-public-edge-gateway/authorizer/) | Rust/Tonic | Separate gRPC process that consumes a ticket before streaming to MinIO |
| [Zone Control Edge](./zone-control-edge-gateway/) | Envoy + Rust | Private mTLS control boundary and capability authorization |
| [Zone Runtime Stream](./zone-runtime-stream/) | Rust/Axum | SSE read plane for Zone metrics and logs from Victoria |

Local Zone infrastructure consists of NATS JetStream KV, MinIO, Stalwart,
the Zone OTel Collector, Zone ClickHouse metering journal, and
VictoriaMetrics/VictoriaLogs/VictoriaTraces. Zone Control can opt into the
closed-window report publisher (`ZONE_CONTROL_METERING_ENABLED=true`) which
persists a bounded `StorageUsageReportV1` in JetStream before Kafka relay.
Production keeps it disabled until reconciliation/cutover gates pass; Central
Cost Engine continues its current ClickHouse billing path in the meantime.

### Shared repository assets

| Path | Contents |
| --- | --- |
| [proto/](./proto/) | Canonical Protobuf source registry and cross-language fixtures |
| [dev/](./dev/) | Central/Zone Docker Compose and local infrastructure configuration |
| [k8s/](./k8s/) | Kubernetes, Argo CD, HPA/PDB, operator, and NetworkPolicy manifests |
| [god_view/](./god_view/) | Workflow-level design documents |
| [scripts/](./scripts/) | Local bootstrap and provisioning helpers |

## Run locally

### Requirements

- Docker Engine with Docker Compose V2.
- GNU Make.
- `curl` and `jq` for Vault bootstrap.
- Python 3 for the Zone keyring generator.
- A GHCR `read:packages` credential when Aurora packages are private.

### 1. Create app-owned environment files

```bash
cp controlplane/.env.example controlplane/.env
cp acr/.env.example acr/.env
cp job-orchestrator/.env.example job-orchestrator/.env
cp notification-service/.env.example notification-service/.env
cp cost-manager/api/.env.example cost-manager/api/.env
cp cloud-console/.env.example cloud-console/.env
cp cost-console/.env.example cost-console/.env
    cp dataplane/.env.example dataplane/.env
    cp zone-control/.env.example zone-control/.env
    cp zone-public-edge-gateway/authorizer/.env.example zone-public-edge-gateway/authorizer/.env
    cp zone-runtime-stream/.env.example zone-runtime-stream/.env
cp dev/central/.env.example dev/central/.env
cp dev/zone/.env.example dev/zone/.env
```

Fill in every variable that is left blank before starting. `dev/central/.env`
and `dev/zone/.env` must pin each Aurora service to a GHCR digest produced and
scanned by the release workflow. Compose does not build source locally. For a
private package, log in first with a PAT that has only the `read:packages`
scope:

```bash
echo "$GHCR_READ_TOKEN" | docker login ghcr.io -u phucle996 --password-stdin
```

Vault bootstrap creates fixed development tokens per workload in
[`dev/central/vault/vault-bootstrap.sh`](./dev/central/vault/vault-bootstrap.sh):

| File | Local-only value |
| --- | --- |
| `controlplane/.env` | `VAULT_TOKEN=aurora-dev-controlplane-token` |
| `acr/.env` | `VAULT_TOKEN=aurora-dev-acr-token` |
| `job-orchestrator/.env` | `VAULT_TOKEN=aurora-dev-job-orchestrator-token` |
| `notification-service/.env` | `VAULT_TOKEN=aurora-dev-notification-token` |
| `cost-manager/api/.env` | `VAULT_TOKEN=aurora-dev-cost-manager-api-token` |
| `cost-manager/api/.env` | `VAULT_ENGINE_TOKEN=aurora-dev-cost-manager-engine-token` |

These tokens are for local Vault development only. Set `CENTRIFUGO_API_KEY`,
MinIO, and Stalwart credentials to the development values used by Compose.
Never commit `.env` files or `dataplane/.secrets/`.

### 2. Start Central

```bash
make init-central
```

This starts Central infrastructure first, waits for Vault readiness, bootstraps
Transit/KV/policies/connection records, and then pulls and starts Central app
images. Central must start first because it creates the `aurora-dev-transport`
network.

### 3. Start the Zone

```bash
make init-zone
```

This generates the local Dataplane HPKE keyring and starts the Zone stack. Only
Dataplane replicas attach to the Central transport network to reach Kafka and
NATS Core.

### 4. Inspect status and logs

```bash
docker compose -f dev/central/compose.yml ps
docker compose -f dev/zone/compose.yml ps

docker compose -f dev/central/compose.yml logs -f controlplane1
docker compose -f dev/central/compose.yml logs -f job-orchestrator
docker compose -f dev/zone/compose.yml logs -f dataplane-vn-n1
```

These targets are safe to run again. Vault bootstrap uses idempotent requests,
and Compose always uses GHCR images with `--no-build`:

```bash
make up-central
make up-zone
```

Docker only caches layers of pulled images; Rust/Go/Node build caches and source
build contexts are not created on the local machine.

## Component releases

GitHub Actions keeps CI and release workflows separate for Central and Zone and
filters them by subproject. A pull request or branch push runs only jobs for
changed paths. Pushing a component tag builds, Trivy-scans, and publishes only
that component's GHCR image; the version tag is created only after the staging
image passes the scan.

| Tag | Image |
| --- | --- |
| `cp-v*` | `aurora-controlplane` |
| `acr-v*` | `aurora-acr` |
| `jo-v*` | `aurora-job-orchestrator` |
| `notification-service-v*` | `aurora-notification-service` |
| `cost-manager-v*` | `aurora-cost-manager` |
| `cloud-console-v*`, `admin-ui-v*`, `cost-console-v*` | Corresponding console |
| `dataplane-v*` | `aurora-dataplane` |
| `zone-runtime-stream-v*` | `aurora-zone-runtime-stream` |
| `zone-authorizer-v*` | `aurora-zone-control-authorizer` |
| `zone-control-v*` | `aurora-zone-control` |
| `zone-public-edge-authorizer-v*` | `aurora-zone-public-edge-authorizer` |

Changes under `proto/**` trigger only the consumers of the changed contract;
CI does not fan out to workflows that do not own that contract. Cloud Console
and Cost Console create `runtime-config.js` when the container starts, so
release images do not need environment-specific GitHub Variables. Compose and
Kubernetes must provide public URLs through `cloud-console/.env`
(`NEXT_PUBLIC_ENVOY_URL`, `NEXT_PUBLIC_CENTRIFUGO_WS_URL`,
`NEXT_PUBLIC_COST_CONSOLE_URL`) and `cost-console/.env`
(`VITE_CLOUD_CONSOLE_URL`). The entrypoint fails closed when a value is missing
or invalid; secrets must never be placed in runtime configuration.

### 5. Local endpoints

| Surface | Endpoint |
| --- | --- |
| Cloud Console through Envoy | `https://localhost` or `https://cloud.aurora.local` |
| Central Envoy HTTP / HTTPS | `http://localhost:80` / `https://localhost:443` |
| Envoy admin | `http://localhost:29901` |
| Admin UI direct | `http://localhost:5175` |
| Cost Console direct | `http://localhost:5176` |
| Cost Manager HTTP | `http://localhost:8084` |
| Kafka UI | `http://localhost:18080` |
| Vault | `http://localhost:8200` |
| Grafana | `http://localhost:13001` |
| Zone Public Edge | `http://localhost:29000` |
| MinIO API / Console | `http://localhost:9000` / `http://localhost:9001` |

The `*.aurora.local` domains must resolve to `127.0.0.1` in a local
environment.

### 6. Stop the stack

Stop the Zone before Central:

```bash
make down-zone
make down-central
```

`make clean`, `make clean-central`, and `make clean-zone` remove named volumes.
`clean-zone` also removes the local Dataplane keyring; do not use these targets
when the data must be preserved.
