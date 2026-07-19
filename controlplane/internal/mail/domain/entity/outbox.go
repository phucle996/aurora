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

	// OutboxStatusProcessing: Dataplane/projector đã nhận job từ Redis Stream.
	OutboxStatusProcessing OutboxStatus = "PROCESSING"

	// OutboxStatusSucceeded: Executor/projector hoàn thành thành công.
	OutboxStatusSucceeded OutboxStatus = "SUCCEEDED"

	// OutboxStatusFailed: Executor/projector trả lỗi terminal.
	OutboxStatusFailed OutboxStatus = "FAILED"
)

// MailOutboxRecord định nghĩa routing envelope bền vững dùng cho CDC.
type MailOutboxRecord struct {
	// ID: Khóa chính tự tăng (BIGSERIAL) giúp tối ưu đánh chỉ mục vật lý và xác định thứ tự tuần tự trong Postgres
	ID int64
	// EventID: UUID định danh duy nhất toàn cục của sự kiện, dùng làm Idempotency Key chống trùng lặp giữa các node
	EventID uuid.UUID
	// RoutingScope: Zone đích dạng zone:<uuid>, tạo từ trusted X-Zone-ID sau khi cross-check Workspace.
	RoutingScope string
	// JobTopic: Discriminator của dispatcher (ví dụ: "mail.consumer.upsert").
	JobTopic string
	// Payload: Nội dung nhị phân (Protobuf) chứa tham số cấu hình hoặc dữ liệu chi tiết của công việc
	Payload []byte
	// ActorUserID: Caller tạo intent; có thể nil với platform/system job và không phải billing owner.
	ActorUserID *uuid.UUID
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
