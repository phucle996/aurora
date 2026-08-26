# Tenant Mail Consumer Runtime Read — Master God View

Workflow này sở hữu đường đọc health, metrics, logs và events của một Mail
Consumer trong Tenant đang được chọn. Personal authority không được dùng làm
fallback; Tenant ID là range tác động của workflow.

## API-scope contract

- Browser gọi cùng origin `POST /api/v1/runtime/assertions` với JSON phẳng
  `resource_type=mail_consumer`, `resource_id`, `panel`, optional `component_id`
  và `from_seconds`. Đây là endpoint local của ACR, không forward xuống
  Controlplane HTTP.
- ACR xác minh Trinity Tenant session, CSRF, membership-bound tenant, workspace
  và Zone context rồi yêu cầu IAM quyết định exact permission
  `email:consumer:read` trong Tenant đó. Không có Personal fallback.
- Chỉ sau allow decision ACR mới ký vé TTL 10 giây bind exact `GET` path/query.
  Response trả `zone_id`, verified `zone_code`, path, UTC expiry và chữ ký.
- UI ghép `https://{zone_code}.{NEXT_PUBLIC_ZONE_PUBLIC_BASE_DOMAIN}` rồi gọi
  trực tiếp Zone Public Edge. ACR không biết DNS hoặc cluster của Zone.
- Vé chỉ ở memory; stream đang mở không mint lại mỗi 10 giây. Reconnect phải xin
  vé mới và chạy lại Tenant permission decision.

## Boundary matrix

| Boundary | Authority | State / output |
| --- | --- | --- |
| Cloud Console | Same-origin Trinity Tenant session và configured public base domain | Mint vé rồi `fetch` trực tiếp Zone; vé chỉ ở memory |
| Central Envoy + ACR + IAM decision | Tenant membership, actor, workspace, Zone và `email:consumer:read` | ACR-local Ed25519 assertion TTL 10 giây |
| Zone Public Edge Authorizer | Chữ ký ACR, registration head và Zone replay KV | Trusted runtime headers sau distributed replay fence |
| Zone runtime stream | Header scope do authorizer overwrite | SSE có giới hạn lifetime, fan-out và byte budget |
| Zone Victoria | OTel metrics/logs của Dataplane | Telemetry theo consumer/slot; không phải business SoT |
| Zone KV `AURORA_ZONE_CONFIG` | Tenant consumer projection từ durable command | `mail.consumer.head.{consumer_id}` |
| Zone KV `AURORA_ZONE_RUNTIME_REPLAY` | CAS theo assertion `jti`, file KV history 1, TTL 30 giây | Một assertion chỉ mở được một stream trên toàn cụm authorizer |

## Durable registration contract

Tenant consumer được tạo/cập nhật trong transaction Mail riêng. Outbox phát
`MailConsumerUpsertV1` qua Kafka với các trường phẳng:

- `consumer_id`, `config_version`, `config_sha256`;
- `owner_id = tenant_id`, `owner_type = TENANT`;
- `workspace_id`, `zone_id`;
- stream/template/sender/desire-state/parallelism.

Dataplane kiểm tra protobuf, Zone fence và canonical SHA-256 rồi ghi snapshot cùng
head vào `AURORA_ZONE_CONFIG`. Head schema v1 gồm `runtime_read_enabled`,
module/resource identity, version, owner/workspace/zone và tombstone. Delete giữ
scope trong tombstone. Runtime read bị deny khi registration tắt, tombstone hoặc
Tenant assertion không khớp tuyệt đối.

## Runtime read phases

1. Browser gọi cùng origin `POST /api/v1/runtime/assertions`. Central Envoy gọi ACR
   với cookie/session context và raw JSON body; route không có upstream forward.
2. ACR xác minh Tenant context, CSRF, actor, membership-bound Tenant, workspace,
   verified `zone_code`, UUID, panel allow-list và `from_seconds=1..300`. ACR map
   public `mail_consumer` thành internal `mail/consumer` cùng exact permission;
   browser không gửi permission hoặc internal path.
3. ACR gửi protobuf decision request phẳng qua Shared Redis tới IAM. IAM đọc
   `membership_role:{actor}:{tenant}` và chỉ allow exact Tenant/workspace
   permission hoặc wildcard. Personal role không được đọc làm fallback.
4. ACR ký assertion schema v1 bind `owner_type=TENANT`, `owner_id=tenant_id`, actor,
   workspace, `zone_id`, consumer, panel, method và SHA-256 full path. Local HTTP
   200 trả vé, verified `zone_code`, path và UTC expiry.
5. UI ghép Zone origin rồi `fetch GET` trực tiếp
   `/zone-public/v1/runtime/mail/consumer/{consumer_id}/{panel}?from_seconds=60`
   với assertion/signature/key-id trong header và `credentials=omit`.
6. Zone Authorizer xác minh signature/TTL/exact request, lookup registration head
   rồi CAS `jti` vào `AURORA_ZONE_RUNTIME_REPLAY`. `AlreadyExists` là replay;
   timeout/lỗi KV fail closed. Head phải đúng Tenant owner, workspace, Zone,
   module/resource, không tombstone và bật runtime read.
7. Authorizer overwrite trusted scope headers; Envoy rewrite sang
   `/runtime/stream`. Client không cung cấp raw Victoria query.
8. Runtime adapter query Victoria bằng fixed template theo Tenant scope.
9. SSE phát bounded snapshot/live frames. Kết nối đã mở sống tới stream lifetime
   độc lập TTL 10 giây; reconnect đi lại ACR/IAM và Zone admission.

## Failure and isolation rules

| Failure | Result |
| --- | --- |
| Tenant membership/context mất, lệch hoặc IAM deny/timeout | ACR deny, không ký và không downgrade Personal |
| Signature/path/query sai, hết hạn hoặc replay | Zone Edge deny |
| Consumer thuộc Tenant/workspace/Zone khác | Zone Edge deny trước Victoria |
| Registry hoặc replay KV unavailable/corrupt | Fail closed `Unavailable` |
| Victoria unavailable/quá response budget | SSE `stream.error`; workflow cấu hình vẫn chạy |
| Client chậm | Bounded queue/backpressure; không tăng áp lực Controlplane/JO |

Runtime telemetry không thay PostgreSQL/Kafka/Zone KV làm authority. Nó không chứa
credential broker, raw message, recipient, nội dung mail, cookie hoặc assertion.
Runtime read cũng không được ghi nhầm vào Storage metering schema.

## Code map

| Column row | Owner file |
| --- | --- |
| UI | `cloud-console/src/app/(console)/mail/components/ConsumersTab.tsx` |
| Assertion mint | `acr/src/runtime_read.rs`, `acr/src/gateway/ext_authz.rs` |
| IAM permission transport | `controlplane/internal/iam/transport/pubsub/handler/runtime_read_authorization_redis.go` |
| IAM Tenant decision | `controlplane/internal/iam/service/tenant_runtime_read_authorization_service.go` |
| IAM Tenant projection | `controlplane/internal/iam/repository/tenant_runtime_read_authorization_repo.go` |
| Zone verify | `zone-public-edge-gateway/authorizer/src/runtime_read.rs` |
| Zone route | `zone-public-edge-gateway/envoy.yaml` |
| Runtime service | `zone-runtime-stream/src/{http,contract,mail,source,stream}.rs` |
| Registration contract | `proto/controlplane/mail/mail_runtime.proto` |
| Zone projection | `dataplane/src/executor/mail/{projection,runtime/configuration}.rs` |
| Telemetry | `dataplane/src/executor/mail/supervisor/consumer_telemetry.rs` |
| Production chassis | `k8s/zone-public-edge-gateway.yaml`, `k8s/zone-runtime-stream.yaml` |
