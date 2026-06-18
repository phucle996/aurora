package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

// OutboxStatus đại diện cho trạng thái hiện tại của công việc trong Outbox
type OutboxStatus string

const (
	// OutboxStatusPending: Trạng thái ban đầu khi công việc (connection test) vừa được khởi tạo và ghi nhận bền vững vào DB.
	OutboxStatusPending OutboxStatus = "PENDING"

	// OutboxStatusProcessing: Đánh dấu công việc đã được Dataplane Worker đón nhận từ Redis Stream và đang chạy kiểm tra bắt tay SMTP.
	OutboxStatusProcessing OutboxStatus = "PROCESSING"

	// OutboxStatusSucceeded: Kết quả kiểm tra SMTP Handshake thành công hoàn toàn (Trạng thái cuối cùng).
	OutboxStatusSucceeded OutboxStatus = "SUCCEEDED"

	// OutboxStatusFailed: Kết quả kiểm tra thất bại do cấu hình sai, kết nối lỗi hoặc quá thời gian Timeout (Trạng thái cuối cùng).
	OutboxStatusFailed OutboxStatus = "FAILED"
)

// MailOutboxRecord định nghĩa cấu trúc của bản ghi sự kiện Outbox dùng cho việc giao tiếp phi tập trung (CDC).
type MailOutboxRecord struct {
	// ID: Khóa chính tự tăng (BIGSERIAL) giúp tối ưu đánh chỉ mục vật lý và xác định thứ tự tuần tự trong Postgres
	ID int64
	// EventID: UUID định danh duy nhất toàn cục của sự kiện, dùng làm Idempotency Key chống trùng lặp giữa các node
	EventID uuid.UUID
	// ZoneID: UUID phân vùng tài nguyên (Multi-tenancy), xác định Redis Stream đích của dataplane
	ZoneID uuid.UUID
	// JobTopic: Tên loại công việc hoặc topic sự kiện (ví dụ: "mail.test_connection")
	JobTopic string
	// Payload: Nội dung nhị phân (Protobuf) chứa tham số cấu hình hoặc dữ liệu chi tiết của công việc
	Payload []byte
	// UserID: ID của người dùng thực hiện yêu cầu (có thể là UUID hoặc định danh của hệ thống/SRE
	// Do đó sử dụng kiểu string) để phục vụ thông báo real-time qua CDC
	UserID string
	// Status: Trạng thái xử lý sự kiện (PENDING, PROCESSING, SUCCEEDED, FAILED)
	Status OutboxStatus
	// CompletedAt: Thời điểm công việc hoàn tất xử lý (succeeded hoặc failed)
	CompletedAt *time.Time
	// JobVersion: Phiên bản logic của Job chạy ở Dataplane
	JobVersion uint32
	// ResourceID: Định danh tài nguyên đích liên quan đến sự kiện (ví dụ: SMTP endpoint ID)
	ResourceID string
	// PayloadSchemaVersion: Phiên bản cấu trúc dữ liệu của Payload (Schema versioning)
	PayloadSchemaVersion uint32
	// TraceID: Mã định danh phân tán (OpenTelemetry Trace ID) dùng để liên kết chuỗi vết hoạt động (Distributed Tracing)
	// Đã được tối ưu hóa lưu trữ nhị phân BYTEA (16 bytes) thay vì string hex 32 ký tự để giảm chi phí mạng & DB
	TraceID []byte
	// Idle: Hạn mức thời gian thực thi tối đa (Timeout) tính bằng giây. Tránh treo worker vô hạn.
	Idle uint32
	// ErrorCode: Mã lỗi phản hồi từ Dataplane gửi về nếu có sự cố xảy ra
	ErrorCode *string
	// ErrorMessage: Mô tả chi tiết thông tin lỗi phản hồi từ hệ thống
	ErrorMessage *string
}
