// ======================================================================================================
// 📂 PKG: controlplane/pkg/apperr/app_error.go
//         Error Taxonomy Layer — Bộ Phân Loại Lỗi Có Cấu Trúc Của Toàn Hệ Thống
// ======================================================================================================
//
// 📜 VAI TRÒ:
//   - Carrier gọn nhẹ mang Kind, diagnostic class và Cause đã bị chặn khỏi response.
//   - KHÔNG mang metric taxonomy; Service ghi result/reason qua recorder trung tâm.
//   - Handler là nơi DUY NHẤT ghi error log; workflow recorder sở hữu metric/trace outcome.
//
// 🔄 CALLSITE FLOW:
//   Service layer  → apperr.Wrap(ErrXxx, rawErr)
//                  → observability workflow recorder   ← defer trong service
//                  → trả error về Handler
//                        ↓
//   Handler layer  → errors.Is(err, ErrXxx)        → map HTTP status code
//                  → logger.HandlerError(c, op, err)
//                       → apperr.LogFields(err) → inject error_class và sanitized cause
//
// 💡 CORRELATION BRIDGE:
//   result/reason thuộc request correlation state do workflow recorder ghi.
//   AppError class chỉ là diagnostic field; nó không được dùng làm metric label.
//
// ⚠️  GIỮ LẠI package này vì:
//   - errors.Is() chain phụ thuộc Unwrap() → không thể thay bằng errors.New() đơn giản.
//   - sanitizeErrorCause bảo vệ khỏi sensitive string (token/secret) leak vào Loki log.
//
// ======================================================================================================

package apperr

import (
	"errors"
	"regexp"
	"strings"
)

var credentialURLPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s@/]+@`)

// AppError là carrier lỗi mang Kind, diagnostic Class và raw Cause.
type AppError struct {
	// Kind: sentinel error từ module taxonomy — SoT duy nhất để Handler map HTTP status.
	// Ví dụ: iamTaxonomy.ErrInvalidCredentials → 401, iamTaxonomy.ErrUnavailable → 503.
	// PHẢI là sentinel error cố định, KHÔNG dùng runtime string.
	Kind error

	// Class là diagnostic class hữu hạn do workflow chọn. Nó không thay thế
	// result/reason và không được đưa vào metric label.
	Class string

	// Cause: raw error gốc từ dependency (db/redis/network/runtime).
	// Chỉ dùng để log debug qua LogFields() sau khi sanitize — không trả ra client.
	Cause error
}

// Error trả Kind.Error() để tương thích interface error.
// Không chứa thêm thông tin — diagnostic class và cause được log riêng tại Handler.
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

// Wrap tạo AppError từ kind + cause, với diagnostic class tùy chọn.
// Hàm duy nhất để tạo lỗi trong Service Layer — không khởi tạo AppError trực tiếp.
//   - kind:    sentinel error từ errorx (SoT cho HTTP mapping).
//   - cause:   raw error từ dependency, nil nếu lỗi thuần business logic.
//   - class: tùy chọn (variadic, lấy phần tử đầu tiên nếu có).
func Wrap(kind error, cause error, class ...string) error {
	app := &AppError{Kind: kind, Cause: cause}
	if len(class) > 0 {
		app.Class = strings.TrimSpace(class[0])
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
// Được gọi bởi logger trong Handler log path.
// Trả nil nếu err không phải AppError và không có field nào để emit — logger xử lý nil an toàn.
//
// Output fields:
//   - "error_class" : bounded diagnostic class (chỉ có khi Class != "").
//   - "error_cause" : raw Cause đã sanitize (chỉ có khi Cause != nil).
func LogFields(err error) map[string]any {
	appErr, ok := As(err)
	if !ok || appErr == nil {
		return nil
	}
	fields := map[string]any{}
	if class := strings.TrimSpace(appErr.Class); class != "" {
		fields["error_class"] = class
	}
	if appErr.Cause != nil {
		fields["error_cause"] = sanitizeErrorCause(appErr.Cause.Error())
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// SanitizeLogText bounds and redacts a diagnostic string before it enters a
// structured log field or body. It must not be used to make an unsafe value
// safe for a business response.
func SanitizeLogText(value string) string {
	return sanitizeErrorCause(value)
}

// sanitizeErrorCause làm sạch raw Cause string trước khi đưa vào Loki log.
// Hai mục tiêu:
//  1. Bảo mật: redact toàn bộ nếu có dấu hiệu sensitive (token/secret/password/otp/bearer).
//  2. Kích thước: cắt cứng tại 512 ký tự để tránh Loki ingest bị choke.
//
// LogFields() và logger's bounded message path use this function; callers must
// never treat its output as safe for a client response.
func sanitizeErrorCause(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	// Chuẩn hóa newline để không vỡ JSON log format.
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")

	lower := strings.ToLower(value)
	if credentialURLPattern.MatchString(value) {
		// URI userinfo can carry a password even when no conventional field name
		// appears in the provider error (for example postgres://user:pass@host).
		return "[redacted_sensitive_cause]"
	}
	sensitiveHints := []string{
		"token", "secret", "credential", "api key", "apikey", "access key",
		"private key", "otp", "password", "authorization", "bearer",
	}
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
