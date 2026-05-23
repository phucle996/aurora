# Admin Auth Bootstrap Flow (Temp Spec)

## 1) Mục tiêu

Định nghĩa contract cho luồng **bootstrap admin auth** để khởi tạo trạng thái bảo mật ban đầu cho kênh `/admin`.

Luu y: Spec nay chi la dac ta nghiep vu/contract; chi tiet thay doi code (them/sua/xoa folder/file/func) nam trong tai lieu plan.

Scope của spec này:
- Chỉ mô tả flow bootstrap ở service/business contract.
- Khởi tạo admin key singleton, 2FA admin, và recovery codes.
- Bắt buộc ghi `admin_action_audits` khi bootstrap thành công.
- Bắt buộc gửi thông tin bootstrap qua Telegram nội bộ khi bootstrap thành công.

Ngoài scope:
- Không mô tả chi tiết handler HTTP/gRPC.
- Không mô tả chi tiết UI/console vận hành.
- Không mô tả chi tiết cấu trúc payload Telegram (chốt ở spec vận hành).

---

## 2) Nguyên tắc kiến trúc

- Bootstrap là flow tách biệt, chỉ dùng để khởi tạo lần đầu hoặc theo policy recovery đặc biệt.
- Source-of-truth kiểm tra bootstrap-ready dựa trên dữ liệu `admin_api_keys` (singleton semantics).
- Admin auth là flow độc lập với user login flow thông thường.
- Secret/token material không được log plaintext.

---

## 3) Use cases bắt buộc

### 3.1 Bootstrap lần đầu

Khi hệ thống chưa có admin key active, bootstrap tạo đầy đủ security baseline:
- admin API key ban đầu,
- admin 2FA settings,
- admin recovery codes,
- audit + Telegram notify.

### 3.2 Bootstrap bị từ chối

Khi hệ thống đã có admin key active hoặc không đạt precondition bảo mật, bootstrap phải bị từ chối an toàn.

---

## 4) Service contract (đề xuất)

Lưu ý kiến trúc:
- Bootstrap là flow riêng ở tầng service/repo.
- Nhưng bootstrap **không tách thành bộ contract layer riêng**; nằm trong contract admin API key hiện hữu.
- Repository/Cache/Adapter chịu trách nhiệm statement cụ thể.

```go
type AdminAPIKeyService interface {
    Bootstrap(ctx context.Context, actor string) error
    // ...các method admin API key lifecycle khác
}
```

Ghi chú:
- Plain admin credential trả về theo policy vận hành an toàn; không persist plaintext.
- `actor` dùng cho audit trail bootstrap.
- Contract repo cho bootstrap cũng nằm trong `AdminAPIKeyRepository` (không tách repo contract riêng).

---

## 5) Preconditions contract

Bootstrap chỉ được phép chạy khi:
- Không tồn tại admin API key hợp lệ tại thời điểm kiểm tra (`admin_api_keys` không có row hoặc row hiện tại đã hết hạn).
- Kênh vận hành bootstrap hợp lệ theo policy nội bộ.
- Dependency bắt buộc hoạt động: DB + Redis + Telegram adapter.
- Acquire được bootstrap lock trong DB (Postgres advisory lock) cho môi trường HA.

Rule quyết định bootstrap đã chốt:
- Nếu đã có API key nhưng key hết hạn -> cho phép bootstrap.
- Nếu không có API key nhưng vẫn còn dữ liệu admin 2FA/recovery -> vẫn cho phép bootstrap.
- Quyết định cuối cùng chỉ dựa trên việc có/không có **admin API key hợp lệ**.
- Admin 2FA chỉ cho phép duy nhất 1 cấu hình/mã active; bootstrap chạy lại thì ghi đè cấu hình/mã đang có.
- Nếu bootstrap trước đó đã rollback vì Telegram final-fail và app restart, hệ thống không thấy admin API key hợp lệ thì phải cho phép bootstrap chạy lại.

Nếu bất kỳ precondition nào fail -> bootstrap fail đóng (deny).

---

## 6) Bootstrap flow contract

Thứ tự flow chuẩn:
1. Acquire bootstrap lock.
2. Validate preconditions.
3. Sinh admin key material theo policy entropy.
4. Hash key và ghi vào `admin_api_keys` cùng `expires_at`.
5. Khởi tạo/cập nhật `admin_2fa_settings` (TOTP policy).
6. Sinh và lưu hash batch `admin_recovery_codes`.
7. Ghi `admin_action_audits` với action bootstrap thành công.
8. Gọi `controlplane/infra/telegram/telegram.go` gửi thông tin bootstrap theo policy vận hành.
9. Bootstrap chỉ được coi là success khi Telegram notify success; nếu Telegram final-fail thì rollback DB.
10. Release bootstrap lock.
11. Trả kết quả bootstrap thành công.

Yêu cầu bắt buộc:
- Nếu bước ghi DB thành công nhưng Telegram fail, hệ thống phải retry gửi Telegram.
- Retry tối đa 3 lần liên tiếp.
- Nếu vẫn fail sau 3 lần liên tiếp, hệ thống phải rollback dữ liệu bootstrap vừa tạo, sau đó fail-closed bằng cách shutdown toàn app.
- Khi Telegram retry/final fail phải ghi log nội bộ đầy đủ để operator/dev xử lý.
- Không ghi thêm audit DB cho nhánh Telegram final-fail trước khi shutdown.
- Nếu Telegram final-fail, phải rollback dữ liệu bootstrap vừa tạo để không để lại trạng thái bootstrap dở dang trong DB.
- Rollback final-fail phải rollback sạch toàn bộ dữ liệu bootstrap đã ghi trong lần chạy đó, gồm:
  - `admin_api_keys`
  - `admin_2fa_settings`
  - `admin_recovery_codes`
  - record success trong `admin_action_audits` (nếu đã ghi)
- Batch recovery code cho admin cố định là 8 code.
- Format recovery code:
  - chỉ dùng chữ hoa (`A-Z`) và số (`0-9`)
  - độ dài cố định 24 ký tự cho mỗi code
  - case policy: uppercase-only
- Khi bootstrap chạy lại thành công, phải invalidate toàn bộ batch recovery code cũ và thay bằng batch mới.
- Nếu bootstrap rollback, batch recovery code vừa tạo trong lần bootstrap đó cũng phải rollback.
- Không được tạo multi-key window vi phạm singleton semantics.
- Không được để nhiều cấu hình/mã 2FA admin đồng thời; bootstrap luôn upsert/replace 2FA singleton.
- Lock bootstrap phải đảm bảo tại một thời điểm chỉ có 1 instance được phép chạy bootstrap.
- Bootstrap lock backend chốt dùng DB advisory lock, không dùng Redis lock cho flow này.

---

## 7) Transaction & consistency contract

- Các bước dữ liệu cốt lõi trong DB phải chạy trong transaction phù hợp.
- Nếu fail tại bước cốt lõi (key/2FA/recovery/audit), transaction rollback.
- Rollback dữ liệu bootstrap ở nhánh Telegram final-fail phải chạy trong 1 transaction duy nhất.
- Side-effect Telegram có policy bắt buộc:
  - retry tối đa 3 lần,
  - fail cả 3 lần thì rollback dữ liệu bootstrap vừa tạo trước, rồi shutdown toàn app (fail-closed),
  - ghi log nội bộ,
  - không ghi thêm audit DB ở nhánh final-fail này.
- Bootstrap chỉ được coi là completed khi cả DB data path và Telegram notify đều thành công.
- Bootstrap lock luôn phải được release bằng cơ chế `defer/finalizer` kể cả khi flow lỗi.
- Nếu mất lock/DB connection giữa chừng: abort flow ngay và rollback transaction.

---

## 8) Error contract

Nhóm lỗi gợi ý:
- `ErrAdminBootstrapNotAllowed`
- `ErrAdminBootstrapPreconditionFailed`
- `ErrAdminBootstrapPersistFailed`
- `ErrAdminBootstrapAuditFailed`
- `ErrAdminBootstrapNotifyFailed`

Public response:
- Generic message, không lộ nguyên nhân nội bộ hoặc secret state.

Internal logging:
- Log trực tiếp raw error nội bộ để debug vận hành (không bắt buộc reason-code cố định).

---

## 9) Security invariants

- Không lưu plaintext admin key/recovery code trong DB.
- Không log plaintext secret/token/recovery code.
- Bootstrap thành công luôn có audit record trong `admin_action_audits`.
- Bootstrap thành công luôn có Telegram notification attempt.
- Hệ thống không được rơi vào trạng thái "bootstrap nửa chừng" mà thiếu audit/log.
- Admin 2FA luôn tuân thủ singleton semantics (duy nhất 1 cấu hình/mã active tại một thời điểm).
- Không được để lại dữ liệu bootstrap usable nếu Telegram notify final-fail.
- Admin recovery codes dùng batch cố định 8 code theo policy hiện tại.
- Admin recovery code format cố định: uppercase alphanumeric, dài 24 ký tự.

---

## 10) Logging & audit contract

Audit events tối thiểu:
- `admin_bootstrap_started`
- `admin_bootstrap_succeeded`
- `admin_bootstrap_failed`

Logging tối thiểu:
- log raw error theo từng nhóm lỗi bootstrap; caller dùng `logger.SysError("iam.bootstrap.apitoken", err.Error())`.

---

## 10.1) Operational defaults (V1)

### Bootstrap lock
- Lock backend: DB advisory lock (Postgres)
- Cơ chế acquire: non-blocking try-lock, nếu không acquire được thì fail ngay
- Lock gắn với dedicated DB connection của flow bootstrap
- Release lock bắt buộc bằng `defer/finalizer`
- Nếu mất DB connection/lock giữa chừng: abort flow ngay, rollback rồi trả lỗi cho caller

### Telegram retry cadence
- Retry tối đa: `3` lần
- Backoff: exponential `1s -> 2s -> 4s`
- Timeout mỗi lần gửi Telegram: `5s`
- Tổng timeout tối đa cho notify phase: `20s`

### Shutdown semantics (final-fail)
- Khi vào nhánh final-fail: chặn bootstrap trigger mới ngay lập tức
- Thứ tự bắt buộc: rollback sạch -> log fatal -> graceful shutdown
- Graceful shutdown window: `10s`, sau đó `os.Exit(1)`

---

## 11) Data & infra dependencies

DB tables:
- `admin_api_keys`
- `admin_action_audits`
- `admin_2fa_settings`
- `admin_recovery_codes`

Infra:
- Telegram adapter: `controlplane/infra/telegram/telegram.go`

---

## 12) Acceptance criteria cho vòng implement

- Bootstrap chỉ chạy khi không có admin API key hợp lệ (không có key hoặc key đã hết hạn).
- Bootstrap thành công tạo đủ: admin key + admin 2FA settings + recovery codes.
- Có bản ghi `admin_action_audits` khi bootstrap thành công.
- Có gọi Telegram notify khi bootstrap thành công.
- Không lộ plaintext secret trong DB/log.
- Lỗi bootstrap trả generic response nhưng có internal raw-error log ở caller.

---

## 13) Quyết định đã chốt cho flow này

- Admin bootstrap là flow riêng, không reuse user bootstrap.
- Admin 2FA là mandatory-on trong policy hiện tại.
- Admin 2FA singleton: bootstrap nếu đã có thì ghi đè.
- Admin recovery code là one-time semantics.
- Admin audit bắt buộc cho bootstrap success.
- Telegram notify là bắt buộc trong bootstrap success path.
