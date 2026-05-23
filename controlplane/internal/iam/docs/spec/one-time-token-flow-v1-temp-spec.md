# One-Time Token Flow V1 (Temp Spec)

## 1) Mục tiêu

Định nghĩa contract cho luồng **one-time token** trong IAM để các service nội bộ dùng chung.

Luu y: Spec nay chi la dac ta nghiep vu/contract; chi tiet thay doi code (them/sua/xoa folder/file/func) nam trong tai lieu plan.

Scope của spec này:
- Chỉ **service layer** (không có handler HTTP/gRPC).
- Token sinh từ `controlplane/internal/security/token.go`.
- Chỉ lưu `hash(token)` vào Redis với TTL lấy duy nhất từ `config.IAM.OneTimeTokenTTL`.
- Trả **plain token** cho caller service đúng 1 lần khi issue.

Ngoài scope:
- Chưa mở public API.
- Chưa định nghĩa consumer cụ thể (sẽ làm sau).

---

## 2) Nguyên tắc kiến trúc

- Layer chuẩn: `Service (business logic + khoi tao entity) -> Cache (Redis statements)`.
- Service chua logic nghiep vu va khoi tao entity; khong viet statement Redis truc tiep trong service.
- `internal/iam/cache` chi chua statement call Redis (SET/GET/DEL/Lua/pubsub neu can).
- Khong dua logic one-time token vao handler layer.
- Không persist DB cho flow này ở v1; Redis là storage runtime.
- Không log plaintext token.
- Không log raw hash token.

---

## 3) Use cases bắt buộc

### 3.1 Issue token

Service tạo token dùng một lần, lưu hash vào Redis với TTL, trả plaintext token cho caller.

Rule bắt buộc: tại 1 thời điểm chỉ có 1 token active cho 1 cặp `purpose` + `user_id`; nếu issue mới thì phải override token cũ ngay lập tức.

### 3.2 Consume token

Service verify token và consume theo semantics one-time:
- Token hợp lệ -> consume thành công đúng 1 lần.
- Reuse token đã consume -> fail.
- Token hết hạn -> fail.
- Token sai -> fail.

---

## 4) Service contract (đề xuất)

Lưu ý kiến trúc:
- Khong dung entity cho flow nay o V1; service nhan tham so truc tiep va tra ket qua truc tiep.
- Khong dung Input/Output struct cho service contract.

```go
type OneTimeTokenService interface {
    Issue(ctx context.Context, purpose string, userID string) (plainToken string, expiresAt time.Time, err error)
    Consume(ctx context.Context, purpose string, userID string, plainToken string) (consumed bool, err error)
}
```

Ghi chú:
- `purpose` là bắt buộc để tách namespace nghiệp vụ.
- `userID` là định danh user trong hệ thống IAM.
- `plainToken` chỉ xuất hiện ở service input/output, không lưu thô.

---

## 5) Redis contract

## 5.1 Key format

Key format cố định:

```txt
iam:ott:{purpose}:{user_id}
```

Trong đó:
- `{purpose}`: normalize lower-case, whitelist `[a-z0-9._-]`.
- `{user_id}`: normalize theo quy ước domain.
- Value lưu `token_hash` đã hash một chiều từ plaintext token.

## 5.2 Value

Value là chuỗi `token_hash` đã băm một chiều từ plaintext token.

Ví dụ:

```txt
<token_hash>
```

V1 chỉ lưu `token_hash` (không lưu plaintext), không lưu metadata runtime.

## 5.3 TTL

- TTL cố định lấy duy nhất từ `config.IAM.OneTimeTokenTTL` (source of truth).
- Không nhận TTL từ input request, không set TTL ở bất kỳ layer nào khác.
- Không có TTL per-purpose và không có fallback TTL.

## 5.4 Atomic one-time consume

Consume phải atomic để tránh race ở môi trường HA.

Yêu cầu kỹ thuật v1:
- Dùng Redis primitive đảm bảo xóa đúng 1 lần cho 1 key.
- Khuyến nghị: Lua script (`GET` + compare + `DEL`) hoặc flow set key theo hash và `DEL` trực tiếp theo key hash đã tính.

Semantics:
- `DEL = 1` => consume thành công.
- `DEL = 0` => token không tồn tại (expired/used/invalid) => fail generic.

---

## 6) Token + hash contract

## 6.1 Generate

- Dùng token generator từ `controlplane/internal/security/token.go`.
- Entropy đủ mạnh cho secret runtime (không tự viết random thủ công ở service).

## 6.2 Hash

- Dùng hash một chiều (khuyến nghị SHA-256 hoặc chuẩn đang dùng nội bộ security).
- Lưu/so sánh theo hash output đã normalize.

## 6.3 Không lưu plaintext

- Không lưu plaintext token trong Redis/DB/log/audit metadata.
- Plaintext chỉ trả về ngay tại `Issue` cho caller service.

---

## 7) Error contract

Service trả lỗi theo nhóm, message an toàn (không enum token tồn tại hay không):

- `ErrOneTimeTokenInvalidPurposeOrUser`
- `ErrOneTimeTokenIssueFailed`
- `ErrOneTimeTokenInvalidOrExpired` (gộp invalid/expired/used)
- `ErrOneTimeTokenConsumeFailed`

Không phân biệt public message giữa:
- token sai
- token đã dùng
- token hết hạn

---

## 8) Error handling contract

- Service layer không log cho flow này.
- Service chỉ return error theo contract (`ErrOneTimeToken...`).
- Caller (service/handler/job gọi lên) chịu trách nhiệm bắt lỗi và quyết định log/metrics theo context của caller.
- Dù caller log thì vẫn không được log `plain_token` hoặc `token_hash` raw.

---

## 9) Security invariants

- Mỗi token chỉ consume thành công tối đa 1 lần.
- Token tự vô hiệu khi hết TTL.
- Không leak thông tin để user enumeration/token enumeration.
- Safe dưới concurrent/HA do consume atomic trên Redis.

---

## 10) Acceptance criteria cho vòng implement

- Có service `Issue/Consume` không cần handler.
- TTL luôn đọc đúng từ `config.IAM.OneTimeTokenTTL`.
- Redis chỉ chứa hash-key/value, không chứa plaintext token.
- Consume cùng token 2 lần:
  - lần 1 success
  - lần 2 fail `ErrOneTimeTokenInvalidOrExpired`
- Log/metrics có đủ tín hiệu debug mà không lộ secret.

---

## 11) Quyết định đã chốt V1

- Chỉ cho phép 1 active one-time token tại 1 thời điểm trên mỗi cặp `purpose` + `user_id`; issue mới sẽ override token cũ.
- Không binding `tenant_id/workspace_id`.
- Không lưu audit event cho issue/consume ở V1.
- TTL cố định duy nhất từ `config.IAM.OneTimeTokenTTL`, không có TTL per-purpose, không fallback.
