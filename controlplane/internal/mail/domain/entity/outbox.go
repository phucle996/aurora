package mailEntity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// OutboxStatus đại diện cho trạng thái hiện tại của công việc trong Outbox
type OutboxStatus string

const (
	// OutboxStatusPending: Công việc mới được tạo, chờ xử lý/CDC đẩy đi
	OutboxStatusPending   OutboxStatus = "PENDING"
	// OutboxStatusPublished: Công việc đã được chuyển tiếp sang hàng đợi Redis Stream thành công
	OutboxStatusPublished OutboxStatus = "PUBLISHED"
	// OutboxStatusFailed: Công việc xử lý thất bại (quá số lần retry hoặc lỗi không thể phục hồi)
	OutboxStatusFailed    OutboxStatus = "FAILED"
)

// MailOutboxRecord định nghĩa cấu trúc của bản ghi sự kiện Outbox dùng cho việc giao tiếp phi tập trung (CDC)
type MailOutboxRecord struct {
	// ID: Khóa chính tự tăng (BIGSERIAL) giúp tối ưu đánh chỉ mục vật lý và xác định thứ tự tuần tự trong Postgres
	ID                 int64
	// EventID: UUID định danh duy nhất toàn cục của sự kiện, dùng làm Idempotency Key chống trùng lặp giữa các node
	EventID            uuid.UUID
	// ZoneID: UUID phân vùng tài nguyên (Multi-tenancy), xác định Redis Stream đích của dataplane
	ZoneID             uuid.UUID
	// JobTopic: Tên loại công việc hoặc topic sự kiện (ví dụ: "mail.test_connection")
	JobTopic           string
	// PayloadJSON: Nội dung JSON chứa tham số cấu hình hoặc dữ liệu chi tiết của công việc
	PayloadJSON        json.RawMessage
	// Status: Trạng thái xử lý sự kiện (PENDING, PUBLISHED, FAILED)
	Status             OutboxStatus
	// Attempts: Số lần thử thực thi/phát hành lại sự kiện
	Attempts           int
	// LastAttempt: Mốc thời gian của lần thử xử lý gần nhất
	LastAttempt        *time.Time
	// CreatedAt: Thời gian bản ghi Outbox được tạo ra
	CreatedAt          time.Time
	// JobVersion: Phiên bản logic của Job chạy ở Dataplane
	JobVersion         uint32
	// ResourceID: Định danh tài nguyên đích liên quan đến sự kiện (ví dụ: SMTP endpoint ID)
	ResourceID         string
	// PayloadSchemaVersion: Phiên bản cấu trúc dữ liệu của Payload (Schema versioning)
	PayloadSchemaVersion uint32
	// TraceID: Mã định danh phân tán (OpenTelemetry Trace ID) dùng để liên kết chuỗi vết hoạt động (Distributed Tracing)
	TraceID            string
	// Idle: Thời gian Lease tính bằng giây. Tránh các worker khác tranh giành job khi đang xử lý
	Idle               uint32
	// ErrorCode: Mã lỗi phản hồi từ Dataplane gửi về nếu có sự cố xảy ra
	ErrorCode          *string
	// ErrorMessage: Mô tả chi tiết thông tin lỗi phản hồi từ hệ thống
	ErrorMessage       *string
}

