# God Plan — PAYG Pricing Schedule và Storage Billing Admission

> **Status:** OPEN — kế hoạch review, chưa bắt đầu implementation.
>
> Đây là execution plan có checklist, không thay thế God View. File này phải
> được giữ trong branch `refactor/payg-pricing-schedule` cho tới khi mọi gate có
> evidence và người dùng xác nhận hoàn tất. Sau khi hoàn tất và được xác nhận,
> xóa file này trong một change set riêng.
>
> **Working rule bắt buộc:** workflow isolation đứng trước việc giảm số dòng
> code. Không tạo helper function, shared utility, `AppContext`, dependency
> bag, `GenericUsageReport`, `GenericUsageSettlementService` hoặc global wallet
> middleware chỉ vì có cú pháp trùng nhau. Logic phải ở workflow owner; helper
> chỉ được phép khi thật sự giữ invariant correctness/security mà inline không
> làm được, và phải private trong module nhỏ nhất có thể.

## 1. Mục tiêu và nguyên tắc đã chốt

Aurora dùng pay-as-you-go cho mọi module. Không có Free/Pro tier, subscription,
monthly quota, monthly price hoặc committed plan. Storage là module đầu tiên cần
hoàn thiện; Hypervisor, Managed Service và Mail chỉ được onboard sau khi có
contract riêng.

Pricing có một catalog identity chung nhưng workflow vẫn tách theo module:

```text
Zone Public Edge
  -> Zone OTel/Victoria diagnostics + Zone ClickHouse metering journal
  -> Zone Control closes an hourly StorageUsageReportV1
  -> Kafka storage.usage.reports.v1
  -> Job Orchestrator validates and relays to Shared Redis
  -> Cost Engine Storage consumer
  -> Billing PostgreSQL owner/pricing/wallet/ledger transaction
```

Central ClickHouse không còn nằm trong billing path. Zone ClickHouse chỉ là
journal/aggregation local; nó không biết payer, wallet hay giá.

### 1.1 Pricing model

| Model | Dùng cho | Contract |
| --- | --- | --- |
| `PROGRESSIVE_UNIT` | Storage capacity, network in, network out và metric scalar tương lai | Bracket quan hệ typed; raw quantity + rational numerator/denominator; checked integer arithmetic và làm tròn một lần ở cuối ledger line |
| `FIXED_BUNDLE` | VM/service shape trong tương lai | Canonical, versioned schema; exact selection theo normalized shape/config hash; không best-fit hoặc wildcard |

`definition_json` chỉ chứa canonical definition của model đã được code review.
Không nhận expression, code, JSONPath, template, dynamic formula hoặc tax logic.

Storage register đúng ba charge kind:

```text
storage.network_in.byte       BYTE,          CLOSED_DELTA
storage.network_out.byte      BYTE,          CLOSED_DELTA
storage.capacity.gb_hour      GB_HOUR_MICRO, CLOSED_INTEGRAL
```

`GB_HOUR_MICRO` dùng decimal GB: `1 GB = 1_000_000_000 bytes` và
`1_000_000 GB_HOUR_MICRO = 1 GB-hour`. UI, API, protobuf và schedule phải dùng
cùng nghĩa; không trộn với GiB.

### 1.2 Currency và commercial boundary

- Cutover chỉ dùng USD và integer micro-units; không FX và không implicit
  conversion.
- Overdraft mặc định bằng zero. Credit line khác zero là financial workflow
  được audit, không phải tier/plan/permission.
- Referral/promotion chỉ tạo ledger credit độc lập nếu workflow đó còn tồn tại;
  không có free allowance hoặc discount ẩn trong schedule.
- Tax, invoice, payment refund và chargeback nằm ngoài rate catalog. Chúng chỉ
  reference immutable ledger evidence và không mutate settled usage/price.

## 2. Source of Truth và discrepancy phải đóng

Trước mỗi phase phải đọc đầy đủ God View liên quan, trace code/config/contract,
ghi discrepancy nếu có, rồi cập nhật God View trong cùng change set khi contract
thay đổi.

### God View phải reconcile trước implementation

- `god_view/billing/billing_storage_usage_settlement_god_view_workflow.md`
  - đổi Tier/service-type run thành charge-kind + scoped schedule;
  - giữ JO → Redis → Cost Engine và wallet transaction;
  - mô tả pin schedule, `UNRATED`, pending activation reconciliation và retry.
- Tạo
  `god_view/billing/billing_pricing_schedule_version_publish_god_view.md`
  thay cho `billing_tier_version_publish_god_view.md`.
- Cập nhật `god_view/billing/billing_personal_storage_estimate_god_view.md`
  thành hourly-only, trusted Zone từ ACR, không monthly output.
- `god_view/storage/storage_usage_report_publish_god_view_workflow.md`
  - giữ hourly UTC, five-minute late grace, shard barrier, Zone outbox và
    three-kind report.
- Các Storage God View có admission behavior phải cập nhật riêng:
  bucket create, quota update, credential create/delete, access-session,
  transfer-ticket issue/revoke, browser upload/download và SDK GET/PUT.
  Không tạo một God View chung cho wallet authorization.
- `cost-manager/ARCHITECTURE.md` phải mô tả schedule/rating/projection topology;
  không biến architecture thành generic workflow box.

### Discrepancy hiện tại cần giải quyết

| ID | Hiện trạng | Hành vi đích |
| --- | --- | --- |
| P0 | Pricing lookup/cache chỉ theo `service_type` | Lookup `(charge_kind_code, zone_id, effective_at)`; Zone exact rồi Global fallback; zero/multiple winner fail-closed |
| P1 | Tier trộn module và metric | Controlled Charge Kind Registry; schedule chỉ reference kind đã enable |
| P2 | Billing run active unique theo service | Run identity `(source_module, source_report_id, charge_kind_code)`; pin immutable schedule version |
| P3 | Storage mở cả ba run dù quantity zero | Chỉ tạo run cho nonzero charge kind |
| P4 | Estimate dùng 730 giờ và `/month` | Chỉ trả hourly capacity estimate và schedule lineage |
| P5 | UI/backend không thống nhất GB/GiB | Decimal GB wire/display contract |
| P6 | Unit conversion/rounding implicit | Raw unit, rational price, checked integer math, final ceil duy nhất |
| P7 | Wallet status chưa có reason/close barrier/outbox | Reasoned status, durable admission outbox, versioned module projections |
| P8 | `BillingService.CheckWalletStatus` chỉ có generated binding, money là `double` | Không activate RPC; không synchronous Billing dependency ở Zone hot path |
| P9 | SDK data path chưa có admission mapping | Zone Public Authorizer phải enforce cả browser ticket và credential GET/PUT |
| P10 | Storage correction wire chưa có signed policy | Giữ `DEAD` quarantine; chỉ adjustment append-only theo module God View |

## 3. Wallet settlement và enforcement contract

Settlement và enforcement là hai workflow độc lập:

```text
closed trusted usage -> Cost settlement -> immutable ledger -> wallet state
                                                         |
                                                         +-> module admission projection
                                                               -> local enforcement
```

Settlement không được bỏ usage vì wallet restricted. Thiếu owner, schedule,
wallet hoặc currency là durable `UNRATED`, không phải free và không được đoán
payer.

### 3.1 Wallet state

Persisted `billing.wallets` cần `restriction_reason`, `status_changed_at` và
immutable `closed_at` trong clean migration.

| Status | Settlement | Admission | Recovery |
| --- | --- | --- | --- |
| `PENDING_ACTIVATION` + `NOT_ACTIVATED` | `UNRATED_WALLET_PENDING_ACTIVATION`, giữ evidence để reconcile | `SUSPEND_BILLABLE` | Top-up tạo Storage historical reconciliation; chỉ emit `ALLOW` sau khi mọi pending row terminal và credit hợp lệ |
| `ACTIVE` | Rate, ledger, debit theo pinned schedule | `ALLOW` | Debit đưa `cash + overdraft <= 0` thì transaction chuyển `SUSPENDED(CREDIT_EXHAUSTED)` |
| `SUSPENDED` + `CREDIT_EXHAUSTED` | Vẫn settle usage đã xảy ra; không bỏ report | `SUSPEND_BILLABLE` | Eligible credit mới được chuyển `ACTIVE` |
| `SUSPENDED` + `ADMINISTRATIVE`/`COMPLIANCE` | Vẫn giữ evidence/ledger hoặc `UNRATED` | `SUSPEND_BILLABLE` | Chỉ admin workflow; top-up không tự mở resource |
| `CLOSED` + `CLOSED` | Chỉ settle window kết thúc trước `closed_at` sau final-report barrier; phần sau là bounded `UNRATED` + alert | `SUSPEND_BILLABLE` | Không re-open |

Không dùng `RESTRICT_NEW` trong Storage cutover. Grace period là một financial
state/reason và God View riêng nếu sau này thật sự cần; không diễn giải
`PENDING_ACTIVATION` thành grace.

### 3.2 Admission outbox và projection

Wallet row và `billing.wallet_admission_outbox` phải commit atomically. Event
`WalletAdmissionChangedV1` tối thiểu có:

```text
event_id, wallet_id, owner_id, owner_type,
wallet_version, admission_mode, restriction_reason,
effective_at, valid_until
```

Không đưa balance, overdraft, currency amount, ledger, access key, secret,
email hoặc credential vào event. Consumer chỉ apply version lớn hơn; stale
`ALLOW` hết hạn phải fail-closed thành `SUSPEND_BILLABLE`. Relay at-least-once
và periodic full reconciliation đều bắt buộc.

Mỗi module có consumer/store/projection riêng. Storage fan-out đọc durable
`STORAGE_BUCKET` ownership từ Billing projection và phát tới đúng Central/Zone.
Không để Zone query Billing PostgreSQL hoặc gọi wallet RPC trong request path.

### 3.3 Storage admission mapping

| Boundary | `ALLOW` | `SUSPEND_BILLABLE` |
| --- | --- | --- |
| Bucket create | Cho phép | Chặn trước business/outbox transaction |
| Quota update | Tăng/giảm | Chỉ giảm; tăng bị chặn sau khi đọc quota durable |
| Credential create | Cho phép | Chặn trước secret generation; revoke/delete vẫn cho phép |
| Access-session prepare | Cho phép | Chặn ở Central projection |
| Browser ticket | Issue và consume đúng ticket | Ticket issue chặn; revoke luôn cho phép; Public Authorizer chặn upload/download trước MinIO |
| SDK Public Edge | Normal policy | Chặn billable GET/PUT trước MinIO; HEAD/LIST/DELETE theo Storage authorization |
| Cleanup/top-up/read wallet | Cho phép nếu authorization hợp lệ | Vẫn cho phép |

Console API đã xác thực có thể trả `402` với bounded
`BILLING_ACTIVATION_REQUIRED`/`BILLING_CREDIT_EXHAUSTED`, hoặc `403
BILLING_ACCESS_RESTRICTED`. Public Edge/SDK luôn trả protocol-safe `403
AccessDenied`, không lộ balance/reason.

## 4. Phases và checklist

Mỗi phase là một change set reviewable. Không mở phase kế tiếp chỉ vì compile;
phải đạt gate, test boundary và God View evidence của phase hiện tại.

### Phase 0 — Freeze contract và God View

**Owner:** Billing architecture + module owners.
**Durable boundary:** Chưa mutation.

- [ ] Review/accept charge kinds, model, units, currency, scope resolver,
  run identity, correction, retention và wallet admission sections.
- [ ] Đọc đầy đủ các God View trong §2; sửa God View trước code nếu contract
  hoặc failure semantics khác AS-IS.
- [ ] Tạo Pricing Schedule publish God View chi tiết theo chuẩn ACR/HTTP nếu
  endpoint đi qua ACR; không có participant hộp đen.
- [ ] Cập nhật Storage settlement/report/estimate God View và admission phase.
- [ ] Chốt retention baseline: Zone raw Storage journal 30 ngày; report/line
  inbox, run, ledger, correction lineage và statement 7 năm từ cuối năm tài
  chính, trừ khi legal policy yêu cầu lâu hơn.
- [ ] Ghi rõ các discrepancy không được âm thầm giải quyết bằng code.

**Gate 0:** God View và plan thống nhất owner, authority, durable boundary,
retry, replay, security và response semantics.

### Phase 1 — Clean migration và Charge Kind Registry

**Owner:** Billing PostgreSQL.
**Durable boundary:** Forward-only migration trên database greenfield.

- [ ] Preflight fail nếu database có settled monetary data cần mapping mà chưa
  được approve; không sửa checksum migration lịch sử.
- [ ] Drop legacy Pack/Plan/Tier/Subscription/assignment authority sau khi
  xác nhận không còn business data cần preserve.
- [ ] Tạo `charge_kind_catalog` controlled bằng migration/code review; không có
  operator endpoint tạo charge kind.
- [ ] Tạo `pricing_schedules`, immutable `pricing_schedule_versions` và typed
  scalar brackets; scope check, effective-window exclusion và canonical checksum.
- [ ] Tạo `usage_settlement_runs` với unique report/kind, Zone/window,
  schedule/version/checksum và fencing metadata; bỏ global active-service unique.
- [ ] Add `restriction_reason`, `status_changed_at`, `closed_at` và
  `wallet_admission_outbox`; enforce status/reason/close-time consistency.
- [ ] Add schedule/run/charge-kind/raw-unit lineage vào line/ledger/unrated và
  immutable correction lineage; correction producers vẫn disabled.
- [ ] Seed đúng ba Storage Global schedules; không seed Free/Pro/monthly plan.
- [ ] Viết migration tests cho overlap, invalid bracket, duplicate scope,
  invalid model/unit, missing reason và illegal closed transition.

**Gate 1:** Empty DB migrate sạch; schema inspection không còn legacy authority;
đúng ba Storage schedule identities và toàn bộ invariant không thể commit sai.

### Phase 2 — Pricing Schedule API và activation

**Owner:** Cost API schedule workflow.
**Durable boundary:** Immutable version + brackets/definition + outbox trong một
transaction.

- [ ] Rename Tier entity/repository/service/DTO/handler/proto/UI thành Schedule;
  remove Plan endpoint và client consumer, không alias để giữ contract cũ.
- [ ] Publish yêu cầu expected latest version, effective time, change reason và
  immutable full definition; server derives model/unit/scope từ locked registry.
- [ ] Validate progressive brackets `[0, infinity)`, contiguous range,
  denominator dương, numerator không âm, overflow và canonical checksum.
- [ ] Reject bundle publish ở Storage; chỉ enable bundle khi module validator
  cùng change set đã ship.
- [ ] Emit publish event sau commit; event chỉ là cache hint, PostgreSQL là SoT.
- [ ] Refactor cache key thành immutable version identity và lookup
  `(charge_kind_code, zone_or_global, at)`; cache stale/empty fail-closed.
- [ ] Không đưa helper dùng chung vào API; validation/model code nằm trong
  Schedule workflow owner hoặc private module-local function có lý do ghi rõ.

**Gate 2:** OCC, overlap, checksum, canonical JSON, cache loss và outbox atomicity
có focused tests; publish không thể tạo rate không thuộc charge registry.

### Phase 3 — Pricing runtime và run pinning

**Owner:** Cost Engine pricing runtime.
**Durable boundary:** Schedule snapshot/run pin phải hoàn tất trước wallet debit.

- [ ] Remove `ServiceType`, `TierPricingSnapshot`, `CatalogSnapshot` và
  `begin_billing_run(service_type, ...)` khỏi runtime authority.
- [ ] Implement typed pricing kernel chỉ cho snapshot selection + exact model
  calculation; kernel không biết Redis, wallet, HTTP, ownership hay module report.
- [ ] Zone exact → Global fallback tại `window_end`; missing/ambiguous catalog
  không được debit.
- [ ] Storage chỉ tạo run khi charge kind có nonzero quantity; run retry luôn
  đọc pinned version/checksum cũ dù schedule mới đã publish.
- [ ] Scalar math dùng raw integer/rational và một final ceil; overflow là error.
- [ ] Không tạo `GenericUsageSettlementService`; Storage consumer giữ transport,
  owner lookup, retry và wallet transaction của chính nó.

**Gate 3:** Concurrent report/window/Zone, rate publish giữa retry, duplicate
report và arithmetic vector đều chứng minh run không bị dùng nhầm.

### Phase 4 — Storage PAYG settlement

**Owner:** Storage Cost Engine consumer.
**Durable boundary:** Storage report/line inbox + ownership + wallet/ledger
transaction.

- [ ] Giữ `StorageUsageReportV1`; không thay bằng global usage envelope.
- [ ] Map rõ upload → `storage.network_in.byte`, download →
  `storage.network_out.byte`, capacity → `storage.capacity.gb_hour`.
- [ ] Resolve owner từ Billing ownership projection tại closed-window boundary;
  report không mang payer/wallet/price.
- [ ] Persist charge kind, source report, schedule/version/checksum, raw unit và
  run lineage trong line/ledger.
- [ ] Zero quantity không tạo run; valid zero-price bracket vẫn giữ audit
  lineage nếu policy yêu cầu.
- [ ] Missing owner/wallet, currency, schedule hoặc invalid snapshot thành
  durable `UNRATED` reason bounded; không fallback đoán.
- [ ] Chỉ ACK/XDEL Redis sau PostgreSQL commit và fencing lease còn hợp lệ.
- [ ] Storage correction wire không được debit/credit; giữ `DEAD` cho tới khi
  signed append-only adjustment God View được duyệt.

**Gate 4:** Một report đủ ba quantity tạo đúng ba ledger lines pinned; replay,
owner missing, Zone override, Global fallback, zero metric và fence loss không
tạo double debit.

### Phase 5 — PAYG quote và console cleanup

**Owner:** Storage quote workflow + Cloud Console.
**Durable boundary:** Read-only response; không reserve price hay mutate wallet.

- [ ] Quote chỉ trả `hourly_estimate_micro_units` và schedule lineage; xóa
  `monthly_estimate_micro_units`/`billing_hours_per_month` khỏi DTO/API/UI.
- [ ] Zone chỉ lấy từ ACR trusted context; không nhận Zone query/body.
- [ ] UI hiển thị `/hour`, decimal GB và giải thích final charge theo observed
  GB-hour; không render monthly projection.
- [ ] Remove active Plan/Free/Pro/subscription API/UI references.
- [ ] Browser/API tests chứng minh quote không có durable side effect.

**Gate 5:** Repository search không còn monthly runtime field, Plan endpoint,
Free tier hoặc subscription entitlement trong active code.

### Phase 6 — Wallet admission và Storage enforcement

**Owner:** Cost wallet writers, Storage Controlplane, Zone Control và Public Edge.
**Durable boundary:** Wallet transition + outbox; Central/Zone projections có
version fence/checkpoint riêng.

- [ ] Mọi wallet writer (provision, payment settlement, Storage debit, admin
  suspend/close) ghi wallet status/reason/version và admission outbox atomically.
- [ ] Implement Storage pending-activation reconciliation: historical
  `window_end` price, deterministic line identity, crash checkpoint; chỉ emit
  `ALLOW` khi pending rows terminal, không waive unresolved debt.
- [ ] Central projection chặn bucket create, quota increase, credential create
  và access-session prepare khi thiếu/expired/suspended; cho phép cleanup,
  revoke và quota decrease.
- [ ] Zone projection keyed by durable resource/bucket association với monotonic
  `(wallet_id, wallet_version)`; Ticket Issuer chặn issue, revoke vẫn allow.
- [ ] Public Authorizer chặn browser upload/download và SDK GET/PUT trước MinIO;
  không chỉ chặn Controlplane API.
- [ ] Full reconciliation sửa lost/reordered event; stale ALLOW hết hạn thành
  deny; không query Billing đồng bộ từ Zone.
- [ ] Viết/cập nhật God View từng workflow affected; SDK GET/PUT phải có God
  View riêng trước khi sửa code.
- [ ] Close wallet chỉ sau module stop/final-report barrier; không flip enum giữa
  lúc Storage còn billable.
- [ ] Không thêm global billing middleware, wallet helper hoặc status RPC.

**Gate 6:** Tests chứng minh atomic wallet/outbox, pending historical replay,
version reorder, stale projection, browser/SDK denial trước MinIO, cleanup vẫn
hoạt động và close barrier không thể bypass.

### Phase 7 — Module onboarding gates

Không triển khai billing cho module mới trong Storage change set.

#### Mail

- [ ] Durable submission journal `PREPARED → ACCEPTED | REJECTED | AMBIGUOUS →
  SOURCE_SETTLED`.
- [ ] Reconcile ambiguous provider action bằng idempotency/query hoặc giữ
  `UNRATED`; không charge `accepted_total`, OTel metric hoặc transient JMAP ack.
- [ ] Mail God View/report/inbox/consumer riêng trước khi enable charge kind.

#### Managed Service

- [ ] Bill allocated bundle duration only after CREATE/RESIZE ready and close
  after DELETE confirmed; JO terminal fence tạo binding/outbox.
- [ ] Admission workflow riêng cho create/resize/drain/stop; no global wallet
  middleware.
- [ ] Bundle definition/config hash exact; scalar CPU/RAM/network là contract
  mới nếu sau này cần.

#### Hypervisor

- [ ] VM metering/settlement God View riêng; trusted lifecycle source, config
  revision/hash, VRAM included và no browser price input.
- [ ] Exact `FIXED_BUNDLE` shape hoặc explicit scalar interval; không best-fit,
  không double-charge.
- [ ] Runtime stop/final watermark và admission projection riêng trước billing.

**Gate 7:** Module owner chứng minh billable fact, source identity, closed
quantity, payer projection, price binding, lifecycle, replay, evidence,
enforcement và God View trước khi registry row được enable.

### Phase 8 — Verification, CI và close plan

**Owner:** Release/operations.
**Durable boundary:** Production-like test environment; không sửa monetary data
thật.

- [ ] Chạy test/build qua GitHub workflow; không tạo local Docker image/cache.
- [ ] Boot Central/Zone theo runbook: Vault/DB/infra trước app.
- [ ] Publish controlled hourly Storage report và inspect JO → Redis → Cost
  Engine → PostgreSQL lineage.
- [ ] Inject crash/restart trước relay, sau XADD, trong SQL transaction, trước
  ACK và sau projection event; chứng minh no duplicate debit.
- [ ] Publish future schedule, replay pinned report; chứng minh old version.
- [ ] Zone override thắng Global fallback đúng window.
- [ ] Exercise wallet suspend, pending activation reconcile, stale ALLOW, top-up
  reactivation, browser ticket denial, SDK GET/PUT denial và cleanup.
- [ ] Confirm Central không có ClickHouse billing dependency; Zone ClickHouse
  chỉ local journal.
- [ ] Trivy/CI/release evidence xanh; ghi commit, migration, test/run IDs.
- [ ] Review toàn bộ checklist với người dùng. Chỉ sau xác nhận mới xóa file
  plan trong change set riêng.

**Gate 8:** God View, code, migration, CI và runtime evidence cùng mô tả một
topology; Storage settlement và enforcement không còn unresolved blocker.

## 5. Test matrix bắt buộc

| Boundary | Cases |
| --- | --- |
| Migration | Empty DB; legacy authority absent; three seeds; scope/effective overlap; bracket; status/reason/close constraints |
| Schedule API | OCC; canonical checksum; scalar/bundle model; outbox atomicity; cache loss |
| Runtime | Zone exact/Global fallback; historical pin; concurrent report; duplicate/reclaim; overflow; one final rounding |
| Storage settlement | Three kinds; zero metric; missing owner/wallet/price; currency mismatch; report replay; fence loss; correction DEAD |
| Wallet | PENDING historical reconcile; credit-exhausted suspend; admin/compliance suspend; close barrier; no implicit re-enable |
| Admission | Missing/expired/reordered projection; browser ticket; SDK GET/PUT; create/quota/credential; DELETE/revoke cleanup |
| Security | No caller owner/Zone/balance/credential authority; no Billing synchronous query; no secret/PII in event/log/metric label |
| UI | Hourly-only quote; decimal GB; no monthly/Plan/Free language; bounded billing error codes |
| Future onboarding | Mail ambiguous acceptance; Managed Service lifecycle fence; Hypervisor exact shape and stop watermark |

## 6. Evidence register

| Gate | Evidence | Status |
| --- | --- | --- |
| 0 | God View review links/commit | `[ ]` |
| 1 | Migration output/schema tests | `[ ]` |
| 2 | Schedule API tests and outbox evidence | `[ ]` |
| 3 | Pricing vector/replay tests | `[ ]` |
| 4 | Storage report/ledger integration evidence | `[ ]` |
| 5 | Quote/UI CI evidence | `[ ]` |
| 6 | Admission/reconciliation/fault tests | `[ ]` |
| 7 | Module owner approval | `[ ]` |
| 8 | GitHub CI, runtime run IDs and user confirmation | `[ ]` |

## 7. Definition of done

Plan chỉ complete khi:

- Storage dùng ba immutable scoped Pricing Schedule PAYG charge kinds;
- mọi ledger line pin schedule version/checksum và run identity đúng report/kind;
- không còn monthly/Plan/Free/Pro runtime authority;
- settlement luôn giữ usage evidence dù wallet restricted;
- Storage admission projection enforce cả browser ticket và SDK GET/PUT mà
  không gọi Billing đồng bộ;
- God View, architecture, code, migration, CI và runtime evidence khớp nhau;
- Mail, Managed Service và Hypervisor không bị enable ngầm;
- người dùng xác nhận đóng plan.

Sau confirmation cuối cùng, xóa file này; không để execution plan tạm trở thành
workflow SoT lâu dài.
