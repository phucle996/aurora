# ACR — Edge Authz Gateway

ACR là Rust Envoy `ext_authz` service ở edge. ACR xử lý CORS, pre-auth rate
limit, session/proof verification và các local interceptor trước khi request
được forward tới Zone upstream. Zone Catalog được trả trực tiếp từ ACR, không
đi qua HTTP upstream của Controlplane.

## Zone Catalog access matrix

| Login state | JWT role / subject | Canonical route | Zones returned | Virtual `global` |
|---|---|---|---|---|
| Authenticated | `sub == "sre"` | `/admin/hierarchy/zones/catalog` | Tất cả Zone trong catalog (`active`, `planned`, `draining`, `maintenance`, `disabled`) | Có |
| Authenticated | `sub == user UUID` | `/api/v1/zones/catalog` | Chỉ Zone `active` hoặc `draining` | Không |
| Anonymous | Không có session | `/admin/hierarchy/zones/catalog` | Zone `active` hoặc `draining` cho admin sign-in page | Có |
| Anonymous | Không có session | `/api/v1/zones/catalog` | Zone `active` hoặc `draining` cho user sign-in page | Không |

### Virtual admin Zone

| Field | Value |
|---|---|
| Code | `global` |
| Name | `Global Zone` |
| Internal ID | `00000000-0000-0000-0000-000000000000` |
| Scope | Chỉ catalog admin; không được dùng làm runtime Zone của user session |

`/admin/hierarchy/zones/catalog` là route canonical của Hierarchy. Protobuf
descriptor package là `hierarchy.rpc`; Shared Redis transport dùng namespace
`hierarchy`. Không dùng alias `/admin/core/...` hoặc package `core.rpc`.

## Runtime path

1. Envoy gửi `CheckRequest` tới ACR.
2. ACR kiểm tra session/role theo route và đọc Zone snapshot từ L1.
3. L1 hit trả local JSON `200` bằng Envoy denied response; không có upstream
   HTTP call.
4. L1 miss/stale dùng single-flight refresh qua Shared L2 Redis:
   `hierarchy.zone.get_zone_list` và reply prefix
   `hierarchy.zone.get_zone_list.reply.`.
5. Controlplane chọn một replica bằng request-id fence, đọc PostgreSQL
   `hierarchy.zones`, rồi publish catalog protobuf về ACR.

Refresh được giới hạn dưới ngân sách ext_authz: L1 positive TTL `30s`, Shared
L2 positive key TTL `86400s`, negative key TTL `180s`, refresh timeout `1s` và
failure backoff `1s`. Khi refresh lỗi, ACR giữ bounded L1 snapshot và không
biến cache outage thành HTTP `403` giả.

### Catalog cache contract

| Key / state | Store | Contract |
|---|---|---|
| `code_to_id[{normalized_zone_code}]` | ACR process-local L1 | Zone UUID snapshot; positive TTL `30s`, negative TTL `180s` |
| `id_to_status[{zone_id}]` | ACR process-local L1 | Status snapshot, cùng expiry với code entry |
| `id_to_name[{zone_id}]` | ACR process-local L1 | Presentation name snapshot, cùng expiry với code entry |
| `zone:code:{normalized_zone_code}` | Shared L2 Redis | String `zone_id:status`; `NOT_FOUND` là negative marker |
| `hierarchy.zone.get_zone_list` | Shared L2 Redis Pub/Sub | Full-catalog request channel |
| `hierarchy.zone.get_zone_list.reply.{request_id}` | Shared L2 Redis Pub/Sub | Correlated protobuf reply |

`{normalized_zone_code}` là `trim().to_ascii_lowercase()`. L1 là projection có
TTL, không phải durable hierarchy authority; PostgreSQL ở Controlplane là
source of truth của catalog.

## Source map

| Concern | Source |
|---|---|
| Envoy dispatcher và local response | [`src/gateway/ext_authz.rs`](src/gateway/ext_authz.rs) |
| User catalog filtering | [`src/user/zone_catalog.rs`](src/user/zone_catalog.rs) |
| Admin catalog filtering | [`src/sre/zone_catalog.rs`](src/sre/zone_catalog.rs) |
| L1/L2 cache và single-flight | [`src/infra/zone.rs`](src/infra/zone.rs) |
| Zone protobuf contract | [`../proto/zone.proto`](../proto/zone.proto) |

