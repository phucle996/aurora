# Personal Mail Consumer Runtime Read — Master God View

Workflow này sở hữu đường đọc health, metrics, logs và events của một Mail
Consumer cá nhân. Nó không tạo watch lease, không ghi snapshot vào Central Redis
và không đưa Controlplane HTTP hoặc Job Orchestrator vào runtime stream path.

## API-scope contract

- Browser gọi cùng origin `POST /api/v1/runtime/assertions` với JSON phẳng
  `resource_type=mail_consumer`, `resource_id`, `panel`, optional `component_id`
  và `from_seconds`. Đây là endpoint local của ACR; Envoy không forward xuống
  Controlplane HTTP.
- ACR xác minh Trinity Personal session, CSRF, workspace và Zone context, sau đó
  yêu cầu IAM quyết định exact permission `email:consumer:read`. Personal ở đây
  là range tác động lên platform; self-user không tự gán permission.
- Chỉ sau allow decision ACR mới ký vé TTL 10 giây bind exact `GET` path/query.
  Response trả `zone_id`, verified `zone_code`, path, UTC expiry và chữ ký.
- UI ghép `https://{zone_code}.{NEXT_PUBLIC_ZONE_PUBLIC_BASE_DOMAIN}` rồi gọi
  trực tiếp Zone Public Edge. ACR không biết DNS hoặc cluster của Zone.
- Assertion chỉ sống trong memory của lần mở stream, không lưu cookie,
  local/session storage hoặc query string. Stream đang mở không mint lại mỗi 10
  giây; chỉ reconnect mới xin vé mới.

## Boundary matrix

| Boundary | Authority | State / output |
| --- | --- | --- |
| Cloud Console | Same-origin Trinity session và configured public base domain | Mint vé rồi `fetch` trực tiếp Zone; vé chỉ ở memory |
| Central Envoy + ACR + IAM decision | Personal session, workspace, Zone và `email:consumer:read` | ACR-local Ed25519 assertion TTL 10 giây |
| Zone Public Edge Authorizer | Chữ ký ACR, registration head và Zone replay KV | Trusted runtime headers sau distributed replay fence |
| Zone runtime stream | Header scope do authorizer overwrite | SSE có giới hạn lifetime, fan-out và byte budget |
| Zone Victoria | OTel metrics/logs của Dataplane | Telemetry theo consumer/slot; không phải business SoT |
| Zone KV `AURORA_ZONE_CONFIG` | Consumer projection từ durable configuration command | `mail.consumer.head.{consumer_id}` |
| Zone KV `AURORA_ZONE_RUNTIME_REPLAY` | CAS theo assertion `jti`, file KV history 1, TTL 30 giây | Một assertion chỉ mở được một stream trên toàn cụm authorizer |

## Durable registration contract

Personal consumer được tạo/cập nhật trong transaction Mail của Controlplane. Outbox
phát `MailConsumerUpsertV1` qua Kafka với các trường phẳng:

- `consumer_id`, `config_version`, `config_sha256`;
- `owner_id = actor_user_id`, `owner_type = PERSONAL`;
- `workspace_id`, `zone_id`;
- stream/template/sender/desire-state/parallelism.

Dataplane kiểm tra protobuf và Zone fence rồi ghi snapshot cùng head vào
`AURORA_ZONE_CONFIG`. Head schema v1 gồm `runtime_read_enabled`, module/resource,
version, owner/workspace/zone và tombstone. Runtime authorizer chỉ cấp read khi
registration tồn tại, bật read, chưa tombstone và khớp toàn bộ scope.

## Runtime read phases

1. Browser gọi cùng origin `POST /api/v1/runtime/assertions`. Central Envoy gọi ACR
   với cookie/session context và raw JSON body; route không có upstream forward.
2. ACR xác minh Personal context, CSRF, UUID, panel allow-list,
   `from_seconds=1..300`, workspace và verified `zone_code`. ACR map public
   `mail_consumer` thành internal `mail/consumer` cùng permission
   `email:consumer:read`; browser không gửi permission hoặc internal path.
3. ACR gửi protobuf decision request phẳng qua Shared Redis tới IAM. IAM đọc
   `user_role` authority projection và so khớp workspace hoặc wildcard. Timeout,
   malformed reply và deny đều không được ký.
4. ACR ký assertion schema v1 bằng Vault Transit Ed25519, bind actor, Personal
   owner, workspace, `zone_id`, consumer, panel, method và SHA-256 full path.
   Local HTTP 200 trả vé, verified `zone_code`, path và UTC expiry.
5. UI ghép Zone origin rồi `fetch GET` trực tiếp
   `/zone-public/v1/runtime/mail/consumer/{consumer_id}/{panel}?from_seconds=60`
   với assertion/signature/key-id trong header và `credentials=omit`.
6. Zone Authorizer xác minh signature, issuer/audience, TTL, exact request, lookup
   `{module}.{resource_type}.head.{resource_id}` rồi CAS `jti` vào
   `AURORA_ZONE_RUNTIME_REPLAY`. CAS `AlreadyExists` là replay; timeout/lỗi KV
   fail closed. Client scope headers bị xóa; authorizer chỉ overwrite trusted
   headers sau khi registration và distributed replay fence cùng thành công.
7. Zone Envoy rewrite route cố định thành `/runtime/stream`; client không cung cấp
   raw Victoria query.
8. Mail adapter dựng fixed query theo module/resource/owner/workspace/zone và
   optional component. Metrics đọc VictoriaMetrics; logs/events đọc VictoriaLogs.
9. SSE trả snapshot rồi bounded live frames. Kết nối đã mở sống tới stream lifetime
   độc lập TTL 10 giây; mỗi reconnect mint assertion mới.

## Failure and isolation rules

| Failure | Result |
| --- | --- |
| Session/workspace/Zone/path không hợp lệ, IAM deny/timeout | ACR deny, không ký |
| Signature sai, hết hạn hoặc replay | Zone Edge deny |
| Head thiếu, tombstone hoặc scope lệch | Zone Edge deny; không query Victoria |
| Registry hoặc replay KV timeout/corrupt | Fail closed `Unavailable` |
| Victoria timeout/quá byte budget | SSE `stream.error`; lifecycle Mail không bị ảnh hưởng |
| Client chậm | Queue bounded; log stream đóng khi gap, metric stream giữ snapshot mới nhất |

Không assertion, cookie, credential broker, nội dung mail hoặc raw query nào được ghi
vào telemetry. Storage metering chỉ chạy khi authorizer gắn marker Storage; runtime
read không tạo sự kiện tính tiền Storage.

## Code map

| Column row | Owner file |
| --- | --- |
| UI | `cloud-console/src/app/(console)/mail/components/ConsumersTab.tsx` |
| Assertion mint | `acr/src/runtime_read.rs`, `acr/src/gateway/ext_authz.rs` |
| IAM permission transport | `controlplane/internal/iam/transport/pubsub/handler/runtime_read_authorization_redis.go` |
| IAM Personal decision | `controlplane/internal/iam/service/personal_runtime_read_authorization_service.go` |
| IAM Personal projection | `controlplane/internal/iam/repository/personal_runtime_read_authorization_repo.go` |
| Zone verify | `zone-public-edge-gateway/authorizer/src/runtime_read.rs` |
| Zone route | `zone-public-edge-gateway/envoy.yaml` |
| Runtime service | `zone-runtime-stream/src/{http,contract,mail,source,stream}.rs` |
| Registration contract | `proto/controlplane/mail/mail_runtime.proto` |
| Zone projection | `dataplane/src/executor/mail/{projection,runtime/configuration}.rs` |
| Telemetry | `dataplane/src/executor/mail/supervisor/consumer_telemetry.rs` |
| Production chassis | `k8s/zone-public-edge-gateway.yaml`, `k8s/zone-runtime-stream.yaml` |
