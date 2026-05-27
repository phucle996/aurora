// ======================================================================================================
// 📂 PKG: controlplane/pkg/apperr/app_error.go
//         Error Taxonomy Layer — Bộ Phân Loại Lỗi Có Cấu Trúc Của Toàn Hệ Thống
// ======================================================================================================
//
// 📜 VAI TRÒ:
//   - Carrier gọn nhẹ mang 2 thứ: Kind (taxonomy errorx để Handler map HTTP code) và Cause (raw debug).
//   - KHÔNG mang Reason — outcome label được Service quản lý riêng và TRẢ về Handler để log.
//   - Handler là nơi DUY NHẤT ghi log: nhận outcome từ Service, nhận Cause từ AppError, tự log.
//
// 🔄 CALLSITE FLOW:
//   Service layer  → apperr.Wrap(ErrXxx, rawErr)
//                  → iamMetrics.Observe(outcome)   ← defer trong svc, Prometheus label
//                  → trả (result, outcome, err) về Handler
//                        ↓
//   Handler layer  → errors.Is(err, ErrXxx)        → map HTTP status code
//                  → logger.HandlerError(c, op, outcome, err)
//                       → apperr.LogFields(err)    → inject error_cause (sanitized) vào JSON log
//                       → log outcome field        → bridge key để SRE join Loki ↔ Prometheus
//
// 💡 CORRELATION BRIDGE:
//   `outcome` string được emit ở cả 2 nơi với CÙNG giá trị:
//     Prometheus: iam_login_total{outcome="invalid_credentials"}
//     Loki:       outcome=invalid_credentials (field trong JSON log)
//   SRE có thể join trực tiếp mà không cần parse error string hay dùng regex.
//
// ⚠️  GIỮ LẠI package này vì:
//   - errors.Is() chain phụ thuộc Unwrap() → không thể thay bằng errors.New() đơn giản.
//   - sanitizeErrorCause bảo vệ khỏi sensitive string (token/secret) leak vào Loki log.
//
// ======================================================================================================

package apperr

import (
	"errors"
	"strings"
)

// AppError là carrier lỗi mang 3 thứ: Kind (HTTP mapping), Cause (raw debug), Outcome (bridge key).
// Outcome là field tùy chọn — được dùng làm correlation key chung giữa Loki log và Prometheus label.
type AppError struct {
	// Kind: sentinel error từ module taxonomy — SoT duy nhất để Handler map HTTP status.
	// Ví dụ: iamTaxonomy.ErrInvalidCredentials → 401, iamTaxonomy.ErrUnavailable → 503.
	// PHẢI là sentinel error cố định, KHÔNG dùng runtime string.
	Kind error

	// Outcome: coarse label string để làm correlation key giữa Loki log và Prometheus metric.
	// Ví dụ: "invalid_credentials", "dependency_error", "success".
	// Tùy chọn (empty string = không set) — Handler log ra field `outcome` trong JSON log.
	// Service emit cùng giá trị này lên Prometheus để SRE có thể join hai hệ thống.
	Outcome string

	// Cause: raw error gốc từ dependency (db/redis/network/runtime).
	// Chỉ dùng để log debug qua LogFields() sau khi sanitize — không trả ra client.
	Cause error
}

// Error trả Kind.Error() để tương thích interface error.
// Không chứa thêm thông tin — outcome và cause được log riêng tại Handler.
func (e *AppError) Error() string {
	if e == nil || e.Kind == nil {
		return "unknown"
	}
	return e.Kind.Error()
}

// Unwrap cho phép errors.Is() và errors.As() traverse qua Kind.
// Đây là lý do AppError tồn tại thay vì dùng errors.New() đơn giản.
func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

// Wrap tạo AppError từ kind + cause, với outcome tùy chọn.
// Hàm duy nhất để tạo lỗi trong Service Layer — không khởi tạo AppError trực tiếp.
//   - kind:    sentinel error từ errorx (SoT cho HTTP mapping).
//   - cause:   raw error từ dependency, nil nếu lỗi thuần business logic.
//   - outcome: tùy chọn (variadic, lấy phần tử đầu tiên nếu có) — coarse label string
//              dùng làm bridge key giữa Loki log và Prometheus metric.
func Wrap(kind error, cause error, outcome ...string) error {
	app := &AppError{Kind: kind, Cause: cause}
	if len(outcome) > 0 {
		app.Outcome = strings.TrimSpace(outcome[0])
	}
	return app
}

// As extract *AppError từ error chain.
// Dùng khi cần đọc Cause trực tiếp thay vì qua LogFields().
// Trả (nil, false) nếu err nil hoặc không phải AppError — không panic.
func As(err error) (*AppError, bool) {
	if err == nil {
		return nil, false
	}
	var appErr *AppError
	if !errors.As(err, &appErr) {
		return nil, false
	}
	return appErr, true
}

// LogFields trả map để inject vào JSON log tại Handler.
// Được gọi bởi logger.appendAppErrorFields() trong Handler log path.
// Trả nil nếu err không phải AppError và không có field nào để emit — logger xử lý nil an toàn.
//
// Output fields:
//   - "outcome"     : bridge key chung với Prometheus label (chỉ có khi Outcome != "").
//   - "error_cause" : raw Cause đã sanitize (chỉ có khi Cause != nil).
func LogFields(err error) map[string]any {
	appErr, ok := As(err)
	if !ok || appErr == nil {
		return nil
	}
	fields := map[string]any{}
	if o := strings.TrimSpace(appErr.Outcome); o != "" {
		// outcome là bridge key — cùng giá trị được emit lên Prometheus bởi Service defer.
		fields["outcome"] = o
	}
	if appErr.Cause != nil {
		fields["error_cause"] = sanitizeErrorCause(appErr.Cause.Error())
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// sanitizeErrorCause làm sạch raw Cause string trước khi đưa vào Loki log.
// Hai mục tiêu:
//   1. Bảo mật: redact toàn bộ nếu có dấu hiệu sensitive (token/secret/password/otp/bearer).
//   2. Kích thước: cắt cứng tại 512 ký tự để tránh Loki ingest bị choke.
//
// Callsite duy nhất: LogFields() — không gọi trực tiếp từ nơi khác.
func sanitizeErrorCause(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	// Chuẩn hóa newline để không vỡ JSON log format.
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")

	lower := strings.ToLower(value)
	sensitiveHints := []string{"token", "secret", "api key", "apikey", "otp", "password", "authorization", "bearer"}
	for _, hint := range sensitiveHints {
		if strings.Contains(lower, hint) {
			// Redact toàn bộ — không partial vì context xung quanh vẫn có thể leak.
			return "[redacted_sensitive_cause]"
		}
	}

	if len(value) > 512 {
		return value[:512]
	}
	return value
}
