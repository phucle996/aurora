package apperr

import (
	"errors"
	"strings"
)

type AppError struct {
	// Kind: domain error class (thường từ module errorx),
	// dùng để map HTTP/business semantics bằng errors.Is(...)
	Kind error

	// Reason: stable machine code (enum/string cố định),
	// dùng cho observability (log/metrics/trace) và phân nhóm lỗi.
	// Không dùng text động/SQL text ở đây.
	Reason string

	// Cause: raw technical cause (db/redis/network/runtime),
	// chỉ phục vụ debug nội bộ/log nội bộ, không trả ra client response.
	Cause error
}

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	base := "unknown"
	if e.Kind != nil {
		base = e.Kind.Error()
	}
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		return base
	}
	return base + ": " + reason
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

func Wrap(kind error, reason string, cause error) error {
	return &AppError{Kind: kind, Reason: strings.TrimSpace(reason), Cause: cause}
}

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

func LogFields(err error) map[string]any {
	appErr, ok := As(err)
	if !ok || appErr == nil {
		return nil
	}
	fields := map[string]any{}
	if appErr.Kind != nil {
		fields["error_kind"] = appErr.Kind.Error()
	}
	reason := strings.TrimSpace(appErr.Reason)
	if reason != "" {
		fields["error_reason"] = reason
	}
	if appErr.Cause != nil {
		fields["error_cause"] = sanitizeErrorCause(appErr.Cause.Error())
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func sanitizeErrorCause(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")

	lower := strings.ToLower(value)
	sensitiveHints := []string{"token", "secret", "api key", "apikey", "otp", "password", "authorization", "bearer"}
	for _, hint := range sensitiveHints {
		if strings.Contains(lower, hint) {
			return "[redacted_sensitive_cause]"
		}
	}

	if len(value) > 512 {
		return value[:512]
	}
	return value
}
