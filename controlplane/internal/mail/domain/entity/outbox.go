package mailEntity

import (
	"time"

	"github.com/google/uuid"
)

// OutboxStatus đại diện cho trạng thái hiện tại của công việc trong mail outbox duy nhất.
type OutboxStatus string

const (
	// OutboxStatusPending: Trạng thái ban đầu khi mail job/config event được commit bền vững.
	OutboxStatusPending OutboxStatus = "PENDING"

	// OutboxStatusProcessing: JO/Dataplane projector đã nhận durable command từ Kafka transport.
	OutboxStatusProcessing OutboxStatus = "PROCESSING"

	// OutboxStatusSucceeded: Executor/projector hoàn thành thành công.
	OutboxStatusSucceeded OutboxStatus = "SUCCEEDED"

	// OutboxStatusFailed: Executor/projector trả lỗi terminal.
	OutboxStatusFailed OutboxStatus = "FAILED"
)

// MailOutboxRecord định nghĩa routing envelope bền vững dùng cho CDC.
// [COMMENT]: ActorUserID là uuid.UUID cụ thể — không bao giờ nil vì outbox record BẮT BUỘC phải có actor.
// Caller (service layer) phải cung cấp actor trước khi tạo outbox record.
type MailOutboxRecord struct {
	// ID: Khóa chính tự tăng (BIGSERIAL) giúp tối ưu đánh chỉ mục vật lý và xác định thứ tự tuần tự trong Postgres
	ID int64
	// EventID: UUID định danh duy nhất toàn cục của sự kiện, dùng làm Idempotency Key chống trùng lặp giữa các node
	EventID uuid.UUID
	// ZoneID: UUID Zone đã được service cross-check với Workspace trước khi tạo immutable outbox envelope.
	ZoneID uuid.UUID
	// JobTopic: Discriminator của dispatcher (ví dụ: "mail.consumer.upsert").
	JobTopic string
	// Payload: Nội dung nhị phân (Protobuf) chứa tham số cấu hình hoặc dữ liệu chi tiết của công việc
	Payload []byte
	// PayloadKeyID remains queryable outside ciphertext so key drain/retirement
	// never has to parse or decrypt historical outbox bytes.
	PayloadKeyID uuid.UUID
	// ActorUserID: Caller tạo configuration intent; là audit actor và không phải billing owner.
	// [COMMENT]: Bắt buộc — không pointer vì không có khả năng nil trong bất kỳ luồng nào.
	ActorUserID uuid.UUID
	// Status: Trạng thái xử lý sự kiện (PENDING, PROCESSING, SUCCEEDED, FAILED)
	Status OutboxStatus
	// CompletedAt: Thời điểm công việc hoàn tất xử lý (succeeded hoặc failed)
	CompletedAt *time.Time
	// JobVersion: Phiên bản logic của Job chạy ở Dataplane
	JobVersion uint32
	// ResourceID: Định danh aggregate đích liên quan đến sự kiện.
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
