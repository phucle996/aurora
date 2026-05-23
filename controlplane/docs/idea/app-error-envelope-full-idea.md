# Shared App Error Envelope (Full Idea)

## 1) Problem

Hiện tại nhiều flow (đặc biệt auth/iam) cần đồng thời:
- map lỗi về HTTP theo domain error,
- log được nguyên nhân kỹ thuật chi tiết,
- giữ reason code ổn định cho metrics/alert.

Nếu chỉ trả `error` thường hoặc dùng `err.Error()` trực tiếp:
- handler khó phân biệt `kind` vs `reason` vs `cause`,
- dễ leak thông tin nội bộ ra response/log không kiểm soát,
- reason không ổn định (đặc biệt lỗi SQL/driver), gây metric cardinality cao.

Nếu không chuẩn hóa sớm, mỗi module sẽ tự làm một kiểu error envelope, drift rất nhanh và khó vận hành production.

---

## 2) Context

- Controlplane đang có nhiều module (`iam`, `core`, ...), mỗi module có `errorx` riêng để map business/domain error.
- Handler hiện chịu trách nhiệm map HTTP và log.
- Nhu cầu mới là forward được cả primitive reason + raw cause từ service/repo lên handler để log, nhưng response client vẫn generic/safe.
- Hệ observability (Prometheus/Loki/Grafana) đã bật, nên reason code ổn định rất quan trọng để làm dashboard/alert.

Ràng buộc:
- Không phá boundary module-local error mapping.
- Không biến shared package thành nơi chứa domain sentinel của từng module.
- Không dùng raw SQL error string làm reason label trực tiếp.

---

## 3) Solution Direction

Đưa một **shared error envelope** vào `pkg` để dùng toàn app, còn domain sentinel vẫn giữ module-local.

Hướng đề xuất:
- Tạo shared type (ví dụ `pkg/apperr`):
  - `Kind` (domain class để map HTTP),
  - `Reason` (stable reason code),
  - `Cause` (raw cause để log nội bộ).
- Service/Repo wrap lỗi theo envelope này để forward đầy đủ ngữ cảnh.
- Handler dùng:
  - `errors.Is(err, Kind)` để map status code,
  - bóc `Reason/Cause` để log/metric.

Nguyên tắc vận hành:
- `errorx` trong từng module đóng vai trò **domain class / kind** (nguồn để map HTTP/business semantics).
- Shared `AppError` envelope chỉ mang thêm `Reason` + `Cause`, không thay thế domain class của module.
- Client response luôn generic theo security policy.
- `Reason` phải là enum/string ổn định (không dùng raw DB text).
- `Cause` giữ nguyên để debug, nhưng chỉ dùng trong internal logs.

Tại sao hướng này:
- Chuẩn hóa contract lỗi liên module,
- Tăng khả năng debug incident,
- Giữ được clean boundary (shared envelope + module-local domain errors).

Success signal (high-level):
- 100% flow auth/critical có reason code ổn định trong logs,
- giảm thời gian xác định root cause khi incident auth/login,
- không phát sinh leak thông tin nội bộ ra response.

---

## 4) Non-goals

- Không thay toàn bộ error mapping của mọi module trong một lần.
- Không thay đổi public API error contract ở giai đoạn idea này.
- Không thiết kế taxonomy đầy đủ cho toàn bộ domain ngay lập tức.
- Không áp dụng raw `err.Error()` làm reason label cho metrics.

