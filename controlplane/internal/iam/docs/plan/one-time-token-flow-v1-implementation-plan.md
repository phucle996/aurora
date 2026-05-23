# One-Time Token Flow V1 - Implementation Plan

## Mục tiêu

Tài liệu này chỉ mô tả **kế hoạch thay đổi code** (thêm/sửa/xóa folder, file, func) cho one-time token flow.
Spec nghiệp vụ nằm tại:
- `controlplane/internal/iam/docs/spec/one-time-token-flow-v1-temp-spec.md`

---

## 1) Phạm vi thay đổi

- Không thêm handler layer.
- Chỉ triển khai service + cache (Redis statement) + wiring module.
- TTL luôn lấy duy nhất từ `config.IAM.OneTimeTokenTTL` (không nhận từ input).

---

## 2) Kế hoạch thêm/sửa file

### 2.1 Thêm mới

- `controlplane/internal/iam/cache/one_time_token_cache.go`
  - Chứa statement Redis cho issue/consume token hash, TTL, atomic consume.

- `controlplane/internal/iam/service/one_time_token_service.go`
  - Business logic issue/consume.
  - Generate token qua `controlplane/internal/security/token.go`.
  - Hash token trước khi call cache.

- `controlplane/internal/iam/errorx/error.go`
  - Thêm `var (...)` cho nhóm lỗi one-time token flow, comment rõ block thuộc one-time token.

### 2.2 Sửa

- `controlplane/internal/config/config.go`
  - Dùng duy nhất `IAM.OneTimeTokenTTL` làm single source of truth (không đặt TTL ở chỗ khác).

- `controlplane/internal/iam/module.go`
  - Wiring cache + service để module khác có thể gọi service nội bộ.

- `controlplane/internal/iam/docs/spec/one-time-token-flow-v1-temp-spec.md`
  - Giữ ở mức đặc tả nghiệp vụ/contract, không liệt kê changelist chi tiết code.

### 2.3 Xóa (nếu có)

- Chưa có file cần xóa ở phase này.
- Nếu phát sinh helper tạm/thừa, sẽ liệt kê explicit trong changelog implementation trước khi xóa.

---

## 3) Contract func dự kiến

### Service

- `Issue(ctx, purpose, user_id) (plainToken string, expiresAt time.Time, err error)`
- `Consume(ctx, purpose, user_id, plainToken string) (consumed bool, err error)`

### Cache

- `SetHashedToken(ctx, purpose, user_id, tokenHash string, ttl time.Duration) error`
- `ConsumeHashedToken(ctx, purpose, user_id, tokenHash string) (bool, error)`

Ghi chú:
- Signature có thể tinh chỉnh theo style codebase nhưng phải giữ semantics từ spec.

---

## 4) Trình tự implement

1. Chốt config TTL trong `config.go`.
2. Tạo cache layer Redis statements.
3. Tạo service layer issue/consume (nhận tham số trực tiếp, không entity).
4. Wiring vào module.
5. Bổ sung test (svc unit, cache/repo integration theo guideline hiện hành).

---


## 5) Kế hoạch test (bắt buộc)

Test đặt theo cấu trúc hiện tại dưới `controlplane/internal/iam/test`:

### 5.1 Service tests (`svc_test`)

- File: `controlplane/internal/iam/test/svc_test/one_time_token_service_test.go`
- Cover cases:
  - Issue thành công với input hợp lệ.
  - Consume thành công đúng 1 lần.
  - Consume lần 2 fail `ErrOneTimeTokenInvalidOrExpired`.
  - `purpose`/`user_id` rỗng -> `ErrOneTimeTokenInvalidPurposeOrUser`.
  - `plainToken` rỗng -> `ErrOneTimeTokenInvalidOrExpired`.
  - TTL config không hợp lệ (`<=0`) -> `ErrOneTimeTokenIssueFailed`.
  - Cache error path -> `ErrOneTimeTokenIssueFailed` / `ErrOneTimeTokenConsumeFailed`.

### 5.2 Integration tests (`integration_test`)

- File: `controlplane/internal/iam/test/integration_test/one_time_token_integration_test.go`
- Dùng Redis thật trong harness test hiện có.
- Cover cases:
  - Issue overwrite token cũ cho cùng `purpose+user_id`.
  - Consume theo Lua atomic path (`DEL=1` rồi `DEL=0`).
  - TTL expire path -> consume fail.

### 5.3 Repo/cache tests (`repo_test` nếu cần)

- Nếu cần test riêng cache statement:
  - `controlplane/internal/iam/test/repo_test/one_time_token_cache_test.go`
- Tập trung validate key format, value hash, và semantics atomic consume.

## 6) Acceptance checklist

- Không có handler mới cho one-time token.
- TTL luôn đọc từ `config.IAM.OneTimeTokenTTL`, không fallback.
- Redis chỉ lưu hash, không lưu plaintext token.
- Consume một token chỉ thành công 1 lần.
- Lỗi trả về theo nhóm generic, không leak trạng thái token cụ thể.
