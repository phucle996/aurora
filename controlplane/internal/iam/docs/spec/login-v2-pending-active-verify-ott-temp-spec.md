# Login V2 - Pending Active Account Verification via One-Time Token (Temp Spec)

## 1) Mục tiêu

Đặc tả luồng login v2 cho trường hợp account đang `pending_active`:
- Sau khi validate credential thành công, hệ thống **không phát access/refresh token**.
- Hệ thống gọi service one-time token để phát hành token xác minh account.
- Trả response yêu cầu verify account cho caller.

Spec này chỉ là contract nghiệp vụ/flow. Chi tiết thêm/sửa/xóa file/func nằm trong tài liệu plan.

---

## 2) Scope

Trong scope:
- Login flow ở IAM service + handler mapping.
- Tích hợp one-time token service hiện có.
- Publish mail verify job vào Redis stream sau khi issue OTT.
- Trạng thái account `pending_active`.

Ngoài scope:
- Không định nghĩa verify endpoint chi tiết trong tài liệu này.
- Không thay đổi register flow.
- Không thay đổi refresh flow.

---


## 2.1 Boundary bắt buộc: OTT cache vs Mail stream job

Hai thành phần này là **2 concern khác nhau**, không được trộn:

1) One-time token Redis cache (`internal/iam/cache/one_time_token_cache.go`)
- Mục đích: lưu state xác minh one-time token để consume 1 lần.
- Key/value: `iam:ott:{purpose}:{user_id}` -> `token_hash`.
- TTL: `config.IAM.OneTimeTokenTTL`.
- Không dùng cache này để làm queue job gửi mail.

2) Redis stream publish mail job (`infra/redis/stream.go`)
- Mục đích: enqueue job gửi mail verify.
- Payload: dữ liệu mail job (event_type, email, verify_token, ...).
- Idempotency: qua `idempotency_key` + `Publish(..., idempotencyTTL)`.
- Không dùng stream để thay thế state consume của one-time token.

## 3) Rule nghiệp vụ

### 3.1 Login với account active

- Nếu credential hợp lệ và account `active`:
  - Hành vi giữ nguyên như hiện tại.
  - Phát access/refresh token bình thường.

### 3.2 Login với account pending_active

- Nếu credential hợp lệ và account `pending_active`:
  - Gọi OTT service: `Issue(ctx, purpose, userID)`.
  - `purpose` cố định: `account_verify`.
  - `userID` là `users.id`.
  - Không set cookie access/refresh.
  - Trả response yêu cầu verify account.

### 3.3 Login với account suspended/disabled

- Giữ nguyên policy hiện tại: không login thành công.

---

## 4) Service-layer contract

Service login cần hỗ trợ case `verification_required` và gọi OTT để tạo token xác minh nội bộ.

Contract nghiệp vụ cho case `pending_active`:
- Service gọi `Issue(ctx, "account_verify", userID)` để tạo OTT.
- Sau khi có OTT, service publish mail verify job vào Redis stream với payload contract cố định.
- Publish dùng cơ chế idempotency + atomic để tránh duplicate job trong môi trường HA.
- OTT plaintext chỉ đi trong payload mail job nội bộ (không trả ra login response).
- Login response không chứa access/refresh token.

Service không log, chỉ return error/state theo contract.

## 4.1 Redis stream contract (generic infra)

Publish phải đi qua component generic `infra/redis/stream.go` với contract:
- `StreamMessage { Stream, Payload, IdempotencyKey }`
- `Publish(ctx, msg, idempotencyTTL)`

Stream và payload do IAM định nghĩa ở caller layer:
- `stream`: khuyến nghị `mail:jobs`
- `payload` (map string-string) cho case verify account gồm:
  - `event_type=mail.verify_account.requested`
  - `purpose=account_verify`
  - `user_id`
  - `email`
  - `fullname`
  - `verify_token`
  - `requested_at` (RFC3339Nano UTC)
  - `request_id`
  - `idempotency_key`

Idempotency/atomic contract:
- `Publish` thực thi atomically bằng Lua: `SET NX EX` + `XADD`.
- Nếu key idempotency đã tồn tại thì coi là duplicate và không publish thêm job.
- `idempotency_key` đề xuất: `purpose:user_id:request_id`.


Observability contract khi publish Redis stream:
- Bắt buộc tạo tracing span cho bước publish mail job (ví dụ: `iam.login.publish_verify_mail_job`).
- Span cần có attributes tối thiểu: `stream`, `event_type`, `purpose`, `user_id`, `idempotency_key`, `published` (true/false).
- Bắt buộc emit metrics cho publish job, tối thiểu:
  - counter tổng số lần publish attempt
  - counter kết quả theo status (`success`, `duplicate`, `error`)
  - histogram latency publish
- Không đưa `verify_token` plaintext vào log hoặc trace attributes.

---

## 5) Handler mapping contract

### 5.1 Case authenticated

- Set cookie access + refresh như hiện tại.
- HTTP `200`.

### 5.2 Case verification_required

- Không set cookie access/refresh.
- HTTP `403`.
- Response body chỉ trả message yêu cầu user check mail để xác minh tài khoản.

### 5.3 Case invalid credentials

- Giữ nguyên mapping hiện tại (`401`).

---

## 6) Security contract

- Không log plaintext password.
- Không phân biệt chi tiết lỗi theo hướng lộ user enumeration.
- OTT tuân thủ spec one-time token hiện tại:
  - 1 active token / (`purpose`,`user_id`)
  - issue mới override token cũ
  - TTL cố định từ `config.IAM.OneTimeTokenTTL`
- Redis stream publish phải có idempotency + atomic để tránh duplicate mail job trong HA.

---

## 7) Error contract

Sử dụng nhóm lỗi hiện có, đồng bộ theo `ErrOneTimeToken...` và lỗi auth hiện hữu:
- `ErrInvalidCredentials`
- `ErrVerificationRequired`
- `ErrAuthenticationUnavailable` (khi không issue được OTT)

`ErrVerificationRequired` là trạng thái nghiệp vụ cho pending account khi đã xác thực credential đúng.

---

## 8) Acceptance criteria

- Login account `active` vẫn phát cookie access/refresh như cũ.
- Login account `pending_active`:
  - Không phát cookie access/refresh.
  - Trả `403` với message yêu cầu user check mail để verify account.
  - OTT được issue bằng `purpose=account_verify`, `user_id=user.ID`.
  - Publish đúng 1 mail verify job theo idempotency key trong cửa sổ `idempotencyTTL` khi gọi `Publish(...)`.
- Login account `suspended/disabled` không thành công.
- Có tracing + metrics cho bước publish mail job Redis stream (success/duplicate/error + latency).
- Không có log hoặc trace attribute chứa OTT plaintext token.

---

## 9) Phụ thuộc spec

- `controlplane/internal/iam/docs/spec/one-time-token-flow-v1-temp-spec.md`
- `controlplane/internal/iam/docs/spec/register-account-v1-temp-spec.md`
