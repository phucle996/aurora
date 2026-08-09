# Aurora

Aurora là monorepo của một nền tảng cloud đa Zone. Central quản lý identity, desired state, orchestration, notification và billing; mỗi Zone thực thi workload, giữ runtime state và telemetry của chính Zone đó.

## Documentation

| Tài liệu | Nội dung |
| --- | --- |
| [ARCHITECTURE.md](./ARCHITECTURE.md) | Kiến trúc Central và Zone, ownership và các luồng runtime |
| [SECURITY.md](./SECURITY.md) | Security policy, trust boundary, secret handling và báo cáo lỗ hổng |
| [dev/README.md](./dev/README.md) | Chi tiết Docker Compose, network và volume cho local development |
| [contracts/proto/README.md](./contracts/proto/README.md) | Canonical Protobuf contracts và quy tắc compatibility |
| [god_view/](./god_view/) | Source of Truth chi tiết theo từng workflow/domain |

## Components

### Central

| Thành phần | Tech | Vai trò |
| --- | --- | --- |
| [Central Envoy](./dev/central/envoy/) | Envoy | TLS termination, virtual host, API routing và ExtAuthz |
| [ACR](./acr/) | Rust/Tonic | Session, Trinity/Billing/SRE authentication và trusted identity headers |
| [Controlplane](./controlplane/) | Go/Gin/gRPC | Core API và durable desired state |
| [Job Orchestrator](./job-orchestrator/) | Rust/Tokio | PostgreSQL changefeed, Kafka dispatch/result settlement và reconciliation |
| [Notification Service](./notification-service/) | Rust/Axum | Activity/inbox projection, Redis consumers và Centrifugo adapter |
| [Cost Manager API](./cost-manager/api/) | Go/Gin | Billing API, wallet, plan, tier, payment và ownership |
| [Cost Manager Engine](./cost-manager/engine/) | Rust/Tokio | Usage rating, pricing runtime và wallet ledger debit |
| [Cloud Console](./cloud-console/) | Next.js/React | User console |
| [Admin UI](./admin-ui/) | Vite/React | SRE/platform console |
| [Cost Console](./cost-console/) | Vite/React | Billing console |

Central infrastructure gồm Controlplane PostgreSQL, Billing PostgreSQL, Auth-State Redis, Shared L2 Redis, Kafka, NATS Core, Vault, Scylla, ClickHouse, Centrifugo và bộ OpenTelemetry/Victoria/Grafana.

### Zone

| Thành phần | Tech | Vai trò |
| --- | --- | --- |
| [Dataplane](./dataplane/) | Rust/Tokio | Per-Zone job admission, execution, leader election và result/report |
| [Zone Public Edge](./zone-public-edge-gateway/) | Envoy | Public object transfer; runtime route là gate đang staged |
| [Zone Control Edge](./zone-control-edge-gateway/) | Envoy + Rust | Private mTLS control boundary và capability authorization |
| [Zone Runtime Stream](./zone-runtime-stream/) | Rust/Axum | SSE read-plane cho metrics/logs từ Zone Victoria |

Zone infrastructure local gồm NATS JetStream KV, MinIO, Stalwart, OTel Collector và VictoriaMetrics/VictoriaLogs/VictoriaTraces.

### Shared repository assets

| Path | Nội dung |
| --- | --- |
| [contracts/](./contracts/) | Shared Protobuf contracts và cross-language fixtures |
| [dev/](./dev/) | Central/Zone Docker Compose và local infrastructure config |
| [k8s/](./k8s/) | Kubernetes, Argo CD, HPA/PDB, operator và NetworkPolicy manifests |
| [god_view/](./god_view/) | Workflow-level design documents |
| [scripts/](./scripts/) | Local bootstrap/provisioning helpers |

## Run locally

### Requirements

- Docker Engine có Docker Compose V2.
- GNU Make.
- `curl` và `jq` cho Vault bootstrap.
- Python 3 cho Zone keyring generator.
- GHCR `read:packages` credential nếu package Aurora là private.

### 1. Tạo app-owned environment files

```bash
cp controlplane/.env.example controlplane/.env
cp acr/.env.example acr/.env
cp job-orchestrator/.env.example job-orchestrator/.env
cp notification-service/.env.example notification-service/.env
cp cost-manager/api/.env.example cost-manager/api/.env
cp cloud-console/.env.example cloud-console/.env
cp dataplane/.env.example dataplane/.env
cp zone-runtime-stream/.env.example zone-runtime-stream/.env
cp dev/central/.env.example dev/central/.env
cp dev/zone/.env.example dev/zone/.env
```

Điền các biến đang để trống trước khi start. `dev/central/.env` và
`dev/zone/.env` phải pin mỗi Aurora service tới digest GHCR do release workflow
đã scan; Compose không build source ở máy local. Nếu package private, login
trước bằng một PAT chỉ có scope `read:packages`:

```bash
echo "$GHCR_READ_TOKEN" | docker login ghcr.io -u phucle996 --password-stdin
```

Vault bootstrap tạo các development token cố định theo workload trong [`dev/central/vault/vault-bootstrap.sh`](./dev/central/vault/vault-bootstrap.sh):

| File | Local-only value |
| --- | --- |
| `controlplane/.env` | `VAULT_TOKEN=aurora-dev-controlplane-token` |
| `acr/.env` | `VAULT_TOKEN=aurora-dev-acr-token` |
| `job-orchestrator/.env` | `VAULT_TOKEN=aurora-dev-job-orchestrator-token` |
| `notification-service/.env` | `VAULT_TOKEN=aurora-dev-notification-token` |
| `cost-manager/api/.env` | `VAULT_TOKEN=aurora-dev-cost-manager-api-token` |
| `cost-manager/api/.env` | `VAULT_ENGINE_TOKEN=aurora-dev-cost-manager-engine-token` |

Các token trên chỉ dành cho Vault dev local. Đồng bộ `CENTRIFUGO_API_KEY`, MinIO và Stalwart credentials với giá trị development trong Compose. Không commit file `.env` hoặc `dataplane/.secrets/`.

### 2. Khởi động Central

```bash
make init-central
```

Lệnh này start Vault dev, bootstrap Transit/KV/policy/connection records rồi start toàn bộ Central stack. Central phải lên trước vì nó tạo network `aurora-dev-transport`.

### 3. Khởi động Zone

```bash
make init-zone
```

Lệnh này sinh Dataplane HPKE keyring local và start Zone stack. Chỉ các Dataplane replica attach vào Central transport network để truy cập Kafka và NATS Core.

### 4. Kiểm tra trạng thái và log

```bash
docker compose -f dev/central/compose.yml ps
docker compose -f dev/zone/compose.yml ps

docker compose -f dev/central/compose.yml logs -f controlplane1
docker compose -f dev/central/compose.yml logs -f job-orchestrator
docker compose -f dev/zone/compose.yml logs -f dataplane-vn-n1
```

Sau lần bootstrap đầu, có thể start nhanh mà không bootstrap lại:

```bash
make up-central
make up-zone
```

Các target trên luôn `pull` rồi chạy Compose với `--no-build`. Docker vẫn cache
layer của image đã pull, nhưng Rust/Go/Node build cache và source build context
không được tạo trên máy local.

## Component releases

GitHub Actions giữ CI và release tách theo Central/Zone và theo subproject. PR
hoặc push branch chỉ chạy job của path bị thay đổi. Push một component tag chỉ
build, Trivy-scan và publish đúng image GHCR đó; tag version chỉ được gắn sau
khi image staging pass scan.

| Tag | Image |
| --- | --- |
| `cp-v*` | `aurora-controlplane` |
| `acr-v*` | `aurora-acr` |
| `jo-v*` | `aurora-job-orchestrator` |
| `notification-service-v*` | `aurora-notification-service` |
| `cost-manager-v*` | `aurora-cost-manager` |
| `cloud-console-v*`, `admin-ui-v*`, `cost-console-v*` | Console tương ứng |
| `dataplane-v*` | `aurora-dataplane` |
| `zone-runtime-stream-v*` | `aurora-zone-runtime-stream` |
| `zone-authorizer-v*` | `aurora-zone-control-authorizer` |

`contracts/**` là dependency chung của Dataplane, Job Orchestrator và ACR, nên
CI sẽ kiểm tra đúng các consumer này. Release frontend cần GitHub Environment
Variables `NEXT_PUBLIC_ENVOY_URL`, `NEXT_PUBLIC_CENTRIFUGO_WS_URL`,
`NEXT_PUBLIC_COST_CONSOLE_URL` và `VITE_CLOUD_CONSOLE_URL`; workflow fail-closed
nếu URL public bị để trống.

### 5. Local endpoints

| Surface | Endpoint |
| --- | --- |
| Cloud Console qua Envoy | `https://localhost` hoặc `https://cloud.aurora.local` |
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

Các domain `*.aurora.local` cần resolve về `127.0.0.1` trong môi trường local.

### 6. Dừng stack

Zone được dừng trước Central:

```bash
make down-zone
make down-central
```

`make clean`, `make clean-central` và `make clean-zone` xóa named volumes. `clean-zone` còn xóa Dataplane keyring local; không dùng các lệnh này nếu cần giữ dữ liệu.
