# Central and Zone Docker Compose

Môi trường development được tách thành hai Compose project để không còn
cross-wire hạ tầng Central và Zone:

- `dev/central/compose.yml`: Controlplane, ACR, JO, Notification, Cost,
  PostgreSQL, Redis, Kafka, NATS Core và bộ observability Central.
- `dev/zone/compose.yml`: Dataplane Zone Z1, Zone NATS JetStream KV, MinIO,
  Stalwart, Zone Public Edge, `zone-runtime-stream` và bộ observability riêng
  của Zone.
- `dev/shared/tls`: material local dùng cho mTLS/TLS fixture; không thuộc module
  Controlplane và không được đưa vào image hay env.

Mọi `container_name` trong Central bắt đầu bằng `central-`; mọi container của
Zone bắt đầu bằng `zone-`. Service DNS nội bộ vẫn dùng Compose service name để
configuration không phụ thuộc tên container vật lý.

## Start order

Từ repository root:

```bash
docker compose -f dev/central/compose.yml config -q
docker compose -f dev/central/compose.yml up -d

docker compose -f dev/zone/compose.yml config -q
docker compose -f dev/zone/compose.yml up -d
```

Central phải start trước vì nó tạo external network `aurora-dev-transport`.
Zone chỉ attach Dataplane vào network này để dùng Kafka và NATS Core. Bên trong
Zone còn tách `zone-infra`, `zone-telemetry-ingest`, `zone-runtime-read`,
`zone-edge-storage` và `zone-edge-runtime`; không dùng một default network rộng.
Vì vậy runtime stream không có DNS/network path tới MinIO, Zone KV, Dataplane
hay Central transport dù các service cùng nằm trong một Compose project.

Central `nats` chạy Core mode, không bật JetStream và không có data volume.
JetStream chỉ tồn tại ở `nats-zone-z1` để làm Zone-local KV database; hai role
không fallback hoặc dùng chung storage.

Dataplane vẫn fail-fast khi Kafka/NATS Core chưa sẵn sàng; `restart:
unless-stopped` là bounded process recovery của môi trường dev. Compose không
tạo `depends_on` giả xuyên hai project.

## Stop order

```bash
docker compose -f dev/zone/compose.yml down
docker compose -f dev/central/compose.yml down
```

Zone được dừng trước để Central có thể thu hồi transport network sạch. Named
volumes không bị xóa bởi `down`; không dùng `down -v` nếu cần giữ PostgreSQL,
Redis, Zone KV, MinIO hoặc Victoria data.

## Local configuration

Mỗi app nhận runtime configuration từ `.env` của chính app. Các file tracked
`.env.example` là contract mẫu; không đặt private key vào env. Dataplane HPKE
keyring vẫn được mount read-only từ `dataplane/.secrets/`.

Dataplane export OTLP tới `zone-otel-collector:4317`. Central workloads tiếp
tục export tới Central Collector. Không có fallback từ Zone sang Central
Victoria: collector/backend lỗi chỉ làm telemetry unavailable hoặc stale, không
được ảnh hưởng Kafka settlement, lifecycle result hoặc business state.

`zone-runtime-stream` hiện được dựng trên network nội bộ nhưng Public Edge
runtime route/ticket vẫn là staged gate; Compose không publish trực tiếp port
của service hay các Zone Victoria backend ra host.
