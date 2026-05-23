# Login V2 Pending Active Verify OTT - Implementation Plan

## Mục tiêu

Tài liệu này chỉ mô tả **kế hoạch thay đổi code** (thêm/sửa/xóa folder, file, func) cho login v2 khi account `pending_active`.
Spec nghiệp vụ nằm tại:
- `controlplane/internal/iam/docs/spec/login-v2-pending-active-verify-ott-temp-spec.md`

---

## 1) Phạm vi thay đổi

- Không thêm route mới trong phase này.
- Chỉ cập nhật login flow hiện có ở service + handler.
- Khi `pending_active`: issue OTT và publish mail verify job vào Redis stream generic (`infra/redis/stream.go`).
- Response login chỉ trả message check mail, không trả OTT token.

---


## 1.1 Boundary bắt buộc

- `one_time_token_cache` và `redis stream job` là 2 lớp khác nhau, không merge:
  - OTT cache: state token one-time (`token_hash`) để verify/consume.
  - Stream: queue job gửi mail verify.
- Không publish mail job vào OTT cache keyspace.
- Không dùng stream message làm nguồn xác thực OTT.

## 2) Kế hoạch thêm/sửa file

### 2.1 Thêm mới

- Không thêm file  mới.
- Mapper payload mail job viết inline trực tiếp trong luồng chính `auth_service.go` (nhánh `pending_active`).

### 2.2 Sửa

- `controlplane/internal/iam/service/auth_service.go`
  - Update nhánh login `pending_active`:
    - Gọi OTT service `Issue(ctx, "account_verify", userID)`.
    - Build stream payload mail verify inline (không tách helper file).
    - Gọi generic stream publisher `Publish(ctx, msg, idempotencyTTL)`.
    - Return trạng thái `ErrVerificationRequired` để handler map 403 check-mail.
  - Không log trong service.

- `controlplane/internal/iam/module.go`
  - Fail-fast validate dependency cho login v2 flow:
    - Redis client bắt buộc.
    - Stream publisher dependency bắt buộc.
    - OTT service dependency bắt buộc.
  - Wiring `authService` với OTT service + stream publisher.

- `controlplane/internal/iam/transport/http/handler/auth_handler.go`
  - Mapping `ErrVerificationRequired` -> `403` + message check mail.
  - Đảm bảo không set access/refresh cookie ở nhánh verification required.

- `controlplane/internal/iam/metrics/login.go`
  - Bổ sung metric cho bước publish verify mail job:
    - attempt
    - success/duplicate/error
    - latency

- `controlplane/internal/iam/docs/flow/login-flow.md`
  - Cập nhật flow tài liệu theo login v2 pending_active.

- `controlplane/internal/iam/docs/spec/login-v2-pending-active-verify-ott-temp-spec.md`
  - Chỉ update nếu cần đồng bộ nhỏ sau implement, không chuyển thành changelog.

### 2.3 Xóa (nếu có)

- Chưa có file cần xóa ở phase này.

---

## 3) Contract func dự kiến

### AuthService login branch

- `Issue(ctx, "account_verify", userID)` từ OTT service.
- `Publish(ctx, msg, idempotencyTTL)` từ stream publisher generic.

### Stream message payload (caller IAM)

Payload map dự kiến gồm:
- `event_type=mail.verify_account.requested`
- `purpose=account_verify`
- `user_id`
- `email`
- `fullname`
- `verify_token`
- `requested_at`
- `request_id`
- `idempotency_key`

Ghi chú:
- `idempotency_key` sinh theo công thức cố định: `purpose:user_id:request_id`.
- `idempotencyTTL` dùng đúng `config.IAM.OneTimeTokenTTL` (không dùng TTL khác, không hardcode).

---

## 4) Trình tự implement

1. Cập nhật `auth_service.go` cho nhánh `pending_active` (issue OTT + publish stream).
2. Cập nhật `module.go` wiring + fail-fast dependency checks.
3. Cập nhật `auth_handler.go` mapping 403 check-mail và chặn set cookie ở nhánh verify.
4. Bổ sung tracing + metrics cho bước publish job.
5. Cập nhật docs flow login.
6. Viết/chỉnh test cho service + transport mapping.
7. Chạy test package IAM và chốt acceptance.

---

## 5) Acceptance checklist

- Login account `active` giữ nguyên hành vi hiện tại.
- Login account `pending_active`:
  - Issue OTT `purpose=account_verify` thành công.
  - Publish đúng 1 mail verify job theo idempotency key `purpose:user_id:request_id` trong cửa sổ TTL = `config.IAM.OneTimeTokenTTL`.
  - Trả `403` message check mail.
  - Không set access/refresh cookie.
- Có tracing span + metrics cho publish mail job (success/duplicate/error + latency).
- Không log/token leak (`verify_token` plaintext không đi vào log/trace attrs).
- `go test ./internal/iam/...` pass.
