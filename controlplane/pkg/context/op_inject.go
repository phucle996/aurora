package pkgcontext

import (
	stdcontext "context"

	"controlplane/pkg/logger"
)

// OperationKeyType là kiểu dữ liệu riêng cho context key của Operation Name (workflow)
// nhằm bảo đảm an toàn kiểu dữ liệu và tránh xung đột key trong context.
type OperationKeyType struct{}

// OperationKey là instance duy nhất làm key để lưu trữ/truy xuất tên operation.
var OperationKey = OperationKeyType{}

// WithOperation tiêm tên operation/workflow vào Go context hiện tại.
func WithOperation(ctx stdcontext.Context, op string) stdcontext.Context {
	ctx = logger.WithCorrelation(ctx)
	logger.SetCorrelationOperation(ctx, op)
	return stdcontext.WithValue(ctx, OperationKey, op)
}

// GetOperation trích xuất tên operation từ Go context.
// Trả về "unknown" làm fallback nếu không tìm thấy key trong context.
func GetOperation(ctx stdcontext.Context) string {
	if ctx == nil {
		return "unknown"
	}
	// Kiểm tra xem value có kiểu string hay không
	if op, ok := ctx.Value(OperationKey).(string); ok {
		return op
	}
	return "unknown"
}
