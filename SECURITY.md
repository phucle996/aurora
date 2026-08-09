# Security Policy

Aurora xử lý identity, session, billing state, infrastructure command và Zone-local credentials. Mọi thay đổi tại authentication, authorization, cryptography, transport contract, payment, edge gateway hoặc secret boundary phải được xem là security-sensitive.

## Supported versions

| Version | Security support |
| --- | --- |
| `main` / bản release mới nhất | Có |
| Commit hoặc release cũ | Chỉ khi maintainer xác nhận |
| Fork không do Aurora maintain | Không |

Repo hiện phát triển theo mainline. Security fix được áp dụng vào nhánh đang được support; không mặc định backport cho mọi commit cũ.

## Reporting a vulnerability

Không tạo public issue, discussion, pull request hoặc log paste có chứa chi tiết khai thác, token, cookie, private key hay dữ liệu người dùng.

Ưu tiên dùng GitHub Private Vulnerability Reporting:

[Report a vulnerability privately](https://github.com/phucle996/aurora/security/advisories/new)

Nếu private reporting chưa được bật, hãy liên hệ maintainer qua một kênh riêng được công bố trên GitHub profile/repository. Có thể mở public issue chỉ để yêu cầu một kênh liên lạc riêng; không đính kèm chi tiết vulnerability.

Báo cáo nên gồm:

- Component, commit/release và môi trường bị ảnh hưởng.
- Mô tả impact và trust boundary bị phá vỡ.
- Điều kiện tiên quyết và các bước tái hiện tối thiểu.
- Proof of concept đã sanitize, không chứa production secret/data.
- Đánh giá severity nếu có.
- Cách khắc phục hoặc defense-in-depth đề xuất.
- Kênh liên hệ và mong muốn attribution.

Không kiểm thử trên production hoặc dữ liệu của người khác. Không thực hiện destructive action, persistence, lateral movement, social engineering, denial of service hoặc exfiltration vượt quá mức cần thiết để chứng minh impact.

## Response process

Mục tiêu xử lý:

1. Xác nhận đã nhận báo cáo trong 3 ngày làm việc.
2. Triage severity và affected surface trong 7 ngày làm việc.
3. Thống nhất cách phối hợp, embargo và disclosure với reporter.
4. Phát hành fix/mitigation theo mức độ rủi ro.
5. Công bố advisory sau khi người dùng có thời gian cập nhật phù hợp.

Đây là mục tiêu best-effort, không phải SLA hoặc bug-bounty commitment. Reward không được mặc định nếu chưa có chương trình riêng được công bố.

## Security boundaries

### Central edge

- Browser chỉ đi vào Central API qua Envoy.
- Envoy terminate TLS, giới hạn body/timeout và gọi ACR bằng gRPC ExtAuthz.
- ACR phải xóa hoặc overwrite mọi trusted identity/proof header do client gửi.
- Static asset route có thể tắt ExtAuthz; API route không được kế thừa bypass đó.
- Backend vẫn kiểm tra domain authorization; UI hiding hoặc ACR authentication không thay backend permission enforcement.

### Identity and session

- IAM PostgreSQL là durable identity/role/membership Source of Truth.
- Auth-State Redis giữ runtime session, nonce, replay fence và rate limit; dependency failure phải fail-closed.
- Shared L2 Redis chỉ giữ request/reply, cache, Pub/Sub, bounded Stream và workflow lock/checkpoint.
- Auth-State Redis và Shared Redis phải dùng deployment, credential/ACL và namespace tách biệt.
- Session/alias cookie là host-only, `Secure`, `HttpOnly` và có CSRF/session-proof contract phù hợp.
- Critical route phải dùng fresh proof; không chấp nhận stale authorization cache cho privileged mutation.

### Central and Zone isolation

- Kafka là durable Central↔Zone transport; NATS Core chỉ chở soft state.
- NATS JetStream KV là database riêng của từng Zone, không phải Central event bus.
- Dataplane Zone A không được subscribe command/metadata của Zone B.
- JO không có Zone KV credential hoặc Zone HPKE private key.
- Dataplane không có Controlplane/Billing PostgreSQL, Auth Redis, Shared Redis hoặc Vault credential.
- Notification Service và Cost Engine không được cấp NATS/Zone KV credential nếu workflow không cần.

### Protected Zone payload

- Controlplane serialize toàn bộ domain command rồi HPKE-seal bằng active public key của đúng Zone.
- JO chỉ validate public envelope metadata và relay byte-identical ciphertext.
- Dataplane load private key từ read-only Zone-local file mount; private key không được đặt trong env, image, PostgreSQL, Redis, Kafka, Zone KV, log hoặc trace.
- AAD phải bind key, Zone, source domain, job topic, resource, job version và schema version.
- Retry at-least-once reuse đúng ciphertext đã commit; không rebuild plaintext từ projection không authoritative.
- DLQ chỉ chứa sanitized metadata, byte length và fingerprint khi cần; không copy raw protected payload.

### Zone edge separation

- Zone Public Edge không nhận Central cookie, ACR assertion, Zone KV credential hoặc private control identity.
- Public Edge chỉ route allow-listed data/read capability; presigned URL được MinIO/S3 verify.
- Zone Control Edge chỉ nhận Central Envoy workload identity qua mTLS.
- Zone Control Authorizer verify signed assertion, replay/request binding và Zone access; nó không tự suy ownership.
- Runtime Stream chỉ query fixed allow-list từ Zone Victoria; browser không được truyền PromQL/LogsQL tùy ý.

### Billing

- Billing PostgreSQL tách khỏi Controlplane PostgreSQL.
- Cost Manager không query trực tiếp Controlplane database.
- Owner/user/tenant context được derive từ trusted identity; client-provided owner ID không phải billing authority.
- Wallet mutation và immutable ledger insert phải commit cùng transaction.
- Payment webhook phải verify signature trên exact raw body, timestamp window và idempotency key.
- Money dùng fixed integer micro-unit; không dùng floating point cho settlement.

### Telemetry and projection

- Victoria, Scylla timeline và realtime channel không phải business aggregate Source of Truth.
- Log/metric/trace không chứa raw token, cookie, password, private key, customer credential, protected payload, rendered Secret hoặc presigned query.
- User/resource/workspace/Zone UUID, raw path, Redis key, SQL text và error string không được dùng làm unbounded metric label.
- Telemetry failure không được đổi durable business outcome.

## Secret handling

- Không commit `.env`, private key, access token, refresh token, cookie, Vault response, TLS private key hoặc Dataplane keyring.
- Chỉ commit `.env.example` với placeholder không nhạy cảm.
- Production dùng workload identity/AppRole/Kubernetes auth và least-privilege capability record; không dùng local static Vault token.
- Mỗi service chỉ nhận secret cho downstream do nó sở hữu.
- Secret phải được redact khỏi `Debug`, error response, structured log và test fixture.
- Rotation phải giữ overlap/fencing cần thiết; không retire key khi retained outbox/projection còn tham chiếu.

Nếu secret thật bị commit:

1. Revoke/rotate secret ngay; xóa khỏi Git history không làm secret cũ an toàn trở lại.
2. Xác định nơi secret đã được dùng và audit access.
3. Thay credential ở mọi deployment/consumer.
4. Chỉ sau đó sanitize repository/history nếu cần.
5. Ghi incident/advisory qua kênh riêng.

Các giá trị cố định trong Docker Compose/Vault bootstrap là development-only và không được tái sử dụng ở staging/production.

## Secure development requirements

### Input and authority

- Validate size, schema, enum, UUID, timestamp, Zone/tenant/workspace scope tại boundary.
- Không suy authority từ path, body, query hoặc header do client kiểm soát.
- Normalize trước khi sign/hash/compare; signed message phải có canonical format versioned.
- Unsupported route/schema/capability phải fail-closed.

### Durable workflows

- Aggregate mutation và outbox record commit cùng transaction.
- Kafka/Redis Stream consumer chỉ ACK/commit sau durable side effect hoặc durable sanitized quarantine.
- At-least-once handler phải idempotent và dùng stable identity/version/generation fence.
- External mutation không được tự động retry nếu contract chưa chứng minh idempotency.
- Lock giảm concurrency nhưng version/generation/fencing token mới là correctness boundary.

### Cryptography

- Không tự thiết kế primitive hoặc downgrade algorithm/TLS.
- Dùng canonical shared contract và cross-language fixture trong [`proto/`](./proto/).
- Key ID, algorithm suite, AAD và rotation state phải versioned.
- Constant-time/verified library API được ưu tiên cho secret comparison và signature validation.

### Dependencies and infrastructure

- Pin image/dependency version phù hợp với release policy; tránh `latest` trong production.
- Security-sensitive dependency update phải chạy unit/integration/contract tests liên quan.
- NetworkPolicy/ACL phải deny-by-default và chỉ mở exact ingress/egress cần thiết.
- Production transport phải bật TLS/mTLS/SASL theo contract; không silent downgrade về plaintext.
- Dev-only mode, root credential, auto schema hoặc open CORS không được đi vào production manifest.

## Review checklist

Security review là bắt buộc khi thay đổi:

- ACR, IAM, session/cookie, MFA, OAuth, RBAC hoặc trusted headers.
- Vault policy/path, Redis ACL/namespace hoặc workload identity.
- Protobuf field/schema, Kafka topic, NATS subject hoặc DLQ payload.
- HPKE/AAD/key lifecycle hoặc Dataplane key mount.
- Envoy route/filter, Zone Edge, NetworkPolicy hoặc CORS.
- Wallet/ledger/payment/ownership logic.
- Logging, telemetry attribute hoặc data retention.

PR security-sensitive nên chỉ rõ threat model, authority source, failure semantics, replay/race behavior, migration/cutover plan và verification evidence.

## Disclosure

Aurora phối hợp responsible disclosure. Reporter được ghi nhận nếu họ muốn và nếu disclosure không gây rủi ro thêm. Không công bố exploit detail trước khi fix/mitigation sẵn sàng cho người dùng đang được support.

Security design chi tiết theo workflow nằm trong [`god_view/`](./god_view/); kiến trúc hệ thống nằm tại [`ARCHITECTURE.md`](./ARCHITECTURE.md).
